//! PeerConnection 模块 — 基于 webrtc-rs RTCPeerConnection 的封装
//!
//! 提供 ICE/DTLS 一体化的 PeerConnection 管理，支持 SDP offer/answer 协商、
//! ICE 候选交换、DataChannel 创建与状态观测。

use std::sync::Arc;

use parking_lot::RwLock;
use serde::{Deserialize, Serialize};
use webrtc::api::interceptor_registry::register_default_interceptors;
use webrtc::api::media_engine::MediaEngine;
use webrtc::api::setting_engine::SettingEngine;
use webrtc::api::APIBuilder;
use webrtc::interceptor::registry::Registry;
use webrtc::peer_connection::peer_connection_state::RTCPeerConnectionState;
use webrtc::peer_connection::sdp::session_description::RTCSessionDescription;
use webrtc::peer_connection::RTCPeerConnection as InnerPC;

use crate::datachannel::{DataChannel, DataChannelConfig};
use crate::dtls::{DtlsConfig, DtlsState, DtlsTransport};
use crate::ice::{ice_config_to_rtc, IceCandidateInfo, IceConfig, IceConnectionState};
use crate::WebrtcError;

// ============================================================================
// PeerConnection 状态
// ============================================================================

/// PeerConnection 状态
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum PeerConnectionState {
    New,
    Connecting,
    Connected,
    Reconnecting,
    Disconnected,
    Failed,
    Closed,
}

impl PeerConnectionState {
    /// 从 webrtc-rs 的 [`RTCPeerConnectionState`] 转换
    pub fn from_webrtc(state: RTCPeerConnectionState) -> Self {
        match state {
            RTCPeerConnectionState::Unspecified => Self::New,
            RTCPeerConnectionState::New => Self::New,
            RTCPeerConnectionState::Connecting => Self::Connecting,
            RTCPeerConnectionState::Connected => Self::Connected,
            RTCPeerConnectionState::Disconnected => Self::Disconnected,
            RTCPeerConnectionState::Failed => Self::Failed,
            RTCPeerConnectionState::Closed => Self::Closed,
        }
    }

    /// 是否为活跃连接
    pub fn is_connected(self) -> bool {
        matches!(self, Self::Connected)
    }

    /// 是否为终态
    pub fn is_terminal(self) -> bool {
        matches!(self, Self::Failed | Self::Closed)
    }
}

// ============================================================================
// PeerConnection 配置
// ============================================================================

/// PeerConnection 配置
#[derive(Debug, Clone)]
pub struct PeerConnectionConfig {
    pub ice_config: IceConfig,
    pub dtls_config: DtlsConfig,
    pub enable_datachannel: bool,
    pub max_bitrate: u64,
}

impl Default for PeerConnectionConfig {
    fn default() -> Self {
        Self {
            ice_config: IceConfig::default(),
            dtls_config: DtlsConfig::default(),
            enable_datachannel: true,
            max_bitrate: 2_000_000,
        }
    }
}

// ============================================================================
// PeerConnection 统计
// ============================================================================

/// PeerConnection 统计信息
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PeerConnectionStats {
    pub bytes_received: u64,
    pub bytes_sent: u64,
    pub rtt_ms: u32,
    pub jitter_ms: u32,
    pub packets_lost: u64,
    pub ice_state: String,
    pub dtls_state: String,
}

// ============================================================================
// PeerConnection 封装
// ============================================================================

/// PeerConnection 封装
pub struct PeerConnection {
    inner: Arc<InnerPC>,
    config: PeerConnectionConfig,
    state: Arc<RwLock<PeerConnectionState>>,
    dtls: Arc<DtlsTransport>,
}

impl PeerConnection {
    /// 创建 PeerConnection
    pub async fn create(config: PeerConnectionConfig) -> Result<Self, WebrtcError> {
        // MediaEngine：注册默认编解码器
        let mut m = MediaEngine::default();
        m.register_default_codecs()
            .map_err(|e| WebrtcError::PeerConnection(format!("注册编解码器失败: {e}")))?;

        // SettingEngine：ICE Lite 等
        let mut setting = SettingEngine::default();
        if config.ice_config.ice_lite {
            setting.set_lite(true);
        }

        // InterceptorRegistry：默认（绑定到 media engine）
        let ir = Registry::default();
        let ir = register_default_interceptors(ir, &mut m)
            .map_err(|e| WebrtcError::PeerConnection(format!("注册拦截器失败: {e}")))?;

        let api = APIBuilder::new()
            .with_media_engine(m)
            .with_interceptor_registry(ir)
            .with_setting_engine(setting)
            .build();

        let rtc_config = ice_config_to_rtc(&config.ice_config);
        let inner = api.new_peer_connection(rtc_config).await?;

        let state = Arc::new(RwLock::new(PeerConnectionState::New));
        let dtls = Arc::new(DtlsTransport::new(config.dtls_config.clone()));

        // 注册状态回调，同步本地状态
        let state_cb = state.clone();
        inner.on_peer_connection_state_change(Box::new(move |s| {
            let mapped = PeerConnectionState::from_webrtc(s);
            *state_cb.write() = mapped;
            tracing::debug!(?mapped, "peer connection state changed");
            Box::pin(async {})
        }));

        // ICE 连接状态回调，联动 DTLS 状态
        let dtls_cb = dtls.clone();
        inner.on_ice_connection_state_change(Box::new(move |s| {
            let ice = IceConnectionState::from_webrtc(s);
            // 简化：ICE 连接视为 DTLS 连接
            let dtls_state = match ice {
                IceConnectionState::Connected | IceConnectionState::Completed => {
                    DtlsState::Connected
                }
                IceConnectionState::Checking => DtlsState::Connecting,
                IceConnectionState::Failed => DtlsState::Failed,
                IceConnectionState::Closed => DtlsState::Closed,
                _ => DtlsState::New,
            };
            dtls_cb.set_state(dtls_state);
            Box::pin(async {})
        }));

        Ok(Self {
            inner: Arc::new(inner),
            config,
            state,
            dtls,
        })
    }

    /// 设置远端 SDP 描述（offer 或 answer）
    pub async fn set_remote_description(&self, sdp: &str) -> Result<(), WebrtcError> {
        let desc = RTCSessionDescription::offer(sdp.to_string())
            .map_err(|e| WebrtcError::Sdp(format!("解析 offer 失败: {e}")))?;
        self.inner.set_remote_description(desc).await?;
        Ok(())
    }

    /// 设置远端 answer 描述
    pub async fn set_remote_answer(&self, sdp: &str) -> Result<(), WebrtcError> {
        let desc = RTCSessionDescription::answer(sdp.to_string())
            .map_err(|e| WebrtcError::Sdp(format!("解析 answer 失败: {e}")))?;
        self.inner.set_remote_description(desc).await?;
        Ok(())
    }

    /// 创建 answer SDP（需先 set_remote_description(offer)）
    pub async fn create_answer(&self) -> Result<String, WebrtcError> {
        let answer = self.inner.create_answer(None).await?;
        Ok(answer.sdp)
    }

    /// 创建 offer SDP
    pub async fn create_offer(&self) -> Result<String, WebrtcError> {
        let offer = self.inner.create_offer(None).await?;
        Ok(offer.sdp)
    }

    /// 设置本地 SDP 描述
    pub async fn set_local_description(&self, sdp: &str) -> Result<(), WebrtcError> {
        let desc = RTCSessionDescription::answer(sdp.to_string())
            .map_err(|e| WebrtcError::Sdp(format!("解析本地 SDP 失败: {e}")))?;
        self.inner.set_local_description(desc).await?;
        Ok(())
    }

    /// 添加 ICE 候选
    pub async fn add_ice_candidate(&self, candidate: &IceCandidateInfo) -> Result<(), WebrtcError> {
        self.inner.add_ice_candidate(candidate.to_init()).await?;
        Ok(())
    }

    /// 创建 DataChannel
    pub async fn create_data_channel(
        &self,
        dc_config: DataChannelConfig,
    ) -> Result<DataChannel, WebrtcError> {
        if !self.config.enable_datachannel {
            return Err(WebrtcError::PeerConnection("DataChannel 未启用".into()));
        }
        let init = dc_config.to_init();
        let label = dc_config.label.clone();
        let rtc_dc = self.inner.create_data_channel(&label, Some(init)).await?;
        DataChannel::from_rtc(rtc_dc, dc_config)
    }

    /// 关闭 PeerConnection
    pub async fn close(&self) -> Result<(), WebrtcError> {
        self.inner.close().await?;
        *self.state.write() = PeerConnectionState::Closed;
        self.dtls.set_state(DtlsState::Closed);
        Ok(())
    }

    /// 当前状态
    pub fn state(&self) -> PeerConnectionState {
        *self.state.read()
    }

    /// ICE 连接状态
    pub fn ice_state(&self) -> IceConnectionState {
        IceConnectionState::from_webrtc(self.inner.ice_connection_state())
    }

    /// DTLS 状态
    pub fn dtls_state(&self) -> DtlsState {
        self.dtls.state()
    }

    /// 配置
    pub fn config(&self) -> &PeerConnectionConfig {
        &self.config
    }

    /// 内部 RTCPeerConnection 引用（供高级用法）
    pub fn inner(&self) -> &Arc<InnerPC> {
        &self.inner
    }

    /// 获取统计信息（简化版，基于当前状态）
    pub fn stats(&self) -> PeerConnectionStats {
        PeerConnectionStats {
            bytes_received: 0,
            bytes_sent: 0,
            rtt_ms: 0,
            jitter_ms: 0,
            packets_lost: 0,
            ice_state: format!("{:?}", self.ice_state()),
            dtls_state: format!("{:?}", self.dtls_state()),
        }
    }
}

impl std::fmt::Debug for PeerConnection {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("PeerConnection")
            .field("state", &self.state())
            .field("ice_state", &self.ice_state())
            .field("dtls_state", &self.dtls_state())
            .field("config", &self.config)
            .finish()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_peer_connection_config_default() {
        let cfg = PeerConnectionConfig::default();
        assert!(cfg.enable_datachannel);
        assert!(cfg.max_bitrate > 0);
        assert!(!cfg.ice_config.ice_lite);
        assert!(cfg.dtls_config.auto_generate_cert);
    }

    #[test]
    fn test_peer_connection_state_transitions() {
        use PeerConnectionState as S;
        let states = [
            S::New,
            S::Connecting,
            S::Connected,
            S::Disconnected,
            S::Failed,
            S::Closed,
        ];
        for w in states.windows(2) {
            assert_ne!(w[0], w[1]);
        }
        assert!(S::Connected.is_connected());
        assert!(!S::Connecting.is_connected());
        assert!(S::Failed.is_terminal());
        assert!(S::Closed.is_terminal());
        assert!(!S::Connected.is_terminal());
    }

    #[test]
    fn test_peer_connection_state_from_webrtc() {
        assert_eq!(
            PeerConnectionState::from_webrtc(RTCPeerConnectionState::New),
            PeerConnectionState::New
        );
        assert_eq!(
            PeerConnectionState::from_webrtc(RTCPeerConnectionState::Connected),
            PeerConnectionState::Connected
        );
        assert_eq!(
            PeerConnectionState::from_webrtc(RTCPeerConnectionState::Closed),
            PeerConnectionState::Closed
        );
    }

    #[test]
    fn test_peer_connection_stats() {
        // 仅验证结构可构造
        let stats = PeerConnectionStats {
            bytes_received: 100,
            bytes_sent: 200,
            rtt_ms: 10,
            jitter_ms: 5,
            packets_lost: 1,
            ice_state: "Connected".into(),
            dtls_state: "Connected".into(),
        };
        assert_eq!(stats.bytes_sent, 200);
    }
}
