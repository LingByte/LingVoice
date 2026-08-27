//! 会话桥接管理器
//!
//! 参考 forge-media ForwardingEngine + rustpbx ConferenceMediaBridge。
//!
//! 桥接两个 session 的媒体流，实现双向转发。
//! - 同 codec：零拷贝 relay（直接转发 RTP 包）
//! - 不同 codec：转码桥接（解码→重编码）
//!
//! 桥接通过订阅 source track 的 broadcast channel，
//! 将 RTP 包转发到 target track 的 broadcast channel。

use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;

use dashmap::DashMap;
use tokio::sync::broadcast;
use tracing::info;

use crate::session::{MediaSession, RtpPacketOut, TrackState};

/// 桥接键
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
struct BridgeKey {
    session_a: String,
    session_b: String,
}

/// 桥接方向
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum BridgeDirection {
    AToB,
    BToA,
}

/// 桥接管理器
pub struct BridgeManager {
    /// 活跃的桥接：BridgeKey → 停止标志
    bridges: DashMap<BridgeKey, Arc<AtomicBool>>,
}

impl Default for BridgeManager {
    fn default() -> Self {
        Self::new()
    }
}

impl BridgeManager {
    pub fn new() -> Self {
        Self {
            bridges: DashMap::new(),
        }
    }

    /// 桥接两个 session
    ///
    /// 将 session_a 的所有 track 转发到 session_b，反之亦然。
    /// 如果两 session 的 codec 相同，使用零拷贝 relay。
    /// 如果不同，需要转码（当前简化为 relay，转码由 push_rtp 热路径处理）。
    pub fn bridge_sessions(
        &self,
        session_a: &Arc<MediaSession>,
        session_b: &Arc<MediaSession>,
    ) -> Result<bool, String> {
        let key = BridgeKey {
            session_a: session_a.id.0.clone(),
            session_b: session_b.id.0.clone(),
        };

        if self.bridges.contains_key(&key) {
            return Err(format!(
                "bridge {} <-> {} already exists",
                key.session_a, key.session_b
            ));
        }

        let stopped = Arc::new(AtomicBool::new(false));

        // 收集两 session 的所有 track
        let tracks_a: Vec<Arc<TrackState>> =
            session_a.tracks.iter().map(|t| t.clone()).collect();
        let tracks_b: Vec<Arc<TrackState>> =
            session_b.tracks.iter().map(|t| t.clone()).collect();

        // A → B 转发
        for src_track in &tracks_a {
            let dst_tracks: Vec<Arc<TrackState>> = tracks_b
                .iter()
                .filter(|t| t.kind == src_track.kind)
                .cloned()
                .collect();

            for dst_track in dst_tracks {
                let stopped_clone = stopped.clone();
                let src_rx = src_track.subscribe();
                let dst_tx = dst_track.rtp_broadcast.clone();
                let src_session = session_a.id.0.clone();
                let dst_session = session_b.id.0.clone();
                let track_id = src_track.track_id.0.clone();

                tokio::spawn(async move {
                    run_bridge_forward(
                        src_rx,
                        dst_tx,
                        stopped_clone,
                        src_session,
                        dst_session,
                        track_id,
                    )
                    .await;
                });
            }
        }

        // B → A 转发
        for src_track in &tracks_b {
            let dst_tracks: Vec<Arc<TrackState>> = tracks_a
                .iter()
                .filter(|t| t.kind == src_track.kind)
                .cloned()
                .collect();

            for dst_track in dst_tracks {
                let stopped_clone = stopped.clone();
                let src_rx = src_track.subscribe();
                let dst_tx = dst_track.rtp_broadcast.clone();
                let src_session = session_b.id.0.clone();
                let dst_session = session_a.id.0.clone();
                let track_id = src_track.track_id.0.clone();

                tokio::spawn(async move {
                    run_bridge_forward(
                        src_rx,
                        dst_tx,
                        stopped_clone,
                        src_session,
                        dst_session,
                        track_id,
                    )
                    .await;
                });
            }
        }

        self.bridges.insert(key.clone(), stopped.clone());

        info!(
            session_a = %key.session_a,
            session_b = %key.session_b,
            tracks_a = tracks_a.len(),
            tracks_b = tracks_b.len(),
            "bridge established"
        );

        // 判断是否 relay 模式（同 codec）
        let relay_mode = tracks_a
            .iter()
            .zip(tracks_b.iter())
            .all(|(a, b)| a.codec == b.codec);

        Ok(relay_mode)
    }

    /// 解除桥接
    pub fn unbridge_sessions(
        &self,
        session_a: &str,
        session_b: &str,
    ) -> Result<(), String> {
        let key = BridgeKey {
            session_a: session_a.to_string(),
            session_b: session_b.to_string(),
        };

        let (_, stopped) = self
            .bridges
            .remove(&key)
            .ok_or_else(|| format!("bridge {} <-> {} not found", session_a, session_b))?;

        stopped.store(true, Ordering::Relaxed);

        info!(
            session_a = %key.session_a,
            session_b = %key.session_b,
            "bridge removed"
        );

        Ok(())
    }

    /// 桥接数量
    pub fn bridge_count(&self) -> usize {
        self.bridges.len()
    }

    /// 检查两 session 是否已桥接
    pub fn is_bridged(&self, session_a: &str, session_b: &str) -> bool {
        self.bridges.contains_key(&BridgeKey {
            session_a: session_a.to_string(),
            session_b: session_b.to_string(),
        })
    }
}

/// 桥接转发任务
///
/// 从 source track 的 broadcast channel 接收 RTP 包，
/// 转发到 target track 的 broadcast channel。
async fn run_bridge_forward(
    mut src_rx: broadcast::Receiver<RtpPacketOut>,
    dst_tx: broadcast::Sender<RtpPacketOut>,
    stopped: Arc<AtomicBool>,
    src_session: String,
    dst_session: String,
    track_id: String,
) {
    info!(
        src = %src_session,
        dst = %dst_session,
        track = %track_id,
        "bridge forwarder started"
    );

    loop {
        if stopped.load(Ordering::Relaxed) {
            info!(
                src = %src_session,
                dst = %dst_session,
                track = %track_id,
                "bridge forwarder stopped"
            );
            break;
        }

        match src_rx.recv().await {
            Ok(pkt) => {
                let _ = dst_tx.send(pkt);
            }
            Err(broadcast::error::RecvError::Lagged(n)) => {
                tracing::warn!(
                    src = %src_session,
                    dst = %dst_session,
                    track = %track_id,
                    lagged = n,
                    "bridge forwarder lagged"
                );
            }
            Err(broadcast::error::RecvError::Closed) => {
                info!(
                    src = %src_session,
                    dst = %dst_session,
                    track = %track_id,
                    "bridge forwarder source closed"
                );
                break;
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use lm_core::{CodecType, EndpointId, SessionId, TrackId, TrackKind};

    #[test]
    fn test_bridge_manager_basic() {
        let mgr = BridgeManager::new();
        assert_eq!(mgr.bridge_count(), 0);
    }

    #[test]
    fn test_bridge_key() {
        let k1 = BridgeKey {
            session_a: "s1".into(),
            session_b: "s2".into(),
        };
        let k2 = BridgeKey {
            session_a: "s1".into(),
            session_b: "s2".into(),
        };
        assert_eq!(k1, k2);
    }
}
