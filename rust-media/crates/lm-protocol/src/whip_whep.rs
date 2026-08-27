//! WHIP/WHEP Handler — WebRTC HTTP 信令
//!
//! WHIP (WebRTC-HTTP Ingestion Protocol): https://datatracker.ietf.org/doc/draft-ietf-wish-whip/
//! WHEP (WebRTC-HTTP Egress Protocol): https://datatracker.ietf.org/doc/draft-ietf-wish-whep/
//!
//! 协议流程：
//! 1. Client POST SDP offer to WHIP/WHEP endpoint
//! 2. Server 创建 PeerConnection，设置 remote description
//! 3. Server 生成 SDP answer
//! 4. Server 返回 SDP answer in response body
//! 5. ICE 完成，媒体开始传输
//! 6. DELETE 请求终止会话
//!
//! 架构说明：Rust 层只做信令桥接（保存/查询 SDP offer、管理会话状态），
//! 实际的 WebRTC PeerConnection 创建、ICE 协商、DTLS 握手由 Go/Pion 层完成。
//! Rust 层将 SDP offer 透传给 Go 层，Go 层协商完成后回写 SDP answer。

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
    /// SDP offer 已保存，等待 Go 层协商
    PendingGoNegotiation,
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
    /// SDP offer 已保存，等待 Go 层协商
    PendingGoNegotiation,
    /// SDP answer 已生成
    SdpAnswerSent,
    /// ICE 完成
    IceConnected,
    /// 媒体接收中
    Receiving,
    /// 已终止
    Closed,
}

/// WHIP 会话数据（包含状态与待处理的 SDP offer）
#[derive(Debug, Clone)]
pub struct WhipSessionData {
    /// 当前会话状态
    pub state: WhipSessionState,
    /// 客户端发来的 SDP offer（等待 Go 层协商）
    pub pending_offer: Option<String>,
    /// Go 层返回的 SDP answer（协商完成后填充）
    pub sdp_answer: Option<String>,
}

/// WHEP 会话数据（包含状态与待处理的 SDP offer）
#[derive(Debug, Clone)]
pub struct WhepSessionData {
    /// 当前会话状态
    pub state: WhepSessionState,
    /// 客户端发来的 SDP offer（等待 Go 层协商）
    pub pending_offer: Option<String>,
    /// Go 层返回的 SDP answer（协商完成后填充）
    pub sdp_answer: Option<String>,
}

/// WHIP Handler — 处理 WebRTC 推流信令
///
/// 架构：Rust 层只做信令桥接，保存 SDP offer 并管理会话状态。
/// 实际的 PeerConnection 创建与 ICE 协商由 Go/Pion 层完成。
///
/// 工作流程：
/// 1. 收到 POST /whip with SDP offer
/// 2. Rust 层保存 SDP offer，标记为 PendingGoNegotiation
/// 3. 返回占位 SDP answer（标注 pending Go layer negotiation）
/// 4. Go 层读取 pending_offer，创建 PeerConnection，协商完成后回写 SDP answer
/// 5. ICE 完成，媒体通过 RTP 接收
/// 6. DELETE 请求终止会话
pub struct WhipHandler {
    config: WhipWhepConfig,
    /// 活跃会话：session_id → WhipSessionData
    sessions: parking_lot::RwLock<std::collections::HashMap<String, WhipSessionData>>,
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
    /// Rust 层不做 WebRTC PeerConnection 创建，而是：
    /// 1. 保存 SDP offer 到 session state
    /// 2. 返回占位 SDP answer（标注 "pending Go layer negotiation"）
    /// 3. Go 层后续读取 pending_offer 完成实际协商
    ///
    /// 输入：SDP offer
    /// 输出：(SDP answer, session location)
    pub async fn handle_offer(&self, sdp_offer: &str) -> anyhow::Result<(String, String)> {
        let session_id = uuid::Uuid::new_v4().to_string();

        // 保存 SDP offer 到 session state，标记为等待 Go 层协商
        {
            let mut sessions = self.sessions.write();
            sessions.insert(
                session_id.clone(),
                WhipSessionData {
                    state: WhipSessionState::PendingGoNegotiation,
                    pending_offer: Some(sdp_offer.to_string()),
                    sdp_answer: None,
                },
            );
        }

        // Rust 层不创建 PeerConnection，返回占位 SDP answer。
        // 实际 SDP answer 由 Go/Pion 层协商完成后通过 set_sdp_answer() 回写。
        let sdp_answer = format!(
            "v=0\r\n\
             o=- {} 1 IN IP4 0.0.0.0\r\n\
             s=-\r\n\
             t=0 0\r\n\
             a=tool:lm-protocol-rust\r\n\
             a=pending-go-layer-negotiation\r\n",
            session_id
        );

        // 更新状态：占位 answer 已发送，等待 Go 层提供真实 answer
        {
            let mut sessions = self.sessions.write();
            if let Some(data) = sessions.get_mut(&session_id) {
                data.state = WhipSessionState::SdpAnswerSent;
                data.sdp_answer = Some(sdp_answer.clone());
            }
        }

        let location = format!("{}/{}", self.config.whip_path, session_id);

        Ok((sdp_answer, location))
    }

    /// PATCH 会话 — 用于 ICE restart
    ///
    /// 接收新的 SDP offer（包含 ICE restart 标记），更新 session state，
    /// 返回更新后的占位 SDP answer（仍由 Go 层完成实际协商）。
    ///
    /// 如果 session 不存在则返回错误。
    pub async fn patch_session(
        &self,
        session_id: &str,
        new_sdp_offer: &str,
    ) -> anyhow::Result<String> {
        let mut sessions = self.sessions.write();
        let data = sessions
            .get_mut(session_id)
            .ok_or_else(|| anyhow::anyhow!("WHIP session not found: {}", session_id))?;

        if data.state == WhipSessionState::Closed {
            anyhow::bail!("WHIP session already closed: {}", session_id);
        }

        // 更新 pending_offer 为新的 SDP offer（ICE restart）
        data.pending_offer = Some(new_sdp_offer.to_string());
        data.state = WhipSessionState::PendingGoNegotiation;

        // 返回更新后的占位 SDP answer
        let sdp_answer = format!(
            "v=0\r\n\
             o=- {} 2 IN IP4 0.0.0.0\r\n\
             s=-\r\n\
             t=0 0\r\n\
             a=tool:lm-protocol-rust\r\n\
             a=ice-restart\r\n\
             a=pending-go-layer-negotiation\r\n",
            session_id
        );
        data.sdp_answer = Some(sdp_answer.clone());
        data.state = WhipSessionState::SdpAnswerSent;

        Ok(sdp_answer)
    }

    /// Go 层协商完成后回写真实 SDP answer
    pub fn set_sdp_answer(&self, session_id: &str, sdp_answer: &str) -> anyhow::Result<()> {
        let mut sessions = self.sessions.write();
        let data = sessions
            .get_mut(session_id)
            .ok_or_else(|| anyhow::anyhow!("WHIP session not found: {}", session_id))?;

        data.sdp_answer = Some(sdp_answer.to_string());
        data.pending_offer = None;
        Ok(())
    }

    /// 获取待处理的 SDP offer（供 Go 层读取协商）
    pub fn pending_offer(&self, session_id: &str) -> Option<String> {
        self.sessions
            .read()
            .get(session_id)
            .and_then(|d| d.pending_offer.clone())
    }

    /// 终止 WHIP 会话
    pub fn delete_session(&self, session_id: &str) -> anyhow::Result<()> {
        let mut sessions = self.sessions.write();
        if let Some(data) = sessions.get_mut(session_id) {
            data.state = WhipSessionState::Closed;
        }
        sessions.remove(session_id);
        Ok(())
    }

    /// 获取会话状态
    pub fn session_state(&self, session_id: &str) -> Option<WhipSessionState> {
        self.sessions
            .read()
            .get(session_id)
            .map(|d| d.state.clone())
    }

    /// 获取会话完整数据
    pub fn session_data(&self, session_id: &str) -> Option<WhipSessionData> {
        self.sessions.read().get(session_id).cloned()
    }

    /// 活跃会话数
    pub fn session_count(&self) -> usize {
        self.sessions.read().len()
    }
}

/// WHEP Handler — 处理 WebRTC 拉流信令
///
/// 架构与 WhipHandler 相同：Rust 层只做信令桥接，WebRTC 由 Go/Pion 层处理。
pub struct WhepHandler {
    config: WhipWhepConfig,
    sessions: parking_lot::RwLock<std::collections::HashMap<String, WhepSessionData>>,
}

impl WhepHandler {
    pub fn new(config: WhipWhepConfig) -> Self {
        Self {
            config,
            sessions: parking_lot::RwLock::new(std::collections::HashMap::new()),
        }
    }

    /// 处理 WHEP 请求
    ///
    /// Rust 层不做 WebRTC PeerConnection 创建，而是保存 SDP offer 并返回占位 answer。
    /// 实际 SDP answer 由 Go/Pion 层协商完成后通过 set_sdp_answer() 回写。
    pub async fn handle_offer(&self, sdp_offer: &str) -> anyhow::Result<(String, String)> {
        let session_id = uuid::Uuid::new_v4().to_string();

        {
            let mut sessions = self.sessions.write();
            sessions.insert(
                session_id.clone(),
                WhepSessionData {
                    state: WhepSessionState::PendingGoNegotiation,
                    pending_offer: Some(sdp_offer.to_string()),
                    sdp_answer: None,
                },
            );
        }

        let sdp_answer = format!(
            "v=0\r\n\
             o=- {} 1 IN IP4 0.0.0.0\r\n\
             s=-\r\n\
             t=0 0\r\n\
             a=tool:lm-protocol-rust\r\n\
             a=pending-go-layer-negotiation\r\n",
            session_id
        );

        {
            let mut sessions = self.sessions.write();
            if let Some(data) = sessions.get_mut(&session_id) {
                data.state = WhepSessionState::SdpAnswerSent;
                data.sdp_answer = Some(sdp_answer.clone());
            }
        }

        let location = format!("{}/{}", self.config.whep_path, session_id);

        Ok((sdp_answer, location))
    }

    /// PATCH 会话 — 用于 ICE restart
    ///
    /// 接收新的 SDP offer（包含 ICE restart 标记），更新 session state，
    /// 返回更新后的占位 SDP answer。
    pub async fn patch_session(
        &self,
        session_id: &str,
        new_sdp_offer: &str,
    ) -> anyhow::Result<String> {
        let mut sessions = self.sessions.write();
        let data = sessions
            .get_mut(session_id)
            .ok_or_else(|| anyhow::anyhow!("WHEP session not found: {}", session_id))?;

        if data.state == WhepSessionState::Closed {
            anyhow::bail!("WHEP session already closed: {}", session_id);
        }

        data.pending_offer = Some(new_sdp_offer.to_string());
        data.state = WhepSessionState::PendingGoNegotiation;

        let sdp_answer = format!(
            "v=0\r\n\
             o=- {} 2 IN IP4 0.0.0.0\r\n\
             s=-\r\n\
             t=0 0\r\n\
             a=tool:lm-protocol-rust\r\n\
             a=ice-restart\r\n\
             a=pending-go-layer-negotiation\r\n",
            session_id
        );
        data.sdp_answer = Some(sdp_answer.clone());
        data.state = WhepSessionState::SdpAnswerSent;

        Ok(sdp_answer)
    }

    /// Go 层协商完成后回写真实 SDP answer
    pub fn set_sdp_answer(&self, session_id: &str, sdp_answer: &str) -> anyhow::Result<()> {
        let mut sessions = self.sessions.write();
        let data = sessions
            .get_mut(session_id)
            .ok_or_else(|| anyhow::anyhow!("WHEP session not found: {}", session_id))?;

        data.sdp_answer = Some(sdp_answer.to_string());
        data.pending_offer = None;
        Ok(())
    }

    /// 获取待处理的 SDP offer（供 Go 层读取协商）
    pub fn pending_offer(&self, session_id: &str) -> Option<String> {
        self.sessions
            .read()
            .get(session_id)
            .and_then(|d| d.pending_offer.clone())
    }

    /// 终止 WHEP 会话
    pub fn delete_session(&self, session_id: &str) -> anyhow::Result<()> {
        let mut sessions = self.sessions.write();
        if let Some(data) = sessions.get_mut(session_id) {
            data.state = WhepSessionState::Closed;
        }
        sessions.remove(session_id);
        Ok(())
    }

    /// 获取会话状态
    pub fn session_state(&self, session_id: &str) -> Option<WhepSessionState> {
        self.sessions
            .read()
            .get(session_id)
            .map(|d| d.state.clone())
    }

    /// 获取会话完整数据
    pub fn session_data(&self, session_id: &str) -> Option<WhepSessionData> {
        self.sessions.read().get(session_id).cloned()
    }

    /// 活跃会话数
    pub fn session_count(&self) -> usize {
        self.sessions.read().len()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// 测试 WHIP create_session + handle_offer 流程
    #[tokio::test]
    async fn test_whip_handle_offer() {
        let config = WhipWhepConfig::default();
        let handler = WhipHandler::new(config);

        let offer = "v=0\r\no=- 12345 1 IN IP4 0.0.0.0\r\nm=audio 1234 RTP/AVP 0\r\n";
        let (answer, location) = handler.handle_offer(offer).await.unwrap();

        // SDP answer 非空且标注 pending Go layer negotiation
        assert!(!answer.is_empty());
        assert!(answer.contains("pending-go-layer-negotiation"));
        assert!(answer.starts_with("v=0\r\n"));

        // location 包含 whip path
        assert!(location.contains("/whip/"));

        // 会话已创建
        assert_eq!(handler.session_count(), 1);
    }

    /// 测试 WHIP handle_offer 保存了 pending_offer
    #[tokio::test]
    async fn test_whip_pending_offer_saved() {
        let config = WhipWhepConfig::default();
        let handler = WhipHandler::new(config);

        let offer = "v=0\r\no=- 12345 1 IN IP4 0.0.0.0\r\nm=audio 1234 RTP/AVP 0\r\n";
        let (_answer, location) = handler.handle_offer(offer).await.unwrap();

        // 从 location 提取 session_id
        let session_id = location.rsplit('/').next().unwrap();

        // pending_offer 应该被保存
        let pending = handler.pending_offer(session_id);
        assert!(pending.is_some());
        assert_eq!(pending.unwrap(), offer);

        // session state 应该是 SdpAnswerSent
        let state = handler.session_state(session_id);
        assert_eq!(state, Some(WhipSessionState::SdpAnswerSent));
    }

    /// 测试 WHIP delete_session
    #[tokio::test]
    async fn test_whip_delete_session() {
        let config = WhipWhepConfig::default();
        let handler = WhipHandler::new(config);

        let offer = "v=0\r\no=- 12345 1 IN IP4 0.0.0.0\r\n";
        let (_answer, location) = handler.handle_offer(offer).await.unwrap();
        let session_id = location.rsplit('/').next().unwrap();

        assert_eq!(handler.session_count(), 1);

        // 删除会话
        handler.delete_session(session_id).unwrap();
        assert_eq!(handler.session_count(), 0);

        // 再次删除不存在的会话不应 panic（返回 Ok）
        handler.delete_session(session_id).unwrap();
    }

    /// 测试 WHIP session_state 查询
    #[tokio::test]
    async fn test_whip_session_state_query() {
        let config = WhipWhepConfig::default();
        let handler = WhipHandler::new(config);

        // 不存在的 session
        assert_eq!(handler.session_state("nonexistent"), None);

        let offer = "v=0\r\no=- 12345 1 IN IP4 0.0.0.0\r\n";
        let (_answer, location) = handler.handle_offer(offer).await.unwrap();
        let session_id = location.rsplit('/').next().unwrap();

        // 存在的 session
        assert_eq!(
            handler.session_state(session_id),
            Some(WhipSessionState::SdpAnswerSent)
        );
    }

    /// 测试 WHIP patch_session (ICE restart)
    #[tokio::test]
    async fn test_whip_patch_session() {
        let config = WhipWhepConfig::default();
        let handler = WhipHandler::new(config);

        let offer = "v=0\r\no=- 12345 1 IN IP4 0.0.0.0\r\nm=audio 1234 RTP/AVP 0\r\n";
        let (_answer, location) = handler.handle_offer(offer).await.unwrap();
        let session_id = location.rsplit('/').next().unwrap();

        // ICE restart: 新的 SDP offer
        let new_offer =
            "v=0\r\no=- 12345 2 IN IP4 0.0.0.0\r\na=ice-restart\r\nm=audio 1234 RTP/AVP 0\r\n";
        let patched_answer = handler.patch_session(session_id, new_offer).await.unwrap();

        // patch 后的 answer 应包含 ice-restart 标记
        assert!(patched_answer.contains("ice-restart"));
        assert!(patched_answer.contains("pending-go-layer-negotiation"));

        // pending_offer 应更新为新的 offer
        let pending = handler.pending_offer(session_id);
        assert_eq!(pending, Some(new_offer.to_string()));

        // patch 不存在的 session 应返回错误
        let result = handler.patch_session("nonexistent", new_offer).await;
        assert!(result.is_err());
    }

    /// 测试 WHIP set_sdp_answer (Go 层回写)
    #[tokio::test]
    async fn test_whip_set_sdp_answer() {
        let config = WhipWhepConfig::default();
        let handler = WhipHandler::new(config);

        let offer = "v=0\r\no=- 12345 1 IN IP4 0.0.0.0\r\n";
        let (_answer, location) = handler.handle_offer(offer).await.unwrap();
        let session_id = location.rsplit('/').next().unwrap();

        // Go 层回写真实 answer
        let real_answer = "v=0\r\no=- server 1 IN IP4 10.0.0.1\r\nm=audio 5678 RTP/AVP 0\r\n";
        handler.set_sdp_answer(session_id, real_answer).unwrap();

        // pending_offer 应被清除
        assert_eq!(handler.pending_offer(session_id), None);

        // session_data 中应包含真实 answer
        let data = handler.session_data(session_id).unwrap();
        assert_eq!(data.sdp_answer, Some(real_answer.to_string()));
    }

    /// 测试 WHEP handle_offer
    #[tokio::test]
    async fn test_whep_handle_offer() {
        let config = WhipWhepConfig::default();
        let handler = WhepHandler::new(config);

        let offer = "v=0\r\no=- 12345 1 IN IP4 0.0.0.0\r\nm=audio 1234 RTP/AVP 0\r\n";
        let (answer, location) = handler.handle_offer(offer).await.unwrap();

        assert!(!answer.is_empty());
        assert!(answer.contains("pending-go-layer-negotiation"));
        assert!(location.contains("/whep/"));
        assert_eq!(handler.session_count(), 1);
    }

    /// 测试 WHEP delete_session
    #[tokio::test]
    async fn test_whep_delete_session() {
        let config = WhipWhepConfig::default();
        let handler = WhepHandler::new(config);

        let offer = "v=0\r\no=- 12345 1 IN IP4 0.0.0.0\r\n";
        let (_answer, location) = handler.handle_offer(offer).await.unwrap();
        let session_id = location.rsplit('/').next().unwrap();

        assert_eq!(handler.session_count(), 1);
        handler.delete_session(session_id).unwrap();
        assert_eq!(handler.session_count(), 0);
    }

    /// 测试 WHEP session_state 查询
    #[tokio::test]
    async fn test_whep_session_state_query() {
        let config = WhipWhepConfig::default();
        let handler = WhepHandler::new(config);

        assert_eq!(handler.session_state("nonexistent"), None);

        let offer = "v=0\r\no=- 12345 1 IN IP4 0.0.0.0\r\n";
        let (_answer, location) = handler.handle_offer(offer).await.unwrap();
        let session_id = location.rsplit('/').next().unwrap();

        assert_eq!(
            handler.session_state(session_id),
            Some(WhepSessionState::SdpAnswerSent)
        );
    }

    /// 测试 WHEP patch_session (ICE restart)
    #[tokio::test]
    async fn test_whep_patch_session() {
        let config = WhipWhepConfig::default();
        let handler = WhepHandler::new(config);

        let offer = "v=0\r\no=- 12345 1 IN IP4 0.0.0.0\r\nm=audio 1234 RTP/AVP 0\r\n";
        let (_answer, location) = handler.handle_offer(offer).await.unwrap();
        let session_id = location.rsplit('/').next().unwrap();

        let new_offer =
            "v=0\r\no=- 12345 2 IN IP4 0.0.0.0\r\na=ice-restart\r\nm=audio 1234 RTP/AVP 0\r\n";
        let patched_answer = handler.patch_session(session_id, new_offer).await.unwrap();

        assert!(patched_answer.contains("ice-restart"));
        assert!(patched_answer.contains("pending-go-layer-negotiation"));

        let pending = handler.pending_offer(session_id);
        assert_eq!(pending, Some(new_offer.to_string()));

        let result = handler.patch_session("nonexistent", new_offer).await;
        assert!(result.is_err());
    }
}
