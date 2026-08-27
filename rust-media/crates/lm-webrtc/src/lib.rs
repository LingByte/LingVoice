//! lm-webrtc — WebRTC 传输层
//!
//! 基于 webrtc-rs 实现 ICE/STUN/TURN、DTLS 握手、PeerConnection 管理、DataChannel 支持。
//!
//! 模块组织：
//! - [`ice`]: ICE 配置与连接状态
//! - [`dtls`]: DTLS 配置、状态与 SRTP 密钥材料
//! - [`peer`]: PeerConnection 封装（基于 webrtc-rs RTCPeerConnection）
//! - [`track`]: RTP 轨道收发
//! - [`datachannel`]: DataChannel 封装

pub mod datachannel;
pub mod dtls;
pub mod ice;
pub mod peer;
pub mod track;

use serde::{Deserialize, Serialize};

// ============================================================================
// 错误类型
// ============================================================================

/// lm-webrtc 错误
#[derive(Debug, thiserror::Error)]
pub enum WebrtcError {
    #[error("ICE 错误: {0}")]
    Ice(String),
    #[error("DTLS 错误: {0}")]
    Dtls(String),
    #[error("SDP 错误: {0}")]
    Sdp(String),
    #[error("PeerConnection 错误: {0}")]
    PeerConnection(String),
    #[error("webrtc-rs 内部错误: {0}")]
    Internal(#[from] webrtc::Error),
}

// ============================================================================
// SDP 解析辅助
// ============================================================================

/// SDP 类型（对应 RTCSdpType）
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum SdpType {
    Offer,
    Pranswer,
    Answer,
    Rollback,
}

impl SdpType {
    /// 从字符串解析 SDP 类型
    pub fn from_str_lossy(s: &str) -> Option<Self> {
        match s.to_ascii_lowercase().as_str() {
            "offer" => Some(Self::Offer),
            "pranswer" => Some(Self::Pranswer),
            "answer" => Some(Self::Answer),
            "rollback" => Some(Self::Rollback),
            _ => None,
        }
    }

    /// 转为字符串
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Offer => "offer",
            Self::Pranswer => "pranswer",
            Self::Answer => "answer",
            Self::Rollback => "rollback",
        }
    }
}

/// 简单的 SDP 解析结果
#[derive(Debug, Clone)]
pub struct SdpInfo {
    pub sdp_type: Option<SdpType>,
    pub has_audio: bool,
    pub has_video: bool,
    pub has_datachannel: bool,
}

/// 解析 SDP 字符串，提取基本信息（不依赖网络）
pub fn parse_sdp(sdp: &str) -> Result<SdpInfo, WebrtcError> {
    if sdp.trim().is_empty() {
        return Err(WebrtcError::Sdp("SDP 为空".into()));
    }

    let lower = sdp.to_ascii_lowercase();
    let has_audio = lower.contains("m=audio") || lower.contains("audio/");
    let has_video = lower.contains("m=video") || lower.contains("video/");
    let has_datachannel = lower.contains("application/sctp")
        || lower.contains("webrtc-datachannel")
        || lower.contains("m=application");

    Ok(SdpInfo {
        sdp_type: None,
        has_audio,
        has_video,
        has_datachannel,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_sdp_parse_basic() {
        let sdp = "v=0\r\n\
o=- 459625 2 IN IP4 127.0.0.1\r\n\
s=-\r\n\
t=0 0\r\n\
m=audio 9 UDP/TLS/RTP/SAVPF 111\r\n\
a=rtpmap:111 opus/48000/2\r\n\
m=video 9 UDP/TLS/RTP/SAVPF 96\r\n\
a=rtpmap:96 VP8/90000\r\n";
        let info = parse_sdp(sdp).expect("解析 SDP");
        assert!(info.has_audio);
        assert!(info.has_video);
        assert!(!info.has_datachannel);
    }

    #[test]
    fn test_sdp_parse_datachannel() {
        let sdp = "v=0\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\n";
        let info = parse_sdp(sdp).expect("解析 SDP");
        assert!(info.has_datachannel);
    }

    #[test]
    fn test_sdp_parse_empty() {
        assert!(parse_sdp("").is_err());
        assert!(parse_sdp("   ").is_err());
    }

    #[test]
    fn test_sdp_type_from_str() {
        assert_eq!(SdpType::from_str_lossy("offer"), Some(SdpType::Offer));
        assert_eq!(SdpType::from_str_lossy("Answer"), Some(SdpType::Answer));
        assert_eq!(SdpType::from_str_lossy("unknown"), None);
        assert_eq!(SdpType::Offer.as_str(), "offer");
    }
}
