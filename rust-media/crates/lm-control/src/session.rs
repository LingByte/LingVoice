//! 会话管理
//!
//! 管理 Rust 媒体面的所有会话、端点、轨道。
//! 使用 room → sessions 映射实现跨 session 路由：
//!   - 每个 participant 一个 session（避免自回声）
//!   - 同 room 的 session 互相转发 RTP（N-1）
//!   - push_rtp 收到包后，广播到同 room 其他 session 的 track broadcast channel

use std::sync::Arc;

use dashmap::DashMap;
use lm_core::{EndpointId, SessionId, TrackId};
use tracing::{debug, info};

/// 媒体会话（一个 participant 一个 session）
pub struct MediaSession {
    pub id: SessionId,
    pub room_id: Option<String>,
    pub tenant_id: Option<String>,
    pub endpoints: DashMap<EndpointId, Endpoint>,
    pub tracks: DashMap<TrackId, Arc<TrackState>>,
    pub created_at: std::time::Instant,
}

/// 端点
pub struct Endpoint {
    pub id: EndpointId,
    pub direction: lm_core::Direction,
    pub codec: lm_core::CodecType,
}

/// 轨道状态
///
/// 每个轨道有一个 broadcast channel：
/// - push_rtp 收到包后，通过 broadcast 发送给所有 pull_rtp 订阅者
/// - pull_rtp 调用 subscribe() 获取 receiver，通过 gRPC server stream 返回给 Go
///
/// 跨 session 路由：当 A push RTP 时，Rust 查找同 room 其他 session 的 track，
/// 把包 send 到它们的 broadcast channel，这样 B 的 pull_rtp 就能收到 A 的音频。
pub struct TrackState {
    pub track_id: TrackId,
    pub endpoint_id: EndpointId,
    pub session_id: SessionId,
    pub codec: lm_core::CodecType,
    pub kind: lm_core::TrackKind,
    pub ssrc: u32,
    /// RTP 包广播 channel（push_rtp → pull_rtp）
    pub rtp_broadcast: tokio::sync::broadcast::Sender<RtpPacketOut>,
}

/// 待发送的 RTP 包（pull_rtp 输出）
#[derive(Debug, Clone)]
pub struct RtpPacketOut {
    pub ssrc: u32,
    pub payload_type: u32,
    pub sequence_number: u32,
    pub timestamp: u32,
    pub marker: bool,
    pub payload: bytes::Bytes,
    pub rid: String,
    pub clock_rate: u32,
}

impl TrackState {
    /// 创建轨道状态
    pub fn new(
        track_id: TrackId,
        endpoint_id: EndpointId,
        session_id: SessionId,
        codec: lm_core::CodecType,
        kind: lm_core::TrackKind,
        ssrc: u32,
    ) -> Self {
        let (tx, _rx) = tokio::sync::broadcast::channel(512);
        Self {
            track_id,
            endpoint_id,
            session_id,
            codec,
            kind,
            ssrc,
            rtp_broadcast: tx,
        }
    }

    /// 订阅 RTP 包流（pull_rtp 调用）
    pub fn subscribe(&self) -> tokio::sync::broadcast::Receiver<RtpPacketOut> {
        self.rtp_broadcast.subscribe()
    }
}

/// 会话管理器
///
/// 维护两层映射：
/// 1. session_id → Arc<MediaSession>
/// 2. room_id → Vec<session_id>（用于跨 session 路由）
pub struct SessionManager {
    /// session_id → session
    sessions: DashMap<SessionId, Arc<MediaSession>>,
    /// room_id → session_id 列表
    rooms: DashMap<String, Vec<SessionId>>,
}

impl Default for SessionManager {
    fn default() -> Self {
        Self::new()
    }
}

impl SessionManager {
    pub fn new() -> Self {
        Self {
            sessions: DashMap::new(),
            rooms: DashMap::new(),
        }
    }

    /// 创建会话，并加入指定 room
    pub fn create_session(
        &self,
        session_id: SessionId,
        room_id: Option<String>,
        tenant_id: Option<String>,
    ) -> anyhow::Result<Arc<MediaSession>> {
        if self.sessions.contains_key(&session_id) {
            return Err(anyhow::anyhow!("session {} already exists", session_id.0));
        }

        let session = Arc::new(MediaSession {
            id: session_id.clone(),
            room_id: room_id.clone(),
            tenant_id,
            endpoints: DashMap::new(),
            tracks: DashMap::new(),
            created_at: std::time::Instant::now(),
        });

        // 加入 room
        if let Some(ref rid) = room_id {
            self.rooms
                .entry(rid.clone())
                .or_default()
                .push(session_id.clone());
            info!(session = %session_id.0, room = %rid, "session created and joined room");
        } else {
            info!(session = %session_id.0, "session created (no room)");
        }

        self.sessions.insert(session_id, session.clone());
        Ok(session)
    }

    /// 销毁会话，并从 room 移除
    pub fn destroy_session(&self, session_id: &SessionId) -> anyhow::Result<()> {
        let (_key, session) = self
            .sessions
            .remove(session_id)
            .ok_or_else(|| anyhow::anyhow!("session {} not found", session_id.0))?;

        // 从 room 移除
        if let Some(ref rid) = session.room_id {
            if let Some(mut members) = self.rooms.get_mut(rid) {
                members.retain(|s| s != session_id);
                debug!(session = %session_id.0, room = %rid, remaining = members.len(), "removed from room");
            }
            // 如果 room 空了，清理
            if let Some(members) = self.rooms.get(rid) {
                if members.is_empty() {
                    drop(members);
                    self.rooms.remove(rid);
                    debug!(room = %rid, "room removed (empty)");
                }
            }
        }

        info!(session = %session_id.0, "session destroyed");
        Ok(())
    }

    /// 获取会话
    pub fn get_session(&self, session_id: &SessionId) -> Option<Arc<MediaSession>> {
        self.sessions.get(session_id).map(|s| s.clone())
    }

    /// 获取同 room 其他 session 的指定 kind 的 track（用于跨 session 路由）
    ///
    /// 排除自己（src_session_id），返回其他 session 的同 kind track broadcast sender。
    /// 这是 N-1 路由的核心：A push 的音频包只转发给同 room 其他人的音频 track，
    /// 视频包只转发给视频 track，避免音频包污染视频 track。
    pub fn get_room_peer_tracks_by_kind(
        &self,
        src_session_id: &SessionId,
        kind: lm_core::TrackKind,
    ) -> Vec<Arc<TrackState>> {
        let session = match self.sessions.get(src_session_id) {
            Some(s) => s.clone(),
            None => return Vec::new(),
        };

        let room_id = match &session.room_id {
            Some(rid) => rid.clone(),
            None => return Vec::new(),
        };

        let peer_session_ids = match self.rooms.get(&room_id) {
            Some(members) => members
                .iter()
                .filter(|sid| *sid != src_session_id)
                .cloned()
                .collect::<Vec<_>>(),
            None => return Vec::new(),
        };

        let mut peer_tracks = Vec::new();
        for peer_sid in peer_session_ids {
            if let Some(peer_session) = self.sessions.get(&peer_sid) {
                for entry in peer_session.tracks.iter() {
                    if entry.kind == kind {
                        peer_tracks.push(entry.clone());
                    }
                }
            }
        }

        if !peer_tracks.is_empty() {
            debug!(
                session = %src_session_id.0,
                room = %room_id,
                peer_tracks = peer_tracks.len(),
                "found peer tracks for routing"
            );
        }

        peer_tracks
    }

    /// 会话数量
    pub fn session_count(&self) -> usize {
        self.sessions.len()
    }

    /// room 数量
    pub fn room_count(&self) -> usize {
        self.rooms.len()
    }

    /// 获取所有会话 ID
    pub fn session_ids(&self) -> Vec<SessionId> {
        self.sessions.iter().map(|s| s.id.clone()).collect()
    }
}
