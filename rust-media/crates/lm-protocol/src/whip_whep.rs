//! WHIP/WHEP Handler — WebRTC HTTP 信令
//!
//! WHIP (WebRTC-HTTP Ingestion Protocol): https://datatracker.ietf.org/doc/draft-ietf-wish-whip/
//! WHEP (WebRTC-HTTP Egress Protocol): https://datatracker.ietf.org/doc/draft-ietf-wish-whep/
//!
//! 协议流程：
//! 1. Client POST SDP offer to WHIP/WHEP endpoint
//! 2. Server creates PeerConnection, sets remote description
//! 3. Server generates SDP answer
//! 4. Server returns SDP answer in response body
//! 5. ICE 完成，媒体开始传输
//! 6. DELETE 请求终止会话
//!
//! 简化实现：只定义接口和配置，实际 WebRTC 处理由 Go/Pion 层完成。

use crate::Protocol;
use serde::{Deserialize, Serialize};

/// WHIP/WHEP 配置
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WhipWhepConfig {
    /// 监听地址
    pub listen_addr: String,
    /// WHIP endpoint 路径
    pub whip_path: String,
    /// WHEP endpoint 路径
    pub whep_path: String,
    /// 是否启用 ICE Lite
    pub ice_lite: bool,
    /// STUN 服务器
    pub stun_server: Option<String>,
    /// TURN 服务器
    pub turn_server: Option<String>,
    /// TURN 用户名
    pub turn_username: Option<String>,
    /// TURN 密码
    pub turn_password: Option<String>,
}

impl Default for WhipWhepConfig {
    fn default() -> Self {
        Self {
            listen_addr: "0.0.0.0:8888".into(),
            whip_path: "/whip".into(),
            whep_path: "/whep".into(),
            ice_lite: false,
            stun_server: None,
            turn_server: None,
            turn_username: None,
            turn_password: None,
        }
    }
}

/// WHIP 会话状态
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum WhipSessionState {
    /// 收到 SDP offer
    SdpOfferReceived,
    /// 正在创建 PeerConnection
    CreatingPeerConnection,
    /// SDP answer 已生成
    SdpAnswerSent,
    /// ICE 完成
    IceConnected,
    /// 媒体传输中
    Streaming,
    /// 已终止
    Closed,
}

/// WHEP 会话状态
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum WhepSessionState {
    /// 收到 SDP offer
    SdpOfferReceived,
    /// 正在创建 PeerConnection
    CreatingPeerConnection,
    /// SDP answer 已生成
    SdpAnswerSent,
    /// ICE 完成
    IceConnected,
    /// 媒体接收中
    Receiving,
    /// 已终止
    Closed,
}

/// WHIP Handler — 处理 WebRTC 推流信令
///
/// 工作流程：
/// 1. 收到 POST /whip with SDP offer
/// 2. 创建 PeerConnection（委托给 WebRTC 引擎）
/// 3. 设置 remote description
/// 4. 生成 answer
/// 5. 返回 SDP answer
/// 6. 等待 ICE 完成
/// 7. 媒体通过 RTP 接收
pub struct WhipHandler {
    config: WhipWhepConfig,
    /// 活跃会话
    sessions: parking_lot::RwLock<std::collections::HashMap<String, WhipSessionState>>,
}

impl WhipHandler {
    pub fn new(config: WhipWhepConfig) -> Self {
        Self {
            config,
            sessions: parking_lot::RwLock::new(std::collections::HashMap::new()),
        }
    }

    /// 处理 WHIP 请求
    ///
    /// 输入：SDP offer
    /// 输出：SDP answer + session location
    pub async fn handle_offer(&self, sdp_offer: &str) -> anyhow::Result<(String, String)> {
        let session_id = uuid::Uuid::new_v4().to_string();

        // 标记状态
        {
            let mut sessions = self.sessions.write();
            sessions.insert(session_id.clone(), WhipSessionState::SdpOfferReceived);
        }

        // TODO: 创建 PeerConnection 并处理 SDP
        // 当前由 Go/Pion 层处理 WebRTC，这里只做信令桥接
        let sdp_answer = format!(
            "v=0\r\no=- {} 1 IN IP4 0.0.0.0\r\ns=-\r\nt=0 0\r\n",
            session_id
        );

        // 更新状态
        {
            let mut sessions = self.sessions.write();
            sessions.insert(session_id.clone(), WhipSessionState::SdpAnswerSent);
        }

        let location = format!("{}/{}", self.config.whip_path, session_id);

        Ok((sdp_answer, location))
    }

    /// 终止 WHIP 会话
    pub fn delete_session(&self, session_id: &str) -> anyhow::Result<()> {
        let mut sessions = self.sessions.write();
        if let Some(state) = sessions.get_mut(session_id) {
            *state = WhipSessionState::Closed;
        }
        sessions.remove(session_id);
        Ok(())
    }

    /// 获取会话状态
    pub fn session_state(&self, session_id: &str) -> Option<WhipSessionState> {
        self.sessions.read().get(session_id).cloned()
    }

    /// 活跃会话数
    pub fn session_count(&self) -> usize {
        self.sessions.read().len()
    }
}

/// WHEP Handler — 处理 WebRTC 拉流信令
pub struct WhepHandler {
    config: WhipWhepConfig,
    sessions: parking_lot::RwLock<std::collections::HashMap<String, WhepSessionState>>,
}

impl WhepHandler {
    pub fn new(config: WhipWhepConfig) -> Self {
        Self {
            config,
            sessions: parking_lot::RwLock::new(std::collections::HashMap::new()),
        }
    }

    /// 处理 WHEP 请求
    pub async fn handle_offer(&self, sdp_offer: &str) -> anyhow::Result<(String, String)> {
        let session_id = uuid::Uuid::new_v4().to_string();

        {
            let mut sessions = self.sessions.write();
            sessions.insert(session_id.clone(), WhepSessionState::SdpOfferReceived);
        }

        // TODO: 创建 PeerConnection 并处理 SDP
        let sdp_answer = format!(
            "v=0\r\no=- {} 1 IN IP4 0.0.0.0\r\ns=-\r\nt=0 0\r\n",
            session_id
        );

        {
            let mut sessions = self.sessions.write();
            sessions.insert(session_id.clone(), WhepSessionState::SdpAnswerSent);
        }

        let location = format!("{}/{}", self.config.whep_path, session_id);

        Ok((sdp_answer, location))
    }

    /// 终止 WHEP 会话
    pub fn delete_session(&self, session_id: &str) -> anyhow::Result<()> {
        let mut sessions = self.sessions.write();
        sessions.remove(session_id);
        Ok(())
    }

    /// 活跃会话数
    pub fn session_count(&self) -> usize {
        self.sessions.read().len()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn test_whip_handler() {
        let config = WhipWhepConfig::default();
        let handler = WhipHandler::new(config);

        let offer = "v=0\r\no=- 12345 1 IN IP4 0.0.0.0\r\n";
        let (answer, location) = handler.handle_offer(offer).await.unwrap();

        assert!(!answer.is_empty());
        assert!(location.contains("/whip/"));
        assert_eq!(handler.session_count(), 1);
    }

    #[tokio::test]
    async fn test_whep_handler() {
        let config = WhipWhepConfig::default();
        let handler = WhepHandler::new(config);

        let offer = "v=0\r\no=- 12345 1 IN IP4 0.0.0.0\r\n";
        let (answer, location) = handler.handle_offer(offer).await.unwrap();

        assert!(!answer.is_empty());
        assert!(location.contains("/whep/"));
    }
}
