//! lm-router — 媒体路由与转发
//!
//! 参考 webrtc-rs-sfu ForwardTable + rtpbridge 自动路由表：
//! - 使用 (publisher, track_id) 作为稳定转发键（ForwardKey），而非易变的 SSRC
//! - 运行时学习 SSRC → ForwardKey 绑定（支持重协商/simulcast）
//! - 支持 relay fast path（零拷贝转发，相同编解码）
//! - 自动清理不再需要的转发（retain 机制）

use std::collections::HashMap;
use std::sync::Arc;

use dashmap::DashMap;
use lm_core::{Backpressure, EgressSubscriber, TrackId};
use parking_lot::RwLock;
use tracing::debug;

// ============================================================================
// ForwardKey — 稳定的转发键（参考 webrtc-rs-sfu）
// ============================================================================

/// 转发键：使用 (publisher, track_id) 而非 SSRC
///
/// SSRC 可能在重协商时变化，但 publisher + track_id 是稳定的。
/// 运行时通过 `learn_ssrc` 建立 SSRC → ForwardKey 的映射。
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct ForwardKey {
    pub publisher: String,
    pub track_id: TrackId,
}

// ============================================================================
// ForwardTable — 转发表（参考 webrtc-rs-sfu ForwardTable）
// ============================================================================

/// SSRC → subscriber 路由表
///
/// 维护两层映射：
/// 1. ForwardKey → 订阅者列表（稳定路由）
/// 2. SSRC → ForwardKey（运行时学习，支持重协商）
pub struct ForwardTable {
    /// ForwardKey → 订阅者列表
    routes: DashMap<ForwardKey, Vec<Arc<dyn EgressSubscriber>>>,
    /// SSRC → ForwardKey（运行时学习）
    ssrc_index: RwLock<HashMap<u32, ForwardKey>>,
    /// TrackId → SSRC（反查）
    track_ssrc: DashMap<TrackId, u32>,
}

impl Default for ForwardTable {
    fn default() -> Self {
        Self::new()
    }
}

impl ForwardTable {
    pub fn new() -> Self {
        Self {
            routes: DashMap::new(),
            ssrc_index: RwLock::new(HashMap::new()),
            track_ssrc: DashMap::new(),
        }
    }

    /// 添加转发路由：ForwardKey → subscriber
    pub fn add_route(&self, key: ForwardKey, subscriber: Arc<dyn EgressSubscriber>) {
        self.routes.entry(key).or_default().push(subscriber);
    }

    /// 移除 ForwardKey 的所有路由
    pub fn remove_route(&self, key: &ForwardKey) {
        self.routes.remove(key);
    }

    /// 学习 SSRC → ForwardKey 绑定（运行时，参考 webrtc-rs-sfu ssrc_index）
    ///
    /// 当收到第一个 RTP 包或 SSRC 变化时调用。
    pub fn learn_ssrc(&self, ssrc: u32, key: ForwardKey) {
        debug!(ssrc, ?key, "learned ssrc binding");
        self.track_ssrc.insert(key.track_id.clone(), ssrc);
        self.ssrc_index.write().insert(ssrc, key);
    }

    /// 忘记 SSRC 绑定
    pub fn forget_ssrc(&self, ssrc: u32) {
        if let Some(key) = self.ssrc_index.write().remove(&ssrc) {
            self.track_ssrc.remove(&key.track_id);
            debug!(ssrc, ?key, "forgot ssrc binding");
        }
    }

    /// 通过 SSRC 查询订阅者列表
    pub fn subscribers_by_ssrc(&self, ssrc: u32) -> Option<Vec<Arc<dyn EgressSubscriber>>> {
        let key = self.ssrc_index.read().get(&ssrc).cloned()?;
        self.routes.get(&key).map(|v| v.clone())
    }

    /// 通过 ForwardKey 查询订阅者列表
    pub fn subscribers(&self, key: &ForwardKey) -> Option<Vec<Arc<dyn EgressSubscriber>>> {
        self.routes.get(key).map(|v| v.clone())
    }

    /// 绑定 TrackId → SSRC
    pub fn bind_track(&self, track_id: TrackId, ssrc: u32) {
        self.track_ssrc.insert(track_id, ssrc);
    }

    /// 查询 TrackId 对应的 SSRC
    pub fn ssrc_for_track(&self, track_id: &TrackId) -> Option<u32> {
        self.track_ssrc.get(track_id).map(|v| *v)
    }

    /// 清理不再需要的转发（参考 webrtc-rs-sfu retain 机制）
    ///
    /// 移除没有订阅者的路由。
    pub fn retain_active(&self) {
        let before = self.routes.len();
        self.routes.retain(|_, subs| !subs.is_empty());
        let removed = before - self.routes.len();
        if removed > 0 {
            debug!(removed, "retained active routes");
        }
    }

    /// 路由数量
    pub fn route_count(&self) -> usize {
        self.routes.len()
    }

    /// 已学习的 SSRC 数量
    pub fn ssrc_count(&self) -> usize {
        self.ssrc_index.read().len()
    }
}

// ============================================================================
// 路由计划
// ============================================================================

/// 路由计划（描述一帧应如何转发）
#[derive(Debug, Clone)]
pub struct RoutePlan {
    /// 源 SSRC
    pub src_ssrc: u32,
    /// 目标 subscriber 数量
    pub subscriber_count: usize,
    /// 是否走 relay fast path（零拷贝）
    pub relay_fast_path: bool,
}

// ============================================================================
// Relay fast path — 零拷贝转发
// ============================================================================

/// Relay fast path 转发
///
/// 当源与目标编解码一致时，直接转发 RTP payload，无需解码/重编码。
/// 参考 RustPBX RewriteRelay：只重写 SSRC/PT/seq/timestamp，payload 不变。
pub fn relay_fast_path(subscribers: &[Arc<dyn RelaySubscriber>], payload: &[u8]) -> RelayResult {
    let mut delivered = 0;
    let mut dropped = 0;

    for sub in subscribers {
        match sub.backpressure() {
            Backpressure::Block => {
                // 同步路径：直接调用
                // 注意：这里 payload 是原始 RTP payload，subscriber 需要自行打包
                sub.on_relay_payload(payload);
                delivered += 1;
            }
            Backpressure::DropOldest | Backpressure::DropNewest => {
                // 简化：直接丢弃
                dropped += 1;
            }
        }
    }

    RelayResult { delivered, dropped }
}

/// Relay 转发结果
#[derive(Debug, Clone, Copy)]
pub struct RelayResult {
    pub delivered: usize,
    pub dropped: usize,
}

// ============================================================================
// 扩展 EgressSubscriber trait（增加 relay payload 方法）
// ============================================================================

/// 支持 relay fast path 的订阅者
///
/// 在 lm-core 的 EgressSubscriber 基础上增加 relay payload 方法。
/// 实现者可以选择实现这个 trait 来获得零拷贝转发能力。
pub trait RelaySubscriber: EgressSubscriber {
    /// 收到 relay fast path 的原始 RTP payload
    fn on_relay_payload(&self, payload: &[u8]);
}

// ============================================================================
// 测试
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;
    use lm_core::AudioFrame;

    struct TestSubscriber {
        frames: parking_lot::Mutex<Vec<AudioFrame>>,
        payloads: parking_lot::Mutex<Vec<Vec<u8>>>,
    }

    impl EgressSubscriber for TestSubscriber {
        fn on_frame(&mut self, frame: &AudioFrame) {
            self.frames.lock().push(frame.clone());
        }

        fn backpressure(&self) -> Backpressure {
            Backpressure::Block
        }
    }

    impl RelaySubscriber for TestSubscriber {
        fn on_relay_payload(&self, payload: &[u8]) {
            self.payloads.lock().push(payload.to_vec());
        }
    }

    #[test]
    fn test_forward_table_add_remove() {
        let table = ForwardTable::new();
        let key = ForwardKey {
            publisher: "pub1".into(),
            track_id: TrackId("track1".into()),
        };
        let sub: Arc<dyn EgressSubscriber> = Arc::new(TestSubscriber {
            frames: parking_lot::Mutex::new(vec![]),
            payloads: parking_lot::Mutex::new(vec![]),
        });

        table.add_route(key.clone(), sub);
        assert_eq!(table.route_count(), 1);

        let subs = table.subscribers(&key).unwrap();
        assert_eq!(subs.len(), 1);

        table.remove_route(&key);
        assert_eq!(table.route_count(), 0);
    }

    #[test]
    fn test_ssrc_learning() {
        let table = ForwardTable::new();
        let key = ForwardKey {
            publisher: "pub1".into(),
            track_id: TrackId("track1".into()),
        };

        table.learn_ssrc(12345, key.clone());
        assert_eq!(table.ssrc_count(), 1);

        let learned_key = table.ssrc_index.read().get(&12345).cloned();
        assert_eq!(learned_key, Some(key));

        table.forget_ssrc(12345);
        assert_eq!(table.ssrc_count(), 0);
    }

    #[test]
    fn test_relay_fast_path() {
        let sub: Arc<TestSubscriber> = Arc::new(TestSubscriber {
            frames: parking_lot::Mutex::new(vec![]),
            payloads: parking_lot::Mutex::new(vec![]),
        });
        let subs: Vec<Arc<dyn RelaySubscriber>> = vec![sub.clone()];
        let payload = b"audio payload";

        let result = relay_fast_path(&subs, payload);
        assert_eq!(result.delivered, 1);
        assert_eq!(result.dropped, 0);

        // 直接通过原始 Arc 检查
        let received = sub.payloads.lock();
        assert_eq!(received.len(), 1);
        assert_eq!(received[0], payload);
    }

    #[test]
    fn test_retain_active() {
        let table = ForwardTable::new();
        let key = ForwardKey {
            publisher: "pub1".into(),
            track_id: TrackId("track1".into()),
        };

        // 添加再移除所有订阅者，留下空路由
        // （实际场景中通过 remove_route 清理，这里测试 retain）
        table.routes.insert(key.clone(), vec![]);
        assert_eq!(table.route_count(), 1);

        table.retain_active();
        assert_eq!(table.route_count(), 0);
    }
}
