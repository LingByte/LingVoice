//! MQTT 媒体传输协议适配器
//!
//! MQTT 在流媒体场景中主要用于 IoT 设备的媒体传输：
//! - 设备通过 MQTT 发布音频/视频帧（作为 payload）
//! - 服务器订阅 MQTT topic 接收媒体
//! - 服务器也可以通过 MQTT 向设备推送媒体
//!
//! 本模块只实现消息编解码和 topic 管理，不实现 MQTT 协议本身。
//! 实际的 MQTT 连接由 Go 层或外部 broker 处理。
//!
//! ## Topic 结构
//!
//! ```text
//! lingvoice/{device_id}/audio/up   — 设备上传音频
//! lingvoice/{device_id}/audio/down — 服务器下发音频
//! lingvoice/{device_id}/video/up   — 设备上传视频
//! lingvoice/{device_id}/video/down — 服务器下发视频
//! lingvoice/{device_id}/control    — 控制信令
//! ```
//!
//! ## Payload 格式（二进制）
//!
//! ```text
//! [1 byte: version] [1 byte: codec] [4 bytes: timestamp] [2 bytes: seq] [payload...]
//! ```

use lm_core::{CodecType, MediaFrame, TrackKind};

// ============================================================================
// 常量
// ============================================================================

/// 当前协议版本
const MQTT_MEDIA_VERSION: u8 = 1;

/// 媒体消息头长度（version + codec + timestamp + seq）
const MQTT_MEDIA_HEADER_LEN: usize = 1 + 1 + 4 + 2;

/// Topic 前缀
const TOPIC_PREFIX: &str = "lingvoice";

// ============================================================================
// CodecType <-> u8 映射
// ============================================================================

/// 将 CodecType 映射为 u8
fn codec_to_u8(codec: CodecType) -> u8 {
    match codec {
        CodecType::Opus => 0,
        CodecType::PcmU => 1,
        CodecType::PcmA => 2,
        CodecType::G722 => 3,
        CodecType::Pcm => 4,
        CodecType::Aac => 5,
        CodecType::Mp3 => 6,
        CodecType::H264 => 7,
        CodecType::H265 => 8,
        CodecType::Vp8 => 9,
        CodecType::Vp9 => 10,
        CodecType::Av1 => 11,
    }
}

/// 将 u8 映射为 CodecType
fn codec_from_u8(v: u8) -> Option<CodecType> {
    match v {
        0 => Some(CodecType::Opus),
        1 => Some(CodecType::PcmU),
        2 => Some(CodecType::PcmA),
        3 => Some(CodecType::G722),
        4 => Some(CodecType::Pcm),
        5 => Some(CodecType::Aac),
        6 => Some(CodecType::Mp3),
        7 => Some(CodecType::H264),
        8 => Some(CodecType::H265),
        9 => Some(CodecType::Vp8),
        10 => Some(CodecType::Vp9),
        11 => Some(CodecType::Av1),
        _ => None,
    }
}

// ============================================================================
// 错误
// ============================================================================

/// MQTT 错误
#[derive(Debug, thiserror::Error)]
pub enum MqttError {
    #[error("payload 太短: {0} bytes")]
    PayloadTooShort(usize),
    #[error("不支持的版本: {0}")]
    UnsupportedVersion(u8),
    #[error("未知的 codec: {0}")]
    UnknownCodec(u8),
    #[error("未知的 media type: {0}")]
    UnknownMediaType(u8),
    #[error("未知的 direction: {0}")]
    UnknownDirection(u8),
    #[error("JSON 序列化错误: {0}")]
    Json(#[from] serde_json::Error),
    #[error("topic 格式错误: {0}")]
    InvalidTopic(String),
}

// ============================================================================
// 媒体类型 / 方向
// ============================================================================

/// MQTT 媒体类型
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MqttMediaType {
    Audio,
    Video,
    Control,
}

impl MqttMediaType {
    /// 转为 topic 中的字符串片段
    #[allow(dead_code)]
    fn as_topic_segment(&self) -> &'static str {
        match self {
            MqttMediaType::Audio => "audio",
            MqttMediaType::Video => "video",
            MqttMediaType::Control => "control",
        }
    }

    /// 从 topic 中的字符串片段解析
    fn from_topic_segment(s: &str) -> Option<Self> {
        match s {
            "audio" => Some(MqttMediaType::Audio),
            "video" => Some(MqttMediaType::Video),
            "control" => Some(MqttMediaType::Control),
            _ => None,
        }
    }

    /// 转为 TrackKind（Control 无对应）
    fn to_track_kind(self) -> Option<TrackKind> {
        match self {
            MqttMediaType::Audio => Some(TrackKind::Audio),
            MqttMediaType::Video => Some(TrackKind::Video),
            MqttMediaType::Control => None,
        }
    }
}

/// MQTT 方向
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MqttDirection {
    /// 设备 -> 服务器
    Up,
    /// 服务器 -> 设备
    Down,
}

impl MqttDirection {
    /// 转为 topic 中的字符串片段
    #[allow(dead_code)]
    fn as_topic_segment(&self) -> &'static str {
        match self {
            MqttDirection::Up => "up",
            MqttDirection::Down => "down",
        }
    }

    /// 从 topic 中的字符串片段解析
    fn from_topic_segment(s: &str) -> Option<Self> {
        match s {
            "up" => Some(MqttDirection::Up),
            "down" => Some(MqttDirection::Down),
            _ => None,
        }
    }
}

// ============================================================================
// 媒体消息头 / 消息
// ============================================================================

/// MQTT 媒体消息头
#[derive(Debug, Clone)]
pub struct MqttMediaHeader {
    pub version: u8,
    pub codec: CodecType,
    pub timestamp: u32,
    pub sequence: u16,
}

/// MQTT 媒体消息（header + payload）
#[derive(Debug, Clone)]
pub struct MqttMediaMessage {
    pub header: MqttMediaHeader,
    pub payload: Vec<u8>,
}

impl MqttMediaMessage {
    /// 编码为二进制 payload
    ///
    /// 格式：`[version][codec][timestamp(4, BE)][seq(2, BE)][payload...]`
    pub fn encode(&self) -> Vec<u8> {
        let mut buf = Vec::with_capacity(MQTT_MEDIA_HEADER_LEN + self.payload.len());
        buf.push(self.header.version);
        buf.push(codec_to_u8(self.header.codec));
        buf.extend_from_slice(&self.header.timestamp.to_be_bytes());
        buf.extend_from_slice(&self.header.sequence.to_be_bytes());
        buf.extend_from_slice(&self.payload);
        buf
    }

    /// 从二进制 payload 解码
    pub fn decode(data: &[u8]) -> Result<Self, MqttError> {
        if data.len() < MQTT_MEDIA_HEADER_LEN {
            return Err(MqttError::PayloadTooShort(data.len()));
        }
        let version = data[0];
        if version != MQTT_MEDIA_VERSION {
            return Err(MqttError::UnsupportedVersion(version));
        }
        let codec = codec_from_u8(data[1]).ok_or(MqttError::UnknownCodec(data[1]))?;
        let timestamp = u32::from_be_bytes([data[2], data[3], data[4], data[5]]);
        let sequence = u16::from_be_bytes([data[6], data[7]]);
        let payload = data[MQTT_MEDIA_HEADER_LEN..].to_vec();
        Ok(Self {
            header: MqttMediaHeader {
                version,
                codec,
                timestamp,
                sequence,
            },
            payload,
        })
    }

    /// 从 MediaFrame 创建
    pub fn from_frame(frame: &MediaFrame, codec: CodecType, seq: u16) -> Self {
        Self {
            header: MqttMediaHeader {
                version: MQTT_MEDIA_VERSION,
                codec,
                timestamp: frame.timestamp,
                sequence: seq,
            },
            payload: frame.data.to_vec(),
        }
    }

    /// 转换为 MediaFrame
    pub fn to_frame(&self, track_kind: TrackKind) -> MediaFrame {
        MediaFrame {
            kind: track_kind,
            codec: self.header.codec,
            timestamp: self.header.timestamp,
            keyframe: false,
            spatial_layer: 0,
            temporal_layer: 0,
            data: bytes::Bytes::copy_from_slice(&self.payload),
            ssrc: 0,
            rid: String::new(),
        }
    }
}

// ============================================================================
// Topic 构建器
// ============================================================================

/// MQTT Topic 构建器
pub struct MqttTopicBuilder {
    device_id: String,
}

impl MqttTopicBuilder {
    pub fn new(device_id: &str) -> Self {
        Self {
            device_id: device_id.to_string(),
        }
    }

    /// `lingvoice/{id}/audio/up`
    pub fn audio_up(&self) -> String {
        format!("{TOPIC_PREFIX}/{}/audio/up", self.device_id)
    }

    /// `lingvoice/{id}/audio/down`
    pub fn audio_down(&self) -> String {
        format!("{TOPIC_PREFIX}/{}/audio/down", self.device_id)
    }

    /// `lingvoice/{id}/video/up`
    pub fn video_up(&self) -> String {
        format!("{TOPIC_PREFIX}/{}/video/up", self.device_id)
    }

    /// `lingvoice/{id}/video/down`
    pub fn video_down(&self) -> String {
        format!("{TOPIC_PREFIX}/{}/video/down", self.device_id)
    }

    /// `lingvoice/{id}/control`
    pub fn control(&self) -> String {
        format!("{TOPIC_PREFIX}/{}/control", self.device_id)
    }

    /// 从 topic 解析 device_id 和方向
    ///
    /// 支持的格式：
    /// - `lingvoice/{id}/audio/up`
    /// - `lingvoice/{id}/audio/down`
    /// - `lingvoice/{id}/video/up`
    /// - `lingvoice/{id}/video/down`
    /// - `lingvoice/{id}/control`
    pub fn parse_topic(topic: &str) -> Option<(String, MqttMediaType, MqttDirection)> {
        let parts: Vec<&str> = topic.split('/').collect();
        if parts.len() < 3 || parts[0] != TOPIC_PREFIX {
            return None;
        }
        let device_id = parts[1].to_string();
        let media_type = MqttMediaType::from_topic_segment(parts[2])?;
        // control topic 没有方向，统一视为 Up
        let direction = if media_type == MqttMediaType::Control {
            MqttDirection::Up
        } else {
            if parts.len() < 4 {
                return None;
            }
            MqttDirection::from_topic_segment(parts[3])?
        };
        Some((device_id, media_type, direction))
    }
}

// ============================================================================
// 媒体接收器
// ============================================================================

/// MQTT 媒体接收器（订阅 topic，解析消息）
pub struct MqttMediaReceiver {
    device_id: String,
    media_type: MqttMediaType,
    /// 期望的 codec
    expected_codec: Option<CodecType>,
    /// 接收统计
    packets_received: u64,
    bytes_received: u64,
    last_sequence: u16,
}

impl MqttMediaReceiver {
    pub fn new(device_id: &str, media_type: MqttMediaType) -> Self {
        Self {
            device_id: device_id.to_string(),
            media_type,
            expected_codec: None,
            packets_received: 0,
            bytes_received: 0,
            last_sequence: 0,
        }
    }

    /// 设置期望的 codec
    pub fn with_expected_codec(mut self, codec: CodecType) -> Self {
        self.expected_codec = Some(codec);
        self
    }

    /// 返回需要订阅的 topic
    ///
    /// 接收器订阅 "up" 方向（设备上传）。
    pub fn topic(&self) -> String {
        let builder = MqttTopicBuilder::new(&self.device_id);
        match self.media_type {
            MqttMediaType::Audio => builder.audio_up(),
            MqttMediaType::Video => builder.video_up(),
            MqttMediaType::Control => builder.control(),
        }
    }

    /// 处理收到的 MQTT 消息
    pub fn on_message(&mut self, payload: &[u8]) -> Result<MediaFrame, MqttError> {
        let msg = MqttMediaMessage::decode(payload)?;
        // 校验 codec（如果设置了期望）
        if let Some(expected) = self.expected_codec {
            if msg.header.codec != expected {
                return Err(MqttError::UnknownCodec(codec_to_u8(msg.header.codec)));
            }
        }
        // 更新统计
        self.packets_received += 1;
        self.bytes_received += payload.len() as u64;
        self.last_sequence = msg.header.sequence;
        // 转为 MediaFrame
        let track_kind = self.media_type.to_track_kind().unwrap_or(TrackKind::Audio);
        Ok(msg.to_frame(track_kind))
    }

    /// 接收统计 `(packets, bytes)`
    pub fn stats(&self) -> (u64, u64) {
        (self.packets_received, self.bytes_received)
    }

    /// 最近一次序列号
    pub fn last_sequence(&self) -> u16 {
        self.last_sequence
    }
}

// ============================================================================
// 媒体发送器
// ============================================================================

/// MQTT 媒体发送器（编码帧，发布到 topic）
pub struct MqttMediaSender {
    device_id: String,
    media_type: MqttMediaType,
    codec: CodecType,
    sequence: u16,
    /// 发送统计
    packets_sent: u64,
    bytes_sent: u64,
}

impl MqttMediaSender {
    pub fn new(device_id: &str, media_type: MqttMediaType, codec: CodecType) -> Self {
        Self {
            device_id: device_id.to_string(),
            media_type,
            codec,
            sequence: 0,
            packets_sent: 0,
            bytes_sent: 0,
        }
    }

    /// 返回需要发布的 topic
    ///
    /// 发送器发布到 "down" 方向（服务器下发）。
    pub fn topic(&self) -> String {
        let builder = MqttTopicBuilder::new(&self.device_id);
        match self.media_type {
            MqttMediaType::Audio => builder.audio_down(),
            MqttMediaType::Video => builder.video_down(),
            MqttMediaType::Control => builder.control(),
        }
    }

    /// 编码 MediaFrame 为 MQTT payload
    pub fn encode_frame(&mut self, frame: &MediaFrame) -> Vec<u8> {
        let seq = self.sequence;
        // 序列号自增并 wrap around（u16 自然回绕）
        self.sequence = self.sequence.wrapping_add(1);
        let msg = MqttMediaMessage::from_frame(frame, self.codec, seq);
        let payload = msg.encode();
        // 更新统计
        self.packets_sent += 1;
        self.bytes_sent += payload.len() as u64;
        payload
    }

    /// 发送统计 `(packets, bytes)`
    pub fn stats(&self) -> (u64, u64) {
        (self.packets_sent, self.bytes_sent)
    }

    /// 当前序列号
    pub fn sequence(&self) -> u16 {
        self.sequence
    }
}

// ============================================================================
// 控制消息（信令）
// ============================================================================

/// MQTT 控制消息（信令）
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub enum MqttControlMessage {
    /// 开始推流
    StartPublish {
        codec: CodecType,
        sample_rate: u32,
        channels: u16,
    },
    /// 停止推流
    StopPublish,
    /// 开始订阅
    StartSubscribe,
    /// 停止订阅
    StopSubscribe,
    /// 心跳
    Heartbeat { timestamp: u64 },
    /// 服务器请求关键帧
    RequestKeyframe,
}

impl MqttControlMessage {
    /// 编码为 JSON 字节
    pub fn encode(&self) -> Result<Vec<u8>, MqttError> {
        Ok(serde_json::to_vec(self)?)
    }

    /// 从 JSON 字节解码
    pub fn decode(data: &[u8]) -> Result<Self, MqttError> {
        Ok(serde_json::from_slice(data)?)
    }
}

// ============================================================================
// QoS (服务质量) 管理
// ============================================================================

/// MQTT QoS 级别
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MqttQos {
    /// 至多一次 (fire and forget)
    AtMostOnce,
    /// 至少一次 (需要确认)
    AtLeastOnce,
    /// 恰好一次 (需要确认 + 手势)
    ExactlyOnce,
}

impl MqttQos {
    pub fn as_u8(&self) -> u8 {
        match self {
            MqttQos::AtMostOnce => 0,
            MqttQos::AtLeastOnce => 1,
            MqttQos::ExactlyOnce => 2,
        }
    }

    pub fn from_u8(v: u8) -> Option<Self> {
        match v {
            0 => Some(MqttQos::AtMostOnce),
            1 => Some(MqttQos::AtLeastOnce),
            2 => Some(MqttQos::ExactlyOnce),
            _ => None,
        }
    }
}

/// QoS 消息跟踪器 — 跟踪未确认的消息
#[derive(Debug)]
pub struct QosTracker {
    /// QoS 级别
    qos: MqttQos,
    /// 待确认的消息 packet_id → (timestamp, retry_count)
    pending: std::collections::HashMap<u16, (u64, u8)>,
    /// 下一个 packet_id
    next_packet_id: u16,
    /// 最大重试次数
    max_retries: u8,
    /// 重试间隔 (ms)
    retry_interval_ms: u64,
}

impl QosTracker {
    pub fn new(qos: MqttQos) -> Self {
        Self {
            qos,
            pending: std::collections::HashMap::new(),
            next_packet_id: 1,
            max_retries: 5,
            retry_interval_ms: 1000,
        }
    }

    /// 注册一个待发送消息, 返回 packet_id
    pub fn register(&mut self, timestamp_ms: u64) -> u16 {
        if self.qos == MqttQos::AtMostOnce {
            return 0; // QoS 0 不需要 packet_id
        }
        let id = self.next_packet_id;
        self.next_packet_id = self.next_packet_id.wrapping_add(1);
        if self.next_packet_id == 0 {
            self.next_packet_id = 1; // 跳过 0
        }
        self.pending.insert(id, (timestamp_ms, 0));
        id
    }

    /// 确认消息已收到
    pub fn acknowledge(&mut self, packet_id: u16) -> bool {
        self.pending.remove(&packet_id).is_some()
    }

    /// 检查需要重试的消息, 返回需要重发的 packet_id 列表
    pub fn check_retries(&mut self, current_time_ms: u64) -> Vec<u16> {
        if self.qos == MqttQos::AtMostOnce {
            return Vec::new();
        }
        let mut to_retry = Vec::new();
        for (id, (ts, retries)) in self.pending.iter_mut() {
            if current_time_ms.saturating_sub(*ts) >= self.retry_interval_ms
                && *retries < self.max_retries
            {
                to_retry.push(*id);
                *retries += 1;
                *ts = current_time_ms;
            }
        }
        to_retry
    }

    /// 清理超过最大重试次数的消息
    pub fn cleanup_expired(&mut self) -> Vec<u16> {
        let expired: Vec<u16> = self
            .pending
            .iter()
            .filter(|(_, (_, retries))| *retries >= self.max_retries)
            .map(|(id, _)| *id)
            .collect();
        for id in &expired {
            self.pending.remove(id);
        }
        expired
    }

    /// 待确认消息数
    pub fn pending_count(&self) -> usize {
        self.pending.len()
    }

    /// QoS 级别
    pub fn qos(&self) -> MqttQos {
        self.qos
    }
}

// ============================================================================
// MQTT 会话状态管理
// ============================================================================

/// MQTT 会话状态
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MqttSessionState {
    /// 已断开
    Disconnected,
    /// 正在连接
    Connecting,
    /// 已连接
    Connected,
    /// 正在订阅
    Subscribing,
    /// 已订阅
    Subscribed,
    /// 正在发布
    Publishing,
    /// 错误
    Error,
}

/// MQTT 会话管理器 — 跟踪连接状态和订阅
#[derive(Debug)]
pub struct MqttSession {
    /// 设备 ID
    device_id: String,
    /// 当前状态
    state: MqttSessionState,
    /// 已订阅的 topic 列表
    subscribed_topics: Vec<String>,
    /// QoS 跟踪器 (发布方向)
    publish_qos: QosTracker,
    /// QoS 跟踪器 (订阅方向)
    subscribe_qos: QosTracker,
    /// 最后心跳时间
    last_heartbeat_ms: u64,
    /// 心跳超时 (ms)
    heartbeat_timeout_ms: u64,
    /// 接收统计
    packets_received: u64,
    /// 发送统计
    packets_sent: u64,
}

impl MqttSession {
    pub fn new(device_id: &str, qos: MqttQos) -> Self {
        Self {
            device_id: device_id.to_string(),
            state: MqttSessionState::Disconnected,
            subscribed_topics: Vec::new(),
            publish_qos: QosTracker::new(qos),
            subscribe_qos: QosTracker::new(qos),
            last_heartbeat_ms: 0,
            heartbeat_timeout_ms: 30_000, // 30s
            packets_received: 0,
            packets_sent: 0,
        }
    }

    /// 状态转换
    pub fn set_state(&mut self, state: MqttSessionState) {
        self.state = state;
    }

    pub fn state(&self) -> MqttSessionState {
        self.state
    }

    /// 添加订阅
    pub fn add_subscription(&mut self, topic: String) {
        if !self.subscribed_topics.contains(&topic) {
            self.subscribed_topics.push(topic);
        }
    }

    /// 移除订阅
    pub fn remove_subscription(&mut self, topic: &str) {
        self.subscribed_topics.retain(|t| t != topic);
    }

    /// 已订阅 topic 列表
    pub fn subscriptions(&self) -> &[String] {
        &self.subscribed_topics
    }

    /// 更新心跳
    pub fn update_heartbeat(&mut self, timestamp_ms: u64) {
        self.last_heartbeat_ms = timestamp_ms;
    }

    /// 检查心跳是否超时
    pub fn is_heartbeat_expired(&self, current_time_ms: u64) -> bool {
        current_time_ms.saturating_sub(self.last_heartbeat_ms) > self.heartbeat_timeout_ms
    }

    /// 记录接收
    pub fn record_received(&mut self) {
        self.packets_received += 1;
    }

    /// 记录发送
    pub fn record_sent(&mut self) {
        self.packets_sent += 1;
    }

    /// 统计
    pub fn stats(&self) -> (u64, u64) {
        (self.packets_received, self.packets_sent)
    }

    /// 发布 QoS 跟踪器
    pub fn publish_qos(&mut self) -> &mut QosTracker {
        &mut self.publish_qos
    }

    /// 订阅 QoS 跟踪器
    pub fn subscribe_qos(&mut self) -> &mut QosTracker {
        &mut self.subscribe_qos
    }

    /// 设备 ID
    pub fn device_id(&self) -> &str {
        &self.device_id
    }

    /// 断开连接, 清理状态
    pub fn disconnect(&mut self) {
        self.state = MqttSessionState::Disconnected;
        self.subscribed_topics.clear();
    }
}

// ============================================================================
// 测试
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;
    use bytes::Bytes;

    fn make_audio_frame(timestamp: u32, data: Vec<u8>) -> MediaFrame {
        MediaFrame::audio(CodecType::Opus, timestamp, Bytes::from(data), 12345)
    }

    #[test]
    fn test_mqtt_media_header_encode_decode() {
        let header = MqttMediaHeader {
            version: MQTT_MEDIA_VERSION,
            codec: CodecType::Opus,
            timestamp: 0x12345678,
            sequence: 0xABCD,
        };
        let msg = MqttMediaMessage {
            header: header.clone(),
            payload: vec![0x01, 0x02, 0x03],
        };
        let encoded = msg.encode();
        assert_eq!(encoded.len(), MQTT_MEDIA_HEADER_LEN + 3);
        // version
        assert_eq!(encoded[0], MQTT_MEDIA_VERSION);
        // codec
        assert_eq!(encoded[1], codec_to_u8(CodecType::Opus));
        // timestamp (BE)
        assert_eq!(&encoded[2..6], &0x12345678u32.to_be_bytes());
        // seq (BE)
        assert_eq!(&encoded[6..8], &0xABCDu16.to_be_bytes());
        // payload
        assert_eq!(&encoded[8..], &[0x01, 0x02, 0x03]);

        let decoded = MqttMediaMessage::decode(&encoded).unwrap();
        assert_eq!(decoded.header.version, header.version);
        assert_eq!(decoded.header.codec, header.codec);
        assert_eq!(decoded.header.timestamp, header.timestamp);
        assert_eq!(decoded.header.sequence, header.sequence);
        assert_eq!(decoded.payload, msg.payload);
    }

    #[test]
    fn test_mqtt_media_message_roundtrip() {
        for codec in [
            CodecType::Opus,
            CodecType::PcmU,
            CodecType::H264,
            CodecType::Av1,
        ] {
            let msg = MqttMediaMessage {
                header: MqttMediaHeader {
                    version: MQTT_MEDIA_VERSION,
                    codec,
                    timestamp: 99999,
                    sequence: 42,
                },
                payload: vec![0xDE, 0xAD, 0xBE, 0xEF],
            };
            let encoded = msg.encode();
            let decoded = MqttMediaMessage::decode(&encoded).unwrap();
            assert_eq!(decoded.header.codec, codec);
            assert_eq!(decoded.header.timestamp, 99999);
            assert_eq!(decoded.header.sequence, 42);
            assert_eq!(decoded.payload, msg.payload);
        }
    }

    #[test]
    fn test_mqtt_topic_builder() {
        let builder = MqttTopicBuilder::new("device-001");
        assert_eq!(builder.audio_up(), "lingvoice/device-001/audio/up");
        assert_eq!(builder.audio_down(), "lingvoice/device-001/audio/down");
        assert_eq!(builder.video_up(), "lingvoice/device-001/video/up");
        assert_eq!(builder.video_down(), "lingvoice/device-001/video/down");
        assert_eq!(builder.control(), "lingvoice/device-001/control");
    }

    #[test]
    fn test_mqtt_topic_parse() {
        // audio up
        let (id, mt, dir) = MqttTopicBuilder::parse_topic("lingvoice/device-001/audio/up").unwrap();
        assert_eq!(id, "device-001");
        assert_eq!(mt, MqttMediaType::Audio);
        assert_eq!(dir, MqttDirection::Up);

        // video down
        let (id, mt, dir) = MqttTopicBuilder::parse_topic("lingvoice/dev-2/video/down").unwrap();
        assert_eq!(id, "dev-2");
        assert_eq!(mt, MqttMediaType::Video);
        assert_eq!(dir, MqttDirection::Down);

        // control
        let (id, mt, _dir) = MqttTopicBuilder::parse_topic("lingvoice/dev-3/control").unwrap();
        assert_eq!(id, "dev-3");
        assert_eq!(mt, MqttMediaType::Control);

        // invalid
        assert!(MqttTopicBuilder::parse_topic("foo/bar/baz").is_none());
        assert!(MqttTopicBuilder::parse_topic("lingvoice/dev/audio").is_none()); // 缺方向
        assert!(MqttTopicBuilder::parse_topic("lingvoice/dev/unknown/up").is_none());
        assert!(MqttTopicBuilder::parse_topic("lingvoice/dev/audio/sideways").is_none());
    }

    #[test]
    fn test_mqtt_receiver() {
        let mut rx = MqttMediaReceiver::new("device-001", MqttMediaType::Audio);
        assert_eq!(rx.topic(), "lingvoice/device-001/audio/up");
        assert_eq!(rx.stats(), (0, 0));

        let frame = make_audio_frame(1000, vec![0x10, 0x20]);
        let msg = MqttMediaMessage::from_frame(&frame, CodecType::Opus, 1);
        let payload = msg.encode();

        let out = rx.on_message(&payload).unwrap();
        assert_eq!(out.kind, TrackKind::Audio);
        assert_eq!(out.codec, CodecType::Opus);
        assert_eq!(out.timestamp, 1000);
        assert_eq!(out.data.as_ref(), &[0x10, 0x20]);

        let (packets, bytes) = rx.stats();
        assert_eq!(packets, 1);
        assert_eq!(bytes, payload.len() as u64);
        assert_eq!(rx.last_sequence(), 1);
    }

    #[test]
    fn test_mqtt_sender() {
        let mut tx = MqttMediaSender::new("device-001", MqttMediaType::Audio, CodecType::Opus);
        assert_eq!(tx.topic(), "lingvoice/device-001/audio/down");
        assert_eq!(tx.stats(), (0, 0));

        let frame = make_audio_frame(2000, vec![0x30, 0x40]);
        let payload = tx.encode_frame(&frame);
        // 解码验证
        let msg = MqttMediaMessage::decode(&payload).unwrap();
        assert_eq!(msg.header.codec, CodecType::Opus);
        assert_eq!(msg.header.sequence, 0);
        assert_eq!(msg.header.timestamp, 2000);
        assert_eq!(msg.payload, vec![0x30, 0x40]);

        // 第二帧 seq 递增
        let frame2 = make_audio_frame(2010, vec![0x50]);
        let payload2 = tx.encode_frame(&frame2);
        let msg2 = MqttMediaMessage::decode(&payload2).unwrap();
        assert_eq!(msg2.header.sequence, 1);

        let (packets, bytes) = tx.stats();
        assert_eq!(packets, 2);
        assert_eq!(bytes, (payload.len() + payload2.len()) as u64);
    }

    #[test]
    fn test_mqtt_control_message_encode_decode() {
        let msg = MqttControlMessage::StartPublish {
            codec: CodecType::Opus,
            sample_rate: 48000,
            channels: 2,
        };
        let encoded = msg.encode().unwrap();
        let decoded = MqttControlMessage::decode(&encoded).unwrap();
        assert!(matches!(decoded, MqttControlMessage::StartPublish { .. }));
        if let MqttControlMessage::StartPublish {
            codec,
            sample_rate,
            channels,
        } = decoded
        {
            assert_eq!(codec, CodecType::Opus);
            assert_eq!(sample_rate, 48000);
            assert_eq!(channels, 2);
        }
    }

    #[test]
    fn test_mqtt_control_all_variants() {
        let cases = vec![
            MqttControlMessage::StartPublish {
                codec: CodecType::H264,
                sample_rate: 90000,
                channels: 1,
            },
            MqttControlMessage::StopPublish,
            MqttControlMessage::StartSubscribe,
            MqttControlMessage::StopSubscribe,
            MqttControlMessage::Heartbeat { timestamp: 123456 },
            MqttControlMessage::RequestKeyframe,
        ];
        for msg in cases {
            let encoded = msg.encode().unwrap();
            let decoded = MqttControlMessage::decode(&encoded).unwrap();
            // 验证 roundtrip 一致（通过再次编码比较）
            let re_encoded = decoded.encode().unwrap();
            assert_eq!(encoded, re_encoded);
        }
    }

    #[test]
    fn test_mqtt_sequence_wrap() {
        let mut tx = MqttMediaSender::new("dev", MqttMediaType::Video, CodecType::H264);
        // 设置 sequence 接近 u16::MAX
        tx.sequence = u16::MAX - 1;
        let frame = MediaFrame::video(CodecType::H264, 100, Bytes::from_static(&[0x01]), 1, true);
        // seq = u16::MAX - 1
        let p1 = tx.encode_frame(&frame);
        assert_eq!(
            MqttMediaMessage::decode(&p1).unwrap().header.sequence,
            u16::MAX - 1
        );
        // seq = u16::MAX
        let p2 = tx.encode_frame(&frame);
        assert_eq!(
            MqttMediaMessage::decode(&p2).unwrap().header.sequence,
            u16::MAX
        );
        // seq wrap -> 0
        let p3 = tx.encode_frame(&frame);
        assert_eq!(MqttMediaMessage::decode(&p3).unwrap().header.sequence, 0);
        // seq -> 1
        let p4 = tx.encode_frame(&frame);
        assert_eq!(MqttMediaMessage::decode(&p4).unwrap().header.sequence, 1);
    }

    #[test]
    fn test_mqtt_error_cases() {
        // payload 太短
        let short = [0x01, 0x00];
        let err = MqttMediaMessage::decode(&short).unwrap_err();
        assert!(matches!(err, MqttError::PayloadTooShort(2)));

        // 不支持的版本
        let mut bad_version = vec![0x09, 0x00, 0, 0, 0, 0, 0, 0];
        bad_version.extend_from_slice(&[0x01]);
        let err = MqttMediaMessage::decode(&bad_version).unwrap_err();
        assert!(matches!(err, MqttError::UnsupportedVersion(9)));

        // 未知的 codec
        let mut bad_codec = vec![MQTT_MEDIA_VERSION, 0xFF, 0, 0, 0, 0, 0, 0];
        bad_codec.extend_from_slice(&[0x01]);
        let err = MqttMediaMessage::decode(&bad_codec).unwrap_err();
        assert!(matches!(err, MqttError::UnknownCodec(0xFF)));

        // 无效 topic
        assert!(MqttTopicBuilder::parse_topic("invalid/topic").is_none());

        // 无效 JSON 控制消息
        let bad_json = b"{not valid json}";
        let err = MqttControlMessage::decode(bad_json).unwrap_err();
        assert!(matches!(err, MqttError::Json(_)));
    }

    #[test]
    fn test_mqtt_qos_levels() {
        assert_eq!(MqttQos::AtMostOnce.as_u8(), 0);
        assert_eq!(MqttQos::AtLeastOnce.as_u8(), 1);
        assert_eq!(MqttQos::ExactlyOnce.as_u8(), 2);
        assert_eq!(MqttQos::from_u8(0), Some(MqttQos::AtMostOnce));
        assert_eq!(MqttQos::from_u8(3), None);
    }

    #[test]
    fn test_qos_tracker_at_most_once() {
        let mut tracker = QosTracker::new(MqttQos::AtMostOnce);
        let id = tracker.register(1000);
        assert_eq!(id, 0); // QoS 0 不分配 packet_id
        assert_eq!(tracker.pending_count(), 0);
        // 重试检查不应返回任何消息
        let retries = tracker.check_retries(2000);
        assert!(retries.is_empty());
    }

    #[test]
    fn test_qos_tracker_at_least_once() {
        let mut tracker = QosTracker::new(MqttQos::AtLeastOnce);
        let id1 = tracker.register(1000);
        assert!(id1 > 0);
        let id2 = tracker.register(1000);
        assert!(id2 > 0 && id2 != id1);
        assert_eq!(tracker.pending_count(), 2);

        // 确认第一条
        assert!(tracker.acknowledge(id1));
        assert_eq!(tracker.pending_count(), 1);

        // 重复确认应失败
        assert!(!tracker.acknowledge(id1));
    }

    #[test]
    fn test_qos_tracker_retries() {
        let mut tracker = QosTracker::new(MqttQos::AtLeastOnce);
        let id = tracker.register(1000);
        assert_eq!(tracker.pending_count(), 1);

        // 时间未到, 不应重试
        let retries = tracker.check_retries(1500);
        assert!(retries.is_empty());

        // 时间到, 应重试
        let retries = tracker.check_retries(2500);
        assert_eq!(retries, vec![id]);
    }

    #[test]
    fn test_qos_tracker_cleanup() {
        let mut tracker = QosTracker::new(MqttQos::AtLeastOnce);
        let id = tracker.register(1000);

        // 模拟多次重试, 每次时间递增以触发 retry
        let mut time = 1000u64;
        for _ in 0..10 {
            time += 2000; // 超过 retry_interval
            tracker.check_retries(time);
        }
        let expired = tracker.cleanup_expired();
        assert!(
            expired.contains(&id),
            "id {} should be expired, expired={:?}",
            id,
            expired
        );
        assert_eq!(tracker.pending_count(), 0);
    }

    #[test]
    fn test_mqtt_session_state() {
        let mut session = MqttSession::new("device1", MqttQos::AtLeastOnce);
        assert_eq!(session.state(), MqttSessionState::Disconnected);
        assert_eq!(session.device_id(), "device1");

        session.set_state(MqttSessionState::Connecting);
        assert_eq!(session.state(), MqttSessionState::Connecting);

        session.set_state(MqttSessionState::Connected);
        assert_eq!(session.state(), MqttSessionState::Connected);
    }

    #[test]
    fn test_mqtt_session_subscriptions() {
        let mut session = MqttSession::new("device1", MqttQos::AtLeastOnce);
        session.add_subscription("lingvoice/device1/audio/up".to_string());
        session.add_subscription("lingvoice/device1/video/up".to_string());
        assert_eq!(session.subscriptions().len(), 2);

        // 重复添加不应增加
        session.add_subscription("lingvoice/device1/audio/up".to_string());
        assert_eq!(session.subscriptions().len(), 2);

        session.remove_subscription("lingvoice/device1/audio/up");
        assert_eq!(session.subscriptions().len(), 1);
    }

    #[test]
    fn test_mqtt_session_heartbeat() {
        let mut session = MqttSession::new("device1", MqttQos::AtLeastOnce);
        session.update_heartbeat(1000);
        assert!(!session.is_heartbeat_expired(31000)); // 刚好 30s, 未超时
        assert!(session.is_heartbeat_expired(31001)); // 超时
    }

    #[test]
    fn test_mqtt_session_stats() {
        let mut session = MqttSession::new("device1", MqttQos::AtLeastOnce);
        session.record_received();
        session.record_received();
        session.record_sent();
        let (rx, tx) = session.stats();
        assert_eq!(rx, 2);
        assert_eq!(tx, 1);
    }

    #[test]
    fn test_mqtt_session_disconnect() {
        let mut session = MqttSession::new("device1", MqttQos::AtLeastOnce);
        session.add_subscription("test/topic".to_string());
        session.set_state(MqttSessionState::Connected);
        session.disconnect();
        assert_eq!(session.state(), MqttSessionState::Disconnected);
        assert_eq!(session.subscriptions().len(), 0);
    }

    #[test]
    fn test_mqtt_session_qos_integration() {
        let mut session = MqttSession::new("device1", MqttQos::AtLeastOnce);
        let id = session.publish_qos().register(1000);
        assert!(id > 0);
        assert_eq!(session.publish_qos().pending_count(), 1);
        assert!(session.publish_qos().acknowledge(id));
    }
}
