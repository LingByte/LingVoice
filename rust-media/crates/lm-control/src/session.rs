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

#[cfg(test)]
mod tests {
    use super::*;
    use lm_core::{CodecType, EndpointId, SessionId, TrackId, TrackKind};

    fn make_session_id(n: u32) -> SessionId {
        SessionId(format!("sess-{}", n))
    }
    fn make_track_id(n: u32) -> TrackId {
        TrackId(format!("track-{}", n))
    }
    fn make_endpoint_id(n: u32) -> EndpointId {
        EndpointId(format!("ep-{}", n))
    }
    fn add_audio_track(sm: &SessionManager, sid: &SessionId, tid: &str) {
        let session = sm.get_session(sid).unwrap();
        let track = TrackState::new(
            TrackId(tid.to_string()),
            EndpointId("ep".to_string()),
            sid.clone(),
            CodecType::Opus,
            TrackKind::Audio,
            1000,
        );
        session
            .tracks
            .insert(TrackId(tid.to_string()), Arc::new(track));
    }
    fn add_video_track(sm: &SessionManager, sid: &SessionId, tid: &str) {
        let session = sm.get_session(sid).unwrap();
        let track = TrackState::new(
            TrackId(tid.to_string()),
            EndpointId("ep".to_string()),
            sid.clone(),
            CodecType::Vp8,
            TrackKind::Video,
            2000,
        );
        session
            .tracks
            .insert(TrackId(tid.to_string()), Arc::new(track));
    }

    // ========================================================================
    // 1. SessionManager creation
    // ========================================================================

    #[test]
    fn test_create_session() {
        let sm = SessionManager::new();
        let sid = make_session_id(1);
        let session = sm
            .create_session(sid.clone(), Some("room-1".to_string()), None)
            .unwrap();
        assert_eq!(session.id, sid);
        assert!(sm.get_session(&sid).is_some());
        assert_eq!(sm.session_count(), 1);
    }

    #[test]
    fn test_create_duplicate_session() {
        let sm = SessionManager::new();
        let sid = make_session_id(1);
        sm.create_session(sid.clone(), Some("room-1".to_string()), None)
            .unwrap();
        let result = sm.create_session(sid.clone(), Some("room-1".to_string()), None);
        assert!(result.is_err());
        assert_eq!(sm.session_count(), 1);
    }

    #[test]
    fn test_create_session_no_room() {
        let sm = SessionManager::new();
        let sid = make_session_id(1);
        let session = sm.create_session(sid.clone(), None, None).unwrap();
        assert!(session.room_id.is_none());
        assert_eq!(sm.session_count(), 1);
        assert_eq!(sm.room_count(), 0);
    }

    // ========================================================================
    // 2. Session destruction
    // ========================================================================

    #[test]
    fn test_destroy_session() {
        let sm = SessionManager::new();
        let sid = make_session_id(1);
        sm.create_session(sid.clone(), Some("room-1".to_string()), None)
            .unwrap();
        assert_eq!(sm.session_count(), 1);
        sm.destroy_session(&sid).unwrap();
        assert_eq!(sm.session_count(), 0);
        assert!(sm.get_session(&sid).is_none());
    }

    #[test]
    fn test_destroy_nonexistent() {
        let sm = SessionManager::new();
        let sid = make_session_id(1);
        let result = sm.destroy_session(&sid);
        assert!(result.is_err());
    }

    #[test]
    fn test_destroy_removes_from_room() {
        let sm = SessionManager::new();
        let sid1 = make_session_id(1);
        let sid2 = make_session_id(2);
        sm.create_session(sid1.clone(), Some("room-1".to_string()), None)
            .unwrap();
        sm.create_session(sid2.clone(), Some("room-1".to_string()), None)
            .unwrap();
        // Add audio tracks so peer lookup is meaningful
        add_audio_track(&sm, &sid1, "a1");
        add_audio_track(&sm, &sid2, "a2");

        // Before destroy: sid1 sees 1 peer audio track
        let peers = sm.get_room_peer_tracks_by_kind(&sid1, TrackKind::Audio);
        assert_eq!(peers.len(), 1);

        // Destroy sid2
        sm.destroy_session(&sid2).unwrap();

        // After destroy: sid1 sees 0 peer audio tracks, but room still exists
        let peers_after = sm.get_room_peer_tracks_by_kind(&sid1, TrackKind::Audio);
        assert_eq!(peers_after.len(), 0);
        // room still has sid1
        assert_eq!(sm.room_count(), 1);
        assert_eq!(sm.session_count(), 1);
    }

    #[test]
    fn test_destroy_empties_room() {
        let sm = SessionManager::new();
        let sid = make_session_id(1);
        sm.create_session(sid.clone(), Some("room-1".to_string()), None)
            .unwrap();
        assert_eq!(sm.room_count(), 1);
        sm.destroy_session(&sid).unwrap();
        assert_eq!(sm.room_count(), 0);
        assert_eq!(sm.session_count(), 0);
    }

    // ========================================================================
    // 3. Room peer track lookup (kind-aware routing)
    // ========================================================================

    #[test]
    fn test_get_room_peer_tracks_by_kind_audio_only() {
        let sm = SessionManager::new();
        let sid1 = make_session_id(1);
        let sid2 = make_session_id(2);
        sm.create_session(sid1.clone(), Some("room-1".to_string()), None)
            .unwrap();
        sm.create_session(sid2.clone(), Some("room-1".to_string()), None)
            .unwrap();
        add_audio_track(&sm, &sid1, "a1");
        add_audio_track(&sm, &sid2, "a2");

        let peers = sm.get_room_peer_tracks_by_kind(&sid1, TrackKind::Audio);
        assert_eq!(peers.len(), 1);
        assert_eq!(peers[0].kind, TrackKind::Audio);
    }

    #[test]
    fn test_get_room_peer_tracks_by_kind_video_only() {
        let sm = SessionManager::new();
        let sid1 = make_session_id(1);
        let sid2 = make_session_id(2);
        sm.create_session(sid1.clone(), Some("room-1".to_string()), None)
            .unwrap();
        sm.create_session(sid2.clone(), Some("room-1".to_string()), None)
            .unwrap();
        add_video_track(&sm, &sid1, "v1");
        add_video_track(&sm, &sid2, "v2");

        let peers = sm.get_room_peer_tracks_by_kind(&sid1, TrackKind::Video);
        assert_eq!(peers.len(), 1);
        assert_eq!(peers[0].kind, TrackKind::Video);
    }

    #[test]
    fn test_get_room_peer_tracks_by_kind_mixed() {
        let sm = SessionManager::new();
        let sid1 = make_session_id(1);
        let sid2 = make_session_id(2);
        sm.create_session(sid1.clone(), Some("room-1".to_string()), None)
            .unwrap();
        sm.create_session(sid2.clone(), Some("room-1".to_string()), None)
            .unwrap();
        add_audio_track(&sm, &sid1, "a1");
        add_video_track(&sm, &sid1, "v1");
        add_audio_track(&sm, &sid2, "a2");
        add_video_track(&sm, &sid2, "v2");

        let audio_peers = sm.get_room_peer_tracks_by_kind(&sid1, TrackKind::Audio);
        assert_eq!(audio_peers.len(), 1);
        assert_eq!(audio_peers[0].kind, TrackKind::Audio);

        let video_peers = sm.get_room_peer_tracks_by_kind(&sid1, TrackKind::Video);
        assert_eq!(video_peers.len(), 1);
        assert_eq!(video_peers[0].kind, TrackKind::Video);
    }

    #[test]
    fn test_get_room_peer_tracks_excludes_self() {
        let sm = SessionManager::new();
        let sid1 = make_session_id(1);
        let sid2 = make_session_id(2);
        sm.create_session(sid1.clone(), Some("room-1".to_string()), None)
            .unwrap();
        sm.create_session(sid2.clone(), Some("room-1".to_string()), None)
            .unwrap();
        add_audio_track(&sm, &sid1, "a1");
        add_audio_track(&sm, &sid2, "a2");

        let peers = sm.get_room_peer_tracks_by_kind(&sid1, TrackKind::Audio);
        // Should only contain sid2's track, not sid1's own
        assert_eq!(peers.len(), 1);
        for p in &peers {
            assert_ne!(p.session_id, sid1);
        }
    }

    #[test]
    fn test_get_room_peer_tracks_no_room() {
        let sm = SessionManager::new();
        let sid = make_session_id(1);
        sm.create_session(sid.clone(), None, None).unwrap();
        add_audio_track(&sm, &sid, "a1");

        let peers = sm.get_room_peer_tracks_by_kind(&sid, TrackKind::Audio);
        assert!(peers.is_empty());
    }

    #[test]
    fn test_get_room_peer_tracks_isolated_rooms() {
        let sm = SessionManager::new();
        let sid1 = make_session_id(1);
        let sid2 = make_session_id(2);
        let sid3 = make_session_id(3);
        sm.create_session(sid1.clone(), Some("room-A".to_string()), None)
            .unwrap();
        sm.create_session(sid2.clone(), Some("room-A".to_string()), None)
            .unwrap();
        sm.create_session(sid3.clone(), Some("room-B".to_string()), None)
            .unwrap();
        add_audio_track(&sm, &sid1, "a1");
        add_audio_track(&sm, &sid2, "a2");
        add_audio_track(&sm, &sid3, "a3");

        // sid1 (room-A) should only see sid2, not sid3
        let peers = sm.get_room_peer_tracks_by_kind(&sid1, TrackKind::Audio);
        assert_eq!(peers.len(), 1);
        assert_eq!(peers[0].session_id, sid2);

        // sid3 (room-B) should see no peers
        let peers_b = sm.get_room_peer_tracks_by_kind(&sid3, TrackKind::Audio);
        assert!(peers_b.is_empty());
    }

    #[test]
    fn test_get_room_peer_tracks_kind_filter() {
        let sm = SessionManager::new();
        let sid1 = make_session_id(1);
        let sid2 = make_session_id(2);
        sm.create_session(sid1.clone(), Some("room-1".to_string()), None)
            .unwrap();
        sm.create_session(sid2.clone(), Some("room-1".to_string()), None)
            .unwrap();
        // sid1 has only audio, sid2 has only video
        add_audio_track(&sm, &sid1, "a1");
        add_video_track(&sm, &sid2, "v2");

        // sid1 looks for peer audio → sid2 has no audio → 0
        let audio_peers = sm.get_room_peer_tracks_by_kind(&sid1, TrackKind::Audio);
        assert_eq!(audio_peers.len(), 0);

        // sid2 looks for peer video → sid1 has no video → 0
        let video_peers = sm.get_room_peer_tracks_by_kind(&sid2, TrackKind::Video);
        assert_eq!(video_peers.len(), 0);
    }

    // ========================================================================
    // 4. TrackState broadcast
    // ========================================================================

    #[test]
    fn test_track_state_broadcast() {
        let track = TrackState::new(
            make_track_id(1),
            make_endpoint_id(1),
            make_session_id(1),
            CodecType::Opus,
            TrackKind::Audio,
            1000,
        );
        let mut rx = track.subscribe();
        let packet = RtpPacketOut {
            ssrc: 1000,
            payload_type: 111,
            sequence_number: 1,
            timestamp: 160,
            marker: true,
            payload: bytes::Bytes::from_static(b"hello"),
            rid: String::new(),
            clock_rate: 48000,
        };
        track.rtp_broadcast.send(packet.clone()).unwrap();
        let received = rx.try_recv();
        assert!(received.is_ok());
        let got = received.unwrap();
        assert_eq!(got.ssrc, 1000);
        assert_eq!(got.payload, bytes::Bytes::from_static(b"hello"));
    }

    #[test]
    fn test_track_state_multiple_subscribers() {
        let track = TrackState::new(
            make_track_id(1),
            make_endpoint_id(1),
            make_session_id(1),
            CodecType::Vp8,
            TrackKind::Video,
            2000,
        );
        let mut rx1 = track.subscribe();
        let mut rx2 = track.subscribe();
        let packet = RtpPacketOut {
            ssrc: 2000,
            payload_type: 96,
            sequence_number: 10,
            timestamp: 3000,
            marker: false,
            payload: bytes::Bytes::from_static(b"video"),
            rid: String::new(),
            clock_rate: 90000,
        };
        track.rtp_broadcast.send(packet.clone()).unwrap();
        let r1 = rx1.try_recv();
        let r2 = rx2.try_recv();
        assert!(r1.is_ok());
        assert!(r2.is_ok());
        assert_eq!(r1.unwrap().ssrc, 2000);
        assert_eq!(r2.unwrap().ssrc, 2000);
    }

    // ========================================================================
    // 5. Edge cases
    // ========================================================================

    #[test]
    fn test_session_ids() {
        let sm = SessionManager::new();
        let sid1 = make_session_id(1);
        let sid2 = make_session_id(2);
        let sid3 = make_session_id(3);
        sm.create_session(sid1.clone(), Some("room-1".to_string()), None)
            .unwrap();
        sm.create_session(sid2.clone(), Some("room-1".to_string()), None)
            .unwrap();
        sm.create_session(sid3.clone(), Some("room-2".to_string()), None)
            .unwrap();

        let ids = sm.session_ids();
        assert_eq!(ids.len(), 3);
        assert!(ids.contains(&sid1));
        assert!(ids.contains(&sid2));
        assert!(ids.contains(&sid3));
    }

    #[test]
    fn test_room_count() {
        let sm = SessionManager::new();
        let sid1 = make_session_id(1);
        let sid2 = make_session_id(2);
        sm.create_session(sid1.clone(), Some("room-1".to_string()), None)
            .unwrap();
        sm.create_session(sid2.clone(), Some("room-2".to_string()), None)
            .unwrap();
        assert_eq!(sm.room_count(), 2);
    }
}
