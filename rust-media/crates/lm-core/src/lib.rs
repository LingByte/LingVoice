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

// ============================================================================
// MediaFrame — 帧级抽象（参考 Xiu FrameData + atm0s MediaPacket）
// ============================================================================

/// 媒体帧（已从 RTP 包组装完成的完整编码帧）
///
/// 这是协议无关的帧抽象：
/// - 视频帧：从 RTP 包序列组装而来（VP8 descriptor 已剥离、H264 FU-A 已重组）
/// - 音频帧：Opus 通常一个 RTP 包 = 一帧
///
/// 与 `AudioFrame`（PCM 样本）的区别：
/// - `MediaFrame` 是编码后的压缩帧（VP8/H264/Opus payload）
/// - `AudioFrame` 是解码后的 PCM 样本
#[derive(Debug, Clone)]
pub struct MediaFrame {
    /// 帧类型
    pub kind: TrackKind,
    /// 编解码类型
    pub codec: CodecType,
    /// RTP 时间戳（90kHz for video, codec-specific for audio）
    pub timestamp: u32,
    /// 是否关键帧
    pub keyframe: bool,
    /// simulcast 空间层（0=base, 1=mid, 2=high），非 simulcast 为 0
    pub spatial_layer: u8,
    /// simulcast 时间层
    pub temporal_layer: u8,
    /// 帧数据（已剥离 RTP descriptor，完整的编码帧 payload）
    pub data: bytes::Bytes,
    /// 源 SSRC
    pub ssrc: u32,
    /// simulcast RID（如 "low"/"mid"/"high"），非 simulcast 为空
    pub rid: String,
}

impl MediaFrame {
    /// 创建音频帧
    pub fn audio(codec: CodecType, timestamp: u32, data: bytes::Bytes, ssrc: u32) -> Self {
        Self {
            kind: TrackKind::Audio,
            codec,
            timestamp,
            keyframe: false,
            spatial_layer: 0,
            temporal_layer: 0,
            data,
            ssrc,
            rid: String::new(),
        }
    }

    /// 创建视频帧
    pub fn video(
        codec: CodecType,
        timestamp: u32,
        data: bytes::Bytes,
        ssrc: u32,
        keyframe: bool,
    ) -> Self {
        Self {
            kind: TrackKind::Video,
            codec,
            timestamp,
            keyframe,
            spatial_layer: 0,
            temporal_layer: 0,
            data,
            ssrc,
            rid: String::new(),
        }
    }

    /// 帧大小（字节）
    pub fn len(&self) -> usize {
        self.data.len()
    }

    /// 是否为空帧
    pub fn is_empty(&self) -> bool {
        self.data.is_empty()
    }
}

// ============================================================================
// Depacketizer trait — RTP→Frame 解包（参考 atm0s PacketSelector）
// ============================================================================

/// RTP 解包结果
#[derive(Debug, Clone, PartialEq)]
pub enum DepacketizeResult {
    /// 需要更多包
    NeedMore,
    /// 帧已组装完成
    FrameComplete,
    /// 包错误（序列号跳跃、descriptor 解析失败等）
    Error(String),
}

/// RTP 解包器：将 RTP 包序列组装为完整媒体帧
///
/// 各编解码器实现此 trait：
/// - VP8: 解析 payload descriptor (RFC 7741)，按 S bit 和 marker 组装帧
/// - H264: 重组 FU-A 分片，按 marker 组装帧
/// - Opus: 通常一个 RTP 包 = 一帧
pub trait Depacketizer: Send + Sync {
    /// 处理一个 RTP 包
    ///
    /// `payload` 是 RTP payload（已剥离 RTP header）
    /// `marker` 是 RTP marker 位
    /// `sequence_number` 是 RTP 序列号
    /// `timestamp` 是 RTP 时间戳
    fn push_packet(
        &mut self,
        payload: &[u8],
        marker: bool,
        sequence_number: u16,
        timestamp: u32,
    ) -> DepacketizeResult;

    /// 取出组装完成的帧
    ///
    /// 返回 `Some(frame)` 如果有完整帧，否则 `None`
    /// 调用后内部缓冲区会被清空
    fn take_frame(&mut self) -> Option<MediaFrame>;

    /// 重置状态（丢弃当前不完整的帧）
    fn reset(&mut self);

    /// 编解码类型
    fn codec_type(&self) -> CodecType;
}

// ============================================================================
// Packetizer trait — Frame→RTP 打包
// ============================================================================

/// RTP 打包参数
#[derive(Debug, Clone)]
pub struct PacketizeParams {
    /// SSRC
    pub ssrc: u32,
    /// Payload type
    pub payload_type: u8,
    /// 起始序列号
    pub start_sequence: u16,
    /// 起始时间戳
    pub start_timestamp: u32,
    /// 时钟率
    pub clock_rate: u32,
}

/// RTP 打包器：将媒体帧拆分为 RTP 包
pub trait Packetizer: Send + Sync {
    /// 将帧打包为 RTP payload 列表
    ///
    /// 返回 (payload, marker) 列表，调用方负责添加 RTP header
    fn packetize(&mut self, frame: &MediaFrame, max_payload_size: usize) -> Vec<(bytes::Bytes, bool)>;

    /// 编解码类型
    fn codec_type(&self) -> CodecType;
}

// ============================================================================
// StreamSink trait — 流订阅者接口
// ============================================================================

/// 流订阅者：接收媒体帧或 RTP 包
///
/// 两种订阅模式：
/// - **源流订阅**：收到 `MediaFrame`（已组装完整），用于录制、转封装
/// - **转发订阅**：收到 `RtpPacket`（零拷贝直转），用于 SFU 转发
pub trait StreamSink: Send + Sync {
    /// 收到一个完整的媒体帧（源流订阅模式）
    fn on_frame(&self, frame: &MediaFrame);

    /// 背压反馈
    fn backpressure(&self) -> Backpressure {
        Backpressure::DropOldest
    }
}
