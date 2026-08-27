//! DataChannel 模块 — 基于 webrtc-rs RTCDataChannel 的封装
//!
//! 提供有序/无序、可靠/不可靠的 DataChannel 配置与文本/二进制收发。

use std::sync::Arc;

use parking_lot::RwLock;
use webrtc::data_channel::data_channel_init::RTCDataChannelInit;
use webrtc::data_channel::data_channel_state::RTCDataChannelState;
use webrtc::data_channel::RTCDataChannel as InnerDC;

use crate::WebrtcError;

// ============================================================================
// DataChannel 状态
// ============================================================================

/// DataChannel 状态
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum DataChannelState {
    Connecting,
    Open,
    Closing,
    Closed,
}

impl DataChannelState {
    /// 从 webrtc-rs 的 [`RTCDataChannelState`] 转换
    pub fn from_webrtc(state: RTCDataChannelState) -> Self {
        match state {
            RTCDataChannelState::Unspecified => Self::Connecting,
            RTCDataChannelState::Connecting => Self::Connecting,
            RTCDataChannelState::Open => Self::Open,
            RTCDataChannelState::Closing => Self::Closing,
            RTCDataChannelState::Closed => Self::Closed,
        }
    }

    /// 是否已打开
    pub fn is_open(self) -> bool {
        matches!(self, Self::Open)
    }

    /// 是否为终态
    pub fn is_terminal(self) -> bool {
        matches!(self, Self::Closed)
    }
}

// ============================================================================
// DataChannel 配置
// ============================================================================

/// DataChannel 配置
#[derive(Debug, Clone)]
pub struct DataChannelConfig {
    pub label: String,
    pub ordered: bool,
    pub max_retransmits: Option<u16>,
    pub max_packet_life_time: Option<u16>,
    pub protocol: String,
    pub negotiated: bool,
    pub id: Option<u16>,
}

impl Default for DataChannelConfig {
    fn default() -> Self {
        Self {
            label: String::new(),
            ordered: true,
            max_retransmits: None,
            max_packet_life_time: None,
            protocol: String::new(),
            negotiated: false,
            id: None,
        }
    }
}

impl DataChannelConfig {
    /// 转换为 webrtc-rs 的 [`RTCDataChannelInit`]
    pub fn to_init(&self) -> RTCDataChannelInit {
        RTCDataChannelInit {
            ordered: Some(self.ordered),
            max_packet_life_time: self.max_packet_life_time,
            max_retransmits: self.max_retransmits,
            protocol: if self.protocol.is_empty() {
                None
            } else {
                Some(self.protocol.clone())
            },
            negotiated: if self.negotiated { self.id } else { None },
        }
    }
}

// ============================================================================
// DataChannel 封装
// ============================================================================

/// DataChannel 封装
pub struct DataChannel {
    pub id: u16,
    pub label: String,
    config: DataChannelConfig,
    inner: Arc<InnerDC>,
    state: Arc<RwLock<DataChannelState>>,
}

impl DataChannel {
    /// 从配置创建（用于测试/占位，无底层 RTCDataChannel）
    ///
    /// 实际使用应通过 [`PeerConnection::create_data_channel`]。
    pub fn new(config: DataChannelConfig) -> Self {
        Self {
            id: config.id.unwrap_or(0),
            label: config.label.clone(),
            config,
            inner: Arc::new(webrtc::data_channel::RTCDataChannel::default()),
            state: Arc::new(parking_lot::RwLock::new(DataChannelState::Connecting)),
        }
    }

    /// 从 webrtc-rs 的 [`RTCDataChannel`] 构造封装
    pub(crate) fn from_rtc(
        rtc: Arc<InnerDC>,
        config: DataChannelConfig,
    ) -> Result<Self, WebrtcError> {
        let id = rtc.id();
        let label = rtc.label().to_string();
        let state = Arc::new(RwLock::new(DataChannelState::from_webrtc(
            rtc.ready_state(),
        )));

        let state_cb = state.clone();
        rtc.on_open(Box::new(move || {
            *state_cb.write() = DataChannelState::Open;
            Box::pin(async {})
        }));

        Ok(Self {
            id,
            label,
            config,
            inner: rtc,
            state,
        })
    }

    /// 发送文本
    pub async fn send_text(&self, text: &str) -> Result<(), WebrtcError> {
        self.inner
            .send_text(text.to_string())
            .await
            .map_err(WebrtcError::Internal)?;
        Ok(())
    }

    /// 发送二进制
    pub async fn send_binary(&self, data: &[u8]) -> Result<(), WebrtcError> {
        use bytes::Bytes;
        let bytes = Bytes::copy_from_slice(data);
        self.inner
            .send(&bytes)
            .await
            .map_err(WebrtcError::Internal)?;
        Ok(())
    }

    /// 关闭 DataChannel
    pub async fn close(&self) -> Result<(), WebrtcError> {
        self.inner.close().await?;
        *self.state.write() = DataChannelState::Closed;
        Ok(())
    }

    /// 当前状态
    pub fn state(&self) -> DataChannelState {
        // 优先读取底层实时状态
        let live = DataChannelState::from_webrtc(self.inner.ready_state());
        *self.state.write() = live;
        live
    }

    /// 配置
    pub fn config(&self) -> &DataChannelConfig {
        &self.config
    }

    /// 标签
    pub fn label(&self) -> &str {
        &self.label
    }

    /// ID
    pub fn id(&self) -> u16 {
        self.id
    }
}

impl std::fmt::Debug for DataChannel {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("DataChannel")
            .field("id", &self.id)
            .field("label", &self.label)
            .field("state", &self.state())
            .field("config", &self.config)
            .finish()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_data_channel_config_default() {
        let cfg = DataChannelConfig::default();
        assert!(cfg.ordered);
        assert!(cfg.max_retransmits.is_none());
        assert!(cfg.max_packet_life_time.is_none());
        assert!(!cfg.negotiated);
        assert!(cfg.id.is_none());
    }

    #[test]
    fn test_data_channel_config_custom() {
        let cfg = DataChannelConfig {
            label: "chat".into(),
            ordered: false,
            max_retransmits: Some(3),
            max_packet_life_time: None,
            protocol: "json".into(),
            negotiated: true,
            id: Some(42),
        };
        let init = cfg.to_init();
        assert_eq!(init.ordered, Some(false));
        assert_eq!(init.max_retransmits, Some(3));
        assert_eq!(init.protocol, Some("json".to_string()));
        assert_eq!(init.negotiated, Some(42));
    }

    #[test]
    fn test_data_channel_config_negotiated_without_id() {
        let cfg = DataChannelConfig {
            label: "data".into(),
            ordered: true,
            max_retransmits: None,
            max_packet_life_time: Some(1000),
            protocol: String::new(),
            negotiated: true,
            id: None,
        };
        let init = cfg.to_init();
        // negotiated=true 但无 id -> None
        assert_eq!(init.negotiated, None);
        assert_eq!(init.max_packet_life_time, Some(1000));
        assert_eq!(init.protocol, None);
    }

    #[test]
    fn test_data_channel_state_transitions() {
        use DataChannelState as S;
        let states = [S::Connecting, S::Open, S::Closing, S::Closed];
        for w in states.windows(2) {
            assert_ne!(w[0], w[1]);
        }
        assert!(S::Open.is_open());
        assert!(!S::Connecting.is_open());
        assert!(S::Closed.is_terminal());
        assert!(!S::Open.is_terminal());
    }

    #[test]
    fn test_data_channel_state_from_webrtc() {
        assert_eq!(
            DataChannelState::from_webrtc(RTCDataChannelState::Connecting),
            DataChannelState::Connecting
        );
        assert_eq!(
            DataChannelState::from_webrtc(RTCDataChannelState::Open),
            DataChannelState::Open
        );
        assert_eq!(
            DataChannelState::from_webrtc(RTCDataChannelState::Closed),
            DataChannelState::Closed
        );
    }
}
