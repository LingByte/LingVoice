//! ICE 模块 — ICE/STUN/TURN 配置与连接状态
//!
//! 提供 ICE 配置抽象，并桥接到 webrtc-rs 的 [`RTCConfiguration`]。

use serde::{Deserialize, Serialize};
use webrtc::ice_transport::ice_candidate::RTCIceCandidateInit;
use webrtc::ice_transport::ice_connection_state::RTCIceConnectionState;
use webrtc::ice_transport::ice_server::RTCIceServer;
use webrtc::peer_connection::configuration::RTCConfiguration;
use webrtc::peer_connection::policy::ice_transport_policy::RTCIceTransportPolicy;

// ============================================================================
// ICE 配置
// ============================================================================

/// ICE 配置（STUN/TURN 服务器、ICE Lite 等）
#[derive(Debug, Clone)]
pub struct IceConfig {
    /// STUN 服务器 URL 列表
    pub stun_servers: Vec<String>,
    /// TURN 服务器列表
    pub turn_servers: Vec<TurnServer>,
    /// 是否启用 ICE Lite（适用于服务器端、无 NAT 场景）
    pub ice_lite: bool,
}

impl Default for IceConfig {
    fn default() -> Self {
        Self {
            stun_servers: vec!["stun:stun.l.google.com:19302".to_string()],
            turn_servers: Vec::new(),
            ice_lite: false,
        }
    }
}

/// TURN 服务器
#[derive(Debug, Clone)]
pub struct TurnServer {
    pub url: String,
    pub username: String,
    pub credential: String,
}

/// ICE 候选信息（用于信令交换）
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct IceCandidateInfo {
    pub candidate: String,
    pub sdp_mline_index: u16,
    pub sdp_mid: String,
}

impl IceCandidateInfo {
    /// 转换为 webrtc-rs 的 [`RTCIceCandidateInit`]
    pub fn to_init(&self) -> RTCIceCandidateInit {
        RTCIceCandidateInit {
            candidate: self.candidate.clone(),
            sdp_mid: Some(self.sdp_mid.clone()),
            sdp_mline_index: Some(self.sdp_mline_index),
            username_fragment: None,
        }
    }
}

impl From<&IceCandidateInfo> for RTCIceCandidateInit {
    fn from(info: &IceCandidateInfo) -> Self {
        info.to_init()
    }
}

// ============================================================================
// ICE 连接状态
// ============================================================================

/// ICE 连接状态
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum IceConnectionState {
    New,
    Gathering,
    Waiting,
    Checking,
    Connected,
    Completed,
    Failed,
    Disconnected,
    Closed,
}

impl IceConnectionState {
    /// 从 webrtc-rs 的 [`RTCIceConnectionState`] 转换
    pub fn from_webrtc(state: RTCIceConnectionState) -> Self {
        match state {
            RTCIceConnectionState::Unspecified => Self::New,
            RTCIceConnectionState::New => Self::New,
            RTCIceConnectionState::Checking => Self::Checking,
            RTCIceConnectionState::Connected => Self::Connected,
            RTCIceConnectionState::Completed => Self::Completed,
            RTCIceConnectionState::Disconnected => Self::Disconnected,
            RTCIceConnectionState::Failed => Self::Failed,
            RTCIceConnectionState::Closed => Self::Closed,
        }
    }

    /// 是否为活跃连接状态
    pub fn is_connected(self) -> bool {
        matches!(self, Self::Connected | Self::Completed)
    }

    /// 是否为终态
    pub fn is_terminal(self) -> bool {
        matches!(self, Self::Failed | Self::Closed)
    }
}

// ============================================================================
// IceConfig -> RTCConfiguration
// ============================================================================

/// 将 [`IceConfig`] 转换为 webrtc-rs 的 [`RTCConfiguration`]。
pub fn ice_config_to_rtc(config: &IceConfig) -> RTCConfiguration {
    let mut ice_servers: Vec<RTCIceServer> = config
        .stun_servers
        .iter()
        .map(|url| RTCIceServer {
            urls: vec![url.clone()],
            ..Default::default()
        })
        .collect();

    for turn in &config.turn_servers {
        ice_servers.push(RTCIceServer {
            urls: vec![turn.url.clone()],
            username: turn.username.clone(),
            credential: turn.credential.clone(),
            ..Default::default()
        });
    }

    RTCConfiguration {
        ice_servers,
        ice_transport_policy: RTCIceTransportPolicy::All,
        ..Default::default()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_ice_config_default() {
        let cfg = IceConfig::default();
        assert!(!cfg.stun_servers.is_empty());
        assert!(cfg.turn_servers.is_empty());
        assert!(!cfg.ice_lite);
    }

    #[test]
    fn test_ice_config_to_rtc() {
        let cfg = IceConfig {
            stun_servers: vec!["stun:stun.example.com:3478".into()],
            turn_servers: vec![TurnServer {
                url: "turn:turn.example.com:3478".into(),
                username: "user".into(),
                credential: "pass".into(),
            }],
            ice_lite: true,
        };
        let rtc = ice_config_to_rtc(&cfg);
        assert_eq!(rtc.ice_servers.len(), 2);
        assert_eq!(rtc.ice_servers[0].urls[0], "stun:stun.example.com:3478");
        assert_eq!(rtc.ice_servers[1].urls[0], "turn:turn.example.com:3478");
        assert_eq!(rtc.ice_servers[1].username, "user");
        assert_eq!(rtc.ice_servers[1].credential, "pass");
    }

    #[test]
    fn test_ice_connection_state_transitions() {
        use IceConnectionState as S;
        // 基本状态转换链
        let states = [S::New, S::Checking, S::Connected, S::Completed, S::Closed];
        for w in states.windows(2) {
            // 仅验证状态可比较
            assert_ne!(w[0], w[1]);
        }
        assert!(S::Connected.is_connected());
        assert!(S::Completed.is_connected());
        assert!(!S::Checking.is_connected());
        assert!(S::Failed.is_terminal());
        assert!(S::Closed.is_terminal());
        assert!(!S::Connected.is_terminal());
    }

    #[test]
    fn test_ice_candidate_info_to_init() {
        let info = IceCandidateInfo {
            candidate: "candidate:1 1 udp 2130706431 192.168.1.1 50000 typ host".into(),
            sdp_mline_index: 0,
            sdp_mid: "audio".into(),
        };
        let init = info.to_init();
        assert_eq!(init.candidate, info.candidate);
        assert_eq!(init.sdp_mid, Some("audio".to_string()));
        assert_eq!(init.sdp_mline_index, Some(0));
    }

    #[test]
    fn test_ice_state_from_webrtc() {
        assert_eq!(
            IceConnectionState::from_webrtc(RTCIceConnectionState::New),
            IceConnectionState::New
        );
        assert_eq!(
            IceConnectionState::from_webrtc(RTCIceConnectionState::Checking),
            IceConnectionState::Checking
        );
        assert_eq!(
            IceConnectionState::from_webrtc(RTCIceConnectionState::Connected),
            IceConnectionState::Connected
        );
        assert_eq!(
            IceConnectionState::from_webrtc(RTCIceConnectionState::Closed),
            IceConnectionState::Closed
        );
    }
}
