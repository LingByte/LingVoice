//! DTLS 模块 — DTLS 配置、状态与 SRTP 密钥材料
//!
//! webrtc-rs 在 RTCPeerConnection 内部自动完成 DTLS 握手与 SRTP 密钥协商。
//! 本模块提供 DTLS 配置抽象与状态跟踪，便于上层观测与自定义证书。

use parking_lot::RwLock;

// ============================================================================
// DTLS 配置
// ============================================================================

/// DTLS 配置
#[derive(Debug, Clone)]
pub struct DtlsConfig {
    /// PEM 格式证书（可选，未提供时自动生成）
    pub certificate_pem: Option<String>,
    /// PEM 格式私钥（可选）
    pub private_key_pem: Option<String>,
    /// 是否自动生成自签名证书
    pub auto_generate_cert: bool,
}

impl Default for DtlsConfig {
    fn default() -> Self {
        Self {
            certificate_pem: None,
            private_key_pem: None,
            auto_generate_cert: true,
        }
    }
}

// ============================================================================
// DTLS 状态
// ============================================================================

/// DTLS 传输状态
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum DtlsState {
    New,
    Connecting,
    Connected,
    Failed,
    Closed,
}

impl DtlsState {
    /// 是否已建立连接
    pub fn is_connected(self) -> bool {
        matches!(self, Self::Connected)
    }

    /// 是否为终态
    pub fn is_terminal(self) -> bool {
        matches!(self, Self::Failed | Self::Closed)
    }
}

// ============================================================================
// DTLS 传输（状态跟踪封装）
// ============================================================================

/// DTLS 传输封装
///
/// 注意：webrtc-rs 的 DTLS 传输由 RTCPeerConnection 内部管理。
/// 此结构用于跟踪 DTLS 状态与配置，便于上层观测。
pub struct DtlsTransport {
    state: RwLock<DtlsState>,
    config: DtlsConfig,
}

impl DtlsTransport {
    /// 创建 DTLS 传输
    pub fn new(config: DtlsConfig) -> Self {
        Self {
            state: RwLock::new(DtlsState::New),
            config,
        }
    }

    /// 获取当前状态
    pub fn state(&self) -> DtlsState {
        *self.state.read()
    }

    /// 获取配置
    pub fn config(&self) -> &DtlsConfig {
        &self.config
    }

    /// 设置状态（由 PeerConnection 状态回调驱动）
    pub fn set_state(&self, state: DtlsState) {
        *self.state.write() = state;
    }

    /// 是否已连接
    pub fn is_connected(&self) -> bool {
        self.state().is_connected()
    }
}

impl std::fmt::Debug for DtlsTransport {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("DtlsTransport")
            .field("state", &self.state())
            .field("config", &self.config)
            .finish()
    }
}

// ============================================================================
// SRTP 密钥材料
// ============================================================================

/// SRTP 密钥材料（DTLS 握手协商出的密钥）
#[derive(Debug, Clone)]
pub struct SrtpKeyingMaterial {
    /// 本地主密钥
    pub local_master_key: Vec<u8>,
    /// 本地主盐
    pub local_master_salt: Vec<u8>,
    /// 远端主密钥
    pub remote_master_key: Vec<u8>,
    /// 远端主盐
    pub remote_master_salt: Vec<u8>,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_dtls_config_default() {
        let cfg = DtlsConfig::default();
        assert!(cfg.auto_generate_cert);
        assert!(cfg.certificate_pem.is_none());
        assert!(cfg.private_key_pem.is_none());
    }

    #[test]
    fn test_dtls_config_custom() {
        let cfg = DtlsConfig {
            certificate_pem: Some("-----BEGIN CERTIFICATE-----".into()),
            private_key_pem: Some("-----BEGIN PRIVATE KEY-----".into()),
            auto_generate_cert: false,
        };
        assert!(!cfg.auto_generate_cert);
        assert!(cfg.certificate_pem.is_some());
        assert!(cfg.private_key_pem.is_some());
    }

    #[test]
    fn test_dtls_state_transitions() {
        use DtlsState as S;
        let states = [S::New, S::Connecting, S::Connected, S::Closed];
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
    fn test_dtls_transport_state_tracking() {
        let transport = DtlsTransport::new(DtlsConfig::default());
        assert_eq!(transport.state(), DtlsState::New);
        assert!(!transport.is_connected());

        transport.set_state(DtlsState::Connecting);
        assert_eq!(transport.state(), DtlsState::Connecting);

        transport.set_state(DtlsState::Connected);
        assert!(transport.is_connected());
        assert_eq!(transport.state(), DtlsState::Connected);
    }

    #[test]
    fn test_srtp_keying_material() {
        let km = SrtpKeyingMaterial {
            local_master_key: vec![1, 2, 3, 4],
            local_master_salt: vec![5, 6, 7, 8],
            remote_master_key: vec![9, 10, 11, 12],
            remote_master_salt: vec![13, 14, 15, 16],
        };
        assert_eq!(km.local_master_key.len(), 4);
        assert_eq!(km.remote_master_salt.len(), 4);
    }
}
