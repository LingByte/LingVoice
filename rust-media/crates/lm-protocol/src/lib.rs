//! lm-protocol — 协议适配器 + 转封装框架
//!
//! 参考 Xiu StreamHub 的 demuxer/remuxer 模型 + Monibuca 插件化协议。
//!
//! 架构：
//! ```text
//! Protocol Ingest (RTMP/RTSP/WebRTC/SRT/GB28181)
//!     ↓
//! Demuxer → MediaFrame
//!     ↓
//! StreamHub (lm-stream)
//!     ↓
//! Remuxer → Protocol Output (HLS/RTMP/HTTP-FLV/WebRTC/WHEP)
//! ```
//!
//! Phase 5: 协议转封装框架 + HLS/RTMP/HTTP-FLV remuxer
//! Phase 6-10: 各协议 ingest/output 适配器

use std::sync::Arc;
use lm_core::{CodecType, MediaFrame, StreamSink, TrackKind};
use tracing::{info, warn};

// ============================================================================
// 协议类型
// ============================================================================

/// 支持的协议
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Protocol {
    /// WebRTC (WHIP/WHEP)
    WebRtc,
    /// RTMP
    Rtmp,
    /// RTSP
    Rtsp,
    /// HTTP-FLV
    HttpFlv,
    /// HLS
    Hls,
    /// LL-HLS (Low Latency HLS)
    LlHls,
    /// SRT (Secure Reliable Transport)
    Srt,
    /// GB28181
    Gb28181,
}

impl Protocol {
    pub fn as_str(&self) -> &'static str {
        match self {
            Protocol::WebRtc => "webrtc",
            Protocol::Rtmp => "rtmp",
            Protocol::Rtsp => "rtsp",
            Protocol::HttpFlv => "http-flv",
            Protocol::Hls => "hls",
            Protocol::LlHls => "ll-hls",
            Protocol::Srt => "srt",
            Protocol::Gb28181 => "gb28181",
        }
    }
}

/// 流方向
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StreamDirection {
    /// 推流（ingest）
    Ingest,
    /// 拉流（output）
    Output,
}

// ============================================================================
// Demuxer — 协议输入 → MediaFrame
// ============================================================================

/// Demuxer trait：协议输入 → MediaFrame
///
/// 各协议适配器实现此 trait，将协议特定格式转为统一的 MediaFrame。
/// 例如 RTMP demuxer 解析 FLV tag → MediaFrame。
pub trait Demuxer: Send + Sync {
    /// 协议类型
    fn protocol(&self) -> Protocol;

    /// 处理输入数据，返回产生的 MediaFrame 列表
    ///
    /// 大多数协议一次输入产生 0 或 1 个帧（可能需要缓冲）。
    fn push_data(&mut self, data: &[u8]) -> Vec<MediaFrame>;

    /// 重置 demuxer 状态
    fn reset(&mut self);
}

// ============================================================================
// Remuxer — MediaFrame → 协议输出
// ============================================================================

/// Remuxer trait：MediaFrame → 协议输出
///
/// 各协议适配器实现此 trait，将统一的 MediaFrame 转为协议特定格式。
/// 例如 HLS remuxer 将 MediaFrame 转为 TS 分段 + m3u8 playlist。
pub trait Remuxer: Send + Sync {
    /// 协议类型
    fn protocol(&self) -> Protocol;

    /// 处理一帧，返回协议输出数据
    ///
    /// 返回的 Vec<u8> 是该帧对应的协议数据（可能为空，如 HLS 需要攒够一个分段）。
    fn push_frame(&mut self, frame: &MediaFrame) -> Vec<u8>;

    /// 刷新缓冲，返回剩余数据
    ///
    /// 用于停止时强制输出未完成的数据。
    fn flush(&mut self) -> Vec<u8>;

    /// 重置 remuxer 状态
    fn reset(&mut self);
}

// ============================================================================
// 协议转封装管道
// ============================================================================

/// 协议转封装管道
///
/// 从源流订阅 MediaFrame，通过 Remuxer 转为目标协议格式，输出到 sink。
///
/// ```text
/// MediaStream → StreamSink → Remuxer → Protocol Output
/// ```
pub struct RemuxPipeline {
    /// 目标协议
    pub protocol: Protocol,
    /// Remuxer
    remuxer: Box<dyn Remuxer>,
    /// 输出 sink（如 HTTP response writer、TCP stream 等）
    output: Box<dyn ProtocolOutput>,
}

impl RemuxPipeline {
    pub fn new(protocol: Protocol, remuxer: Box<dyn Remuxer>, output: Box<dyn ProtocolOutput>) -> Self {
        Self { protocol, remuxer, output }
    }

    /// 处理一帧
    pub fn push_frame(&mut self, frame: &MediaFrame) {
        let data = self.remuxer.push_frame(frame);
        if !data.is_empty() {
            if let Err(e) = self.output.write(&data) {
                warn!(protocol = self.protocol.as_str(), error = %e, "protocol output write failed");
            }
        }
    }

    /// 刷新并关闭
    pub fn flush_and_close(&mut self) {
        let data = self.remuxer.flush();
        if !data.is_empty() {
            let _ = self.output.write(&data);
        }
        let _ = self.output.close();
    }
}

/// 协议输出 trait
pub trait ProtocolOutput: Send + Sync {
    /// 写入数据
    fn write(&mut self, data: &[u8]) -> anyhow::Result<()>;

    /// 关闭输出
    fn close(&mut self) -> anyhow::Result<()>;
}

// ============================================================================
// Sink 适配器 — 将 RemuxPipeline 桥接为 StreamSink
// ============================================================================

/// RemuxSink — 实现 StreamSink，将 MediaFrame 转发给 RemuxPipeline
///
/// 使用 parking_lot::Mutex 包装 RemuxPipeline 使其可跨线程共享。
pub struct RemuxSink {
    pipeline: parking_lot::Mutex<RemuxPipeline>,
}

impl RemuxSink {
    pub fn new(pipeline: RemuxPipeline) -> Self {
        Self {
            pipeline: parking_lot::Mutex::new(pipeline),
        }
    }
}

impl StreamSink for RemuxSink {
    fn on_frame(&self, frame: &MediaFrame) {
        let mut pipeline = self.pipeline.lock();
        pipeline.push_frame(frame);
    }
}

// ============================================================================
// Phase 5: HLS Remuxer
// ============================================================================

mod hls;
pub use hls::{HlsRemuxer, HlsPlaylist};

// ============================================================================
// Phase 5: HTTP-FLV Remuxer
// ============================================================================

mod flv;
pub use flv::{FlvRemuxer, FlvHeader};

// ============================================================================
// Phase 5: RTMP Remuxer（输出侧）
// ============================================================================

mod rtmp;
pub use rtmp::{RtmpRemuxer, RtmpHandshake};

// ============================================================================
// Phase 7: RTSP Demuxer/Remuxer
// ============================================================================

mod rtsp;
pub use rtsp::{RtspDemuxer, RtspRemuxer};

// ============================================================================
// Phase 8: SRT Demuxer/Remuxer
// ============================================================================

mod srt;
pub use srt::{SrtDemuxer, SrtRemuxer};

// ============================================================================
// Phase 9: GB28181 Demuxer
// ============================================================================

mod gb28181;
pub use gb28181::{Gb28181Demuxer, Gb28181Session};

// ============================================================================
// Phase 10: WHIP/WHEP（WebRTC 信令）
// ============================================================================

mod whip_whep;
pub use whip_whep::{WhipHandler, WhepHandler, WhipWhepConfig};
