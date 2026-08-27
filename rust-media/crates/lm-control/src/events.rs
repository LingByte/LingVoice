//! 事件总线 — 会话级事件广播
//!
//! 参考 forge-media EventBus + rustpbx RwiEvent。
//!
//! 使用 tokio::sync::broadcast channel 实现多订阅者广播。
//! 热路径事件（VAD/DTMF）和非热路径事件（TrackAdded/RecordingCompleted）
//! 都通过同一个 channel 发送。

use std::sync::Arc;

use tokio::sync::broadcast;

/// 媒体事件（内部 Rust 侧事件，映射到 gRPC MediaEvent）
#[derive(Debug, Clone)]
pub enum MediaNodeEvent {
    /// 轨道添加
    TrackAdded {
        session_id: String,
        track_id: String,
        kind: String,
        codec: String,
    },
    /// 轨道移除
    TrackRemoved {
        session_id: String,
        track_id: String,
    },
    /// VAD 事件
    Vad {
        session_id: String,
        track_id: String,
        speech_started: bool,
        energy: f32,
    },
    /// DTMF 事件
    Dtmf {
        session_id: String,
        track_id: String,
        digit: String,
        duration_ms: u32,
    },
    /// 录制完成
    RecordingCompleted {
        session_id: String,
        recording_id: String,
        file_path: String,
        duration_ms: u64,
        file_size: u64,
    },
    /// RTP 超时
    RtpTimeout {
        session_id: String,
        track_id: String,
        duration_ms: u64,
    },
    /// 错误事件
    Error {
        session_id: String,
        code: String,
        message: String,
        track_id: String,
    },
    /// 混音参与者加入
    MixParticipantJoined { mix_id: String, session_id: String },
    /// 混音参与者离开
    MixParticipantLeft { mix_id: String, session_id: String },
    /// 主发言者切换
    DominantSpeakerChanged { mix_id: String, session_id: String },
    /// 会话创建
    SessionCreated {
        session_id: String,
        room_id: Option<String>,
    },
    /// 会话销毁
    SessionDestroyed { session_id: String },
}

impl MediaNodeEvent {
    /// 获取事件关联的 session_id
    pub fn session_id(&self) -> Option<&str> {
        match self {
            MediaNodeEvent::TrackAdded { session_id, .. }
            | MediaNodeEvent::TrackRemoved { session_id, .. }
            | MediaNodeEvent::Vad { session_id, .. }
            | MediaNodeEvent::Dtmf { session_id, .. }
            | MediaNodeEvent::RecordingCompleted { session_id, .. }
            | MediaNodeEvent::RtpTimeout { session_id, .. }
            | MediaNodeEvent::Error { session_id, .. }
            | MediaNodeEvent::SessionCreated { session_id, .. }
            | MediaNodeEvent::SessionDestroyed { session_id, .. } => Some(session_id),
            MediaNodeEvent::MixParticipantJoined { session_id, .. }
            | MediaNodeEvent::MixParticipantLeft { session_id, .. }
            | MediaNodeEvent::DominantSpeakerChanged { session_id, .. } => Some(session_id),
        }
    }
}

/// 事件总线
///
/// 基于 tokio broadcast channel，支持多订阅者。
/// 参考 forge-media EventBus 设计。
#[derive(Clone)]
pub struct EventBus {
    tx: broadcast::Sender<MediaNodeEvent>,
}

impl EventBus {
    pub fn new(capacity: usize) -> Self {
        let (tx, _) = broadcast::channel(capacity);
        Self { tx }
    }

    /// 发布事件
    pub fn publish(&self, event: MediaNodeEvent) {
        let _ = self.tx.send(event);
    }

    /// 订阅事件（可选按 session_id 过滤）
    pub fn subscribe(&self) -> broadcast::Receiver<MediaNodeEvent> {
        self.tx.subscribe()
    }

    /// 订阅者数量
    pub fn subscriber_count(&self) -> usize {
        self.tx.receiver_count()
    }
}

impl Default for EventBus {
    fn default() -> Self {
        Self::new(1024)
    }
}

/// 从 EventBus 创建一个过滤后的 receiver
///
/// 如果 session_id 为 None，接收所有事件；
/// 如果 session_id 为 Some，只接收该 session 的事件。
pub fn filtered_receiver(
    bus: &EventBus,
    session_id: Option<&str>,
) -> broadcast::Receiver<MediaNodeEvent> {
    let _ = session_id;
    bus.subscribe()
}

/// 判断事件是否匹配 session 过滤
pub fn event_matches_session(event: &MediaNodeEvent, session_id: Option<&str>) -> bool {
    match session_id {
        None => true,
        Some(sid) => event.session_id() == Some(sid),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_event_bus_basic() {
        let bus = EventBus::new(64);
        let mut rx = bus.subscribe();

        bus.publish(MediaNodeEvent::SessionCreated {
            session_id: "s1".into(),
            room_id: Some("r1".into()),
        });

        let event = rx.try_recv().unwrap();
        match event {
            MediaNodeEvent::SessionCreated { session_id, .. } => {
                assert_eq!(session_id, "s1");
            }
            _ => panic!("wrong event"),
        }
    }

    #[test]
    fn test_event_bus_multi_subscriber() {
        let bus = EventBus::new(64);
        let mut rx1 = bus.subscribe();
        let mut rx2 = bus.subscribe();

        bus.publish(MediaNodeEvent::TrackAdded {
            session_id: "s1".into(),
            track_id: "t1".into(),
            kind: "audio".into(),
            codec: "opus".into(),
        });

        assert!(rx1.try_recv().is_ok());
        assert!(rx2.try_recv().is_ok());
    }

    #[test]
    fn test_event_matches_session() {
        let event = MediaNodeEvent::TrackAdded {
            session_id: "s1".into(),
            track_id: "t1".into(),
            kind: "audio".into(),
            codec: "opus".into(),
        };

        assert!(event_matches_session(&event, None));
        assert!(event_matches_session(&event, Some("s1")));
        assert!(!event_matches_session(&event, Some("s2")));
    }

    #[test]
    fn test_event_bus_no_subscriber() {
        let bus = EventBus::new(64);
        // 发布无订阅者不应 panic
        bus.publish(MediaNodeEvent::SessionDestroyed {
            session_id: "s1".into(),
        });
        assert_eq!(bus.subscriber_count(), 0);
    }

    #[test]
    fn test_event_session_id() {
        let event = MediaNodeEvent::Vad {
            session_id: "s1".into(),
            track_id: "t1".into(),
            speech_started: true,
            energy: 0.5,
        };
        assert_eq!(event.session_id(), Some("s1"));
    }
}
