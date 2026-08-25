//! lm-core — 核心类型与 trait 定义
//!
//! 提供 SFU/MCU 媒体节点所需的基础抽象：ID newtype、音频帧、编解码类型、
//! Sans-IO Transport trait、AudioCodec trait、EgressSubscriber trait 等。

use serde::{Deserialize, Serialize};

// ============================================================================
// ID newtype
// ============================================================================

/// 会话 ID
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct SessionId(pub String);

/// 端点 ID
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct EndpointId(pub String);

/// 轨道 ID
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct TrackId(pub String);

/// 房间 ID（会议场景）
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct RoomId(pub String);

// ============================================================================
// 音频帧
// ============================================================================

/// PCM 音频帧（i16 交错样本）
#[derive(Debug, Clone)]
pub struct AudioFrame {
    /// 交错 PCM 样本（i16, little-endian in memory）
    pub samples: Vec<i16>,
    /// 采样率（如 8000/16000/48000）
    pub sample_rate: u32,
    /// 时间戳（RTP 时间戳基准，按 clock_rate 递增）
    pub timestamp: u64,
}

// ============================================================================
// 编解码类型
// ============================================================================

/// 编解码类型枚举
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum CodecType {
    /// Opus — WebRTC 默认音频编码
    Opus,
    /// G.711 µ-law — PSTN/SIP
    PcmU,
    /// G.711 A-law — PSTN/SIP
    PcmA,
    /// G.722 — 宽带语音
    G722,
    /// 原始 PCM
    Pcm,
    /// H.264 — 视频编码
    H264,
    /// VP8 — 视频编码
    Vp8,
    /// VP9 — 视频编码
    Vp9,
}

impl CodecType {
    /// 判断是否为音频编解码
    pub fn is_audio(self) -> bool {
        matches!(self, CodecType::Opus | CodecType::PcmU | CodecType::PcmA | CodecType::G722 | CodecType::Pcm)
    }
    /// 判断是否为视频编解码
    pub fn is_video(self) -> bool {
        matches!(self, CodecType::H264 | CodecType::Vp8 | CodecType::Vp9)
    }
}

/// 轨道类型（音频/视频）
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum TrackKind {
    Audio,
    Video,
}

impl TrackKind {
    pub fn is_audio(self) -> bool { matches!(self, TrackKind::Audio) }
    pub fn is_video(self) -> bool { matches!(self, TrackKind::Video) }
}

// ============================================================================
// 媒体方向
// ============================================================================

/// 媒体方向（对应 SDP direction）
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum Direction {
    /// 收发双向
    SendRecv,
    /// 仅发送
    SendOnly,
    /// 仅接收
    RecvOnly,
    /// 不活跃
    Inactive,
}

// ============================================================================
// 背压策略
// ============================================================================

/// Egress 背压策略
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Backpressure {
    /// 阻塞生产者（反压）
    Block,
    /// 丢弃最旧的帧
    DropOldest,
    /// 丢弃最新的帧
    DropNewest,
}

// ============================================================================
// Transport trait（Sans-IO）
// ============================================================================

/// Sans-IO Transport trait
///
/// 实现者不直接持有 IO 资源，而是由驱动层（tokio task）调用 `on_tick`/`on_input`
/// 并通过 `poll_output` 取出待发送的数据。这样便于测试与平台无关。
pub trait Transport {
    /// 输入数据（如收到的 RTP 包）
    fn on_input(&mut self, input: &[u8]);

    /// 时钟驱动（如 20ms tick）
    fn on_tick(&mut self, now_ms: u64);

    /// 轮询输出（如待发送的 RTP 包）
    ///
    /// 返回 `Some(buf)` 表示有数据待发送；`None` 表示暂无输出。
    fn poll_output(&mut self) -> Option<Vec<u8>>;
}

// ============================================================================
// AudioCodec trait
// ============================================================================

/// 音频编解码 trait
pub trait AudioCodec: Send {
    /// 编码 PCM 帧为压缩数据
    fn encode(&mut self, frame: &AudioFrame) -> anyhow::Result<Vec<u8>>;

    /// 解码压缩数据为 PCM 帧
    fn decode(&mut self, payload: &[u8], timestamp: u64) -> anyhow::Result<AudioFrame>;

    /// 编解码类型
    fn codec_type(&self) -> CodecType;
}

// ============================================================================
// EgressSubscriber trait
// ============================================================================

/// Egress 订阅者 trait
///
/// 由下游（pacer/relay/录制）实现，接收转发出来的帧。
pub trait EgressSubscriber: Send {
    /// 收到一帧
    fn on_frame(&mut self, frame: &AudioFrame);

    /// 背压反馈：当下游处理不过来时调用
    fn backpressure(&self) -> Backpressure;
}
