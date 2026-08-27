//! 视频转码会话管理器
//!
//! 管理持久化的转码会话，避免每帧重建解码器/编码器。
//! Go 控制面通过 gRPC 调用 CreateTranscodeSession → TranscodeVideo → DestroyTranscodeSession。

use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use lm_core::CodecType;
use video_codec::transcoder::{TranscodePath, TranscodeSession};
use video_codec::VideoCodecError;

/// 转码会话管理器
pub struct TranscodeManager {
    sessions: Mutex<HashMap<String, TranscodeSession>>,
}

impl TranscodeManager {
    pub fn new() -> Self {
        Self {
            sessions: Mutex::new(HashMap::new()),
        }
    }

    /// 创建转码会话
    pub fn create_session(
        &self,
        transcode_id: &str,
        from: CodecType,
        to: CodecType,
        width: u32,
        height: u32,
        prefer_hardware: bool,
        bitrate: u32,
        framerate: u32,
    ) -> Result<bool, VideoCodecError> {
        let mut session = TranscodeSession::new(
            transcode_id.to_string(),
            from,
            to,
            width,
            height,
            prefer_hardware,
        )?;
        if bitrate > 0 {
            session.set_bitrate(bitrate);
        }
        if framerate > 0 {
            session.set_framerate(framerate);
        }
        let hardware_used = session.hardware_used();

        let mut sessions = self.sessions.lock().unwrap();
        sessions.insert(transcode_id.to_string(), session);
        Ok(hardware_used)
    }

    /// 转码一帧
    pub fn transcode(
        &self,
        transcode_id: &str,
        encoded_frame: &[u8],
        timestamp: u64,
    ) -> Result<TranscodeResult, TranscodeError> {
        let mut sessions = self.sessions.lock().unwrap();
        let session = sessions
            .get_mut(transcode_id)
            .ok_or(TranscodeError::SessionNotFound)?;
        let encoded = session
            .transcode(encoded_frame, timestamp)
            .map_err(TranscodeError::Codec)?;
        Ok(TranscodeResult {
            encoded_frame: encoded.data.to_vec(),
            timestamp: encoded.timestamp,
            keyframe: encoded.keyframe,
            width: encoded.width,
            height: encoded.height,
            frames_processed: session.frame_count(),
            hardware_used: session.hardware_used(),
        })
    }

    /// 销毁转码会话
    pub fn destroy_session(&self, transcode_id: &str) -> Option<u64> {
        let mut sessions = self.sessions.lock().unwrap();
        sessions.remove(transcode_id).map(|s| s.frame_count())
    }

    /// 请求关键帧
    pub fn request_keyframe(&self, transcode_id: &str, timestamp: u64) {
        let mut sessions = self.sessions.lock().unwrap();
        if let Some(session) = sessions.get_mut(transcode_id) {
            session.request_keyframe(timestamp);
        }
    }

    /// 设置码率
    pub fn set_bitrate(&self, transcode_id: &str, bps: u32) {
        let mut sessions = self.sessions.lock().unwrap();
        if let Some(session) = sessions.get_mut(transcode_id) {
            session.set_bitrate(bps);
        }
    }

    /// 设置帧率
    pub fn set_framerate(&self, transcode_id: &str, fps: u32) {
        let mut sessions = self.sessions.lock().unwrap();
        if let Some(session) = sessions.get_mut(transcode_id) {
            session.set_framerate(fps);
        }
    }

    /// 返回活跃转码会话数
    pub fn session_count(&self) -> usize {
        self.sessions.lock().unwrap().len()
    }

    /// 返回转码路径
    pub fn path(&self, transcode_id: &str) -> Option<TranscodePath> {
        let sessions = self.sessions.lock().unwrap();
        sessions.get(transcode_id).map(|s| s.path())
    }
}

/// 转码结果
pub struct TranscodeResult {
    pub encoded_frame: Vec<u8>,
    pub timestamp: u64,
    pub keyframe: bool,
    pub width: u32,
    pub height: u32,
    pub frames_processed: u64,
    pub hardware_used: bool,
}

/// 转码错误
#[derive(Debug)]
pub enum TranscodeError {
    SessionNotFound,
    Codec(VideoCodecError),
}

impl std::fmt::Display for TranscodeError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::SessionNotFound => write!(f, "transcode session not found"),
            Self::Codec(e) => write!(f, "codec error: {}", e),
        }
    }
}

impl std::error::Error for TranscodeError {}

/// 将字符串转换为 CodecType
pub fn parse_codec(s: &str) -> Option<CodecType> {
    match s.to_lowercase().as_str() {
        "h264" => Some(CodecType::H264),
        "h265" | "hevc" => Some(CodecType::H265),
        "vp8" => Some(CodecType::Vp8),
        "vp9" => Some(CodecType::Vp9),
        "av1" => Some(CodecType::Av1),
        "opus" => Some(CodecType::Opus),
        "pcmu" => Some(CodecType::PcmU),
        "pcma" => Some(CodecType::PcmA),
        "g722" => Some(CodecType::G722),
        _ => None,
    }
}

/// 默认实现（用于 Default trait）
impl Default for TranscodeManager {
    fn default() -> Self {
        Self::new()
    }
}

pub type SharedTranscodeManager = Arc<TranscodeManager>;
