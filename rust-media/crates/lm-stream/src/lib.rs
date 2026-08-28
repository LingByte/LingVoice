//! lm-stream — 媒体流抽象 + GOP 缓存 + Simulcast 路由
//!
//! 参考 Xiu StreamHub + atm0s MediaTrack。
//!
//! 核心抽象：
//! - `MediaStream`: 一个 publisher 的一个 track = 一个流
//! - `StreamSink`: 流订阅者（录制、转封装订阅源流；SFU 转发订阅转发流）
//! - `GopCache`: GOP 缓存，新订阅者快速首屏
//! - `StreamRegistry`: 流注册表，管理 room 内所有流

use std::collections::VecDeque;
use std::sync::Arc;

use lm_core::{CodecType, MediaFrame, StreamSink, TrackKind};
use lm_depacketizer::create_depacketizer;
use lm_transport::RtpPacket;
use parking_lot::RwLock;
use std::sync::atomic::{AtomicU64, Ordering};
use tracing::{debug, info};

// ============================================================================
// KeyframeRequester — RTCP PLI 关键帧请求回调
// ============================================================================

/// 关键帧请求回调 trait
///
/// 当 Rust 媒体层需要请求关键帧时（层切换、新订阅者、解码错误），
/// 通过此 trait 通知 Go 控制层发送 RTCP PLI。
///
/// 参考 xiu whip.rs 的 PLI 发送模式 + forge-media RtcpFeedbackHandler。
pub trait KeyframeRequester: Send + Sync {
    /// 请求指定 SSRC 的关键帧
    fn request_keyframe(&self, ssrc: u32);
}

/// 空实现（不请求关键帧）
pub struct NoopKeyframeRequester;
impl KeyframeRequester for NoopKeyframeRequester {
    fn request_keyframe(&self, _ssrc: u32) {}
}

// ============================================================================
// StreamId — 流标识
// ============================================================================

/// 流标识：session_id + track_id
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct StreamId {
    pub session_id: String,
    pub track_id: String,
}

impl StreamId {
    pub fn new(session_id: impl Into<String>, track_id: impl Into<String>) -> Self {
        Self {
            session_id: session_id.into(),
            track_id: track_id.into(),
        }
    }
}

// ============================================================================
// GopCache — GOP 缓存（参考 Xiu Gops）
// ============================================================================

/// GOP（一组从关键帧开始的帧序列）
struct Gop {
    frames: Vec<MediaFrame>,
}

impl Gop {
    fn new() -> Self {
        Self { frames: Vec::new() }
    }

    fn save(&mut self, frame: &MediaFrame) {
        self.frames.push(frame.clone());
    }

    fn frame_count(&self) -> usize {
        self.frames.len()
    }
}

/// GOP 缓存
///
/// 缓存最近 N 个 GOP，新订阅者先 replay 缓存数据，实现快速首屏。
/// 参考 Xiu 的 Gops 实现，包含音频流内存泄漏修复（帧数限制）。
pub struct GopCache {
    /// 缓存的 GOP 数量（通常 1-2）
    gop_count: usize,
    /// 单 GOP 最大帧数（防止纯音频流内存泄漏）
    max_frames_per_gop: usize,
    /// GOP 队列
    gops: VecDeque<Gop>,
}

impl GopCache {
    /// 创建 GOP 缓存
    ///
    /// - `gop_count`: 缓存的 GOP 数量（视频通常 1-2）
    /// - `max_frames_per_gop`: 单 GOP 最大帧数（防止纯音频流内存泄漏）
    pub fn new(gop_count: usize, max_frames_per_gop: usize) -> Self {
        Self {
            gop_count,
            max_frames_per_gop,
            gops: VecDeque::new(),
        }
    }

    /// 禁用缓存
    pub fn disabled() -> Self {
        Self::new(0, 0)
    }

    /// 是否启用缓存
    pub fn is_enabled(&self) -> bool {
        self.gop_count > 0
    }

    /// 保存帧
    ///
    /// 关键帧时新建 GOP，非关键帧追加到当前 GOP。
    /// 纯音频流（无关键帧）按帧数限制轮转。
    pub fn save(&mut self, frame: &MediaFrame) {
        if !self.is_enabled() {
            return;
        }

        // 视频关键帧：新建 GOP
        if frame.kind == TrackKind::Video && frame.keyframe {
            if self.gops.len() >= self.gop_count {
                self.gops.pop_front();
            }
            self.gops.push_back(Gop::new());
        }

        // 追加到当前 GOP
        if let Some(gop) = self.gops.back_mut() {
            // 帧数限制：即使没有关键帧也轮转（防止纯音频流内存泄漏）
            if gop.frame_count() >= self.max_frames_per_gop {
                if self.gops.len() >= self.gop_count {
                    self.gops.pop_front();
                }
                self.gops.push_back(Gop::new());
            }

            if let Some(gop) = self.gops.back_mut() {
                gop.save(frame);
            }
        } else {
            // 第一个帧不是关键帧，也创建 GOP
            let mut gop = Gop::new();
            gop.save(frame);
            self.gops.push_back(gop);
        }
    }

    /// 重放缓存给新订阅者
    pub fn replay(&self, sink: &dyn StreamSink) {
        if !self.is_enabled() {
            return;
        }
        for gop in &self.gops {
            for frame in &gop.frames {
                sink.on_frame(frame);
            }
        }
    }

    /// 清空缓存
    pub fn clear(&mut self) {
        self.gops.clear();
    }

    /// 缓存的帧数
    pub fn frame_count(&self) -> usize {
        self.gops.iter().map(|g| g.frame_count()).sum()
    }
}

// ============================================================================
// MediaStream — 媒体流（参考 Xiu Stream + atm0s MediaTrack）
// ============================================================================

/// 媒体流
///
/// 一个 publisher 的一个 track = 一个流。
/// 职责：
/// 1. 接收 RTP 包，用 Depacketizer 组装为 MediaFrame
/// 2. 将 MediaFrame 分发给**源流订阅者**（录制、转封装）— 收到的是完整帧
/// 3. 将 RTP 包分发给**转发流订阅者**（SFU 转发）— 收到的是裸包
/// 4. 缓存 GOP，新订阅者快速首屏
pub struct MediaStream {
    /// 流标识
    pub id: StreamId,
    /// 所属 room
    pub room_id: Option<String>,
    /// 编解码
    pub codec: CodecType,
    /// 轨道类型
    pub kind: TrackKind,
    /// SSRC（主层）
    pub ssrc: u32,
    /// 时钟率
    pub clock_rate: u32,

    /// 源流订阅者（录制、转封装 — 收到 MediaFrame）
    source_sinks: RwLock<Vec<Arc<dyn StreamSink>>>,

    /// GOP 缓存
    gop_cache: RwLock<GopCache>,

    /// RTP 解包器
    depacketizer: RwLock<Box<dyn lm_core::Depacketizer>>,
}

impl MediaStream {
    /// 创建媒体流
    pub fn new(
        id: StreamId,
        room_id: Option<String>,
        codec: CodecType,
        kind: TrackKind,
        ssrc: u32,
        clock_rate: u32,
    ) -> Self {
        let depacketizer = create_depacketizer(codec);

        // 视频流启用 GOP 缓存，音频流禁用
        let gop_cache = if kind == TrackKind::Video {
            GopCache::new(1, 2000) // 缓存 1 个 GOP，最多 2000 帧
        } else {
            GopCache::disabled()
        };

        Self {
            id,
            room_id,
            codec,
            kind,
            ssrc,
            clock_rate,
            source_sinks: RwLock::new(Vec::new()),
            gop_cache: RwLock::new(gop_cache),
            depacketizer: RwLock::new(depacketizer),
        }
    }

    /// 添加源流订阅者（录制、转封装）
    ///
    /// 订阅后会先 replay GOP 缓存（如果是视频流），然后接收新的帧。
    pub fn add_source_sink(&self, sink: Arc<dyn StreamSink>) {
        // 先 replay GOP 缓存
        {
            let cache = self.gop_cache.read();
            cache.replay(sink.as_ref());
        }
        let mut sinks = self.source_sinks.write();
        sinks.push(sink);
        info!(
            stream = ?self.id,
            sink_count = sinks.len(),
            "source sink added"
        );
    }

    /// 移除源流订阅者
    pub fn remove_source_sink(&self, sink: &Arc<dyn StreamSink>) {
        let mut sinks = self.source_sinks.write();
        sinks.retain(|s| !Arc::ptr_eq(s, sink));
    }

    /// 推入 RTP 包
    ///
    /// 1. 用 Depacketizer 组装帧
    /// 2. 帧完成后分发给源流订阅者
    /// 3. 存入 GOP 缓存
    ///
    /// 返回 true 表示组装出了一个完整帧
    pub fn push_packet(&self, pkt: &RtpPacket) -> bool {
        let mut dep = self.depacketizer.write();
        let result = dep.push_packet(
            &pkt.payload,
            pkt.marker,
            pkt.sequence_number as u16,
            pkt.timestamp,
        );

        let frame_complete = matches!(result, lm_core::DepacketizeResult::FrameComplete);

        if frame_complete {
            if let Some(mut frame) = dep.take_frame() {
                // 设置 SSRC
                frame.ssrc = pkt.ssrc;
                frame.rid = pkt.rid.clone();

                // 存入 GOP 缓存
                {
                    let mut cache = self.gop_cache.write();
                    cache.save(&frame);
                }

                // 分发给源流订阅者
                let sinks = self.source_sinks.read();
                for sink in sinks.iter() {
                    sink.on_frame(&frame);
                }
                return true;
            }
        }

        false
    }

    /// 获取源流订阅者数量
    pub fn source_sink_count(&self) -> usize {
        self.source_sinks.read().len()
    }

    /// 获取 GOP 缓存帧数
    pub fn gop_frame_count(&self) -> usize {
        self.gop_cache.read().frame_count()
    }

    /// 重置解包器
    pub fn reset(&self) {
        self.depacketizer.write().reset();
        self.gop_cache.write().clear();
    }
}

// ============================================================================
// StreamRegistry — 流注册表
// ============================================================================

/// 流注册表
///
/// 管理一个 media node 上所有流。
/// 支持按 room 查询流（用于 SFU 转发时查找 peer tracks）。
pub struct StreamRegistry {
    /// StreamId → Arc<MediaStream>
    streams: dashmap::DashMap<StreamId, Arc<MediaStream>>,
    /// room_id → Vec<StreamId>
    room_streams: dashmap::DashMap<String, Vec<StreamId>>,
}

impl Default for StreamRegistry {
    fn default() -> Self {
        Self::new()
    }
}

impl StreamRegistry {
    pub fn new() -> Self {
        Self {
            streams: dashmap::DashMap::new(),
            room_streams: dashmap::DashMap::new(),
        }
    }

    /// 注册流
    pub fn register(&self, stream: Arc<MediaStream>) {
        let id = stream.id.clone();
        let room_id = stream.room_id.clone();

        self.streams.insert(id.clone(), stream);

        if let Some(room) = &room_id {
            let mut entry = self.room_streams.entry(room.clone()).or_default();
            entry.push(id);
        }
    }

    /// 注销流
    pub fn unregister(&self, id: &StreamId) {
        if let Some((_, stream)) = self.streams.remove(id) {
            if let Some(room) = &stream.room_id {
                if let Some(mut entry) = self.room_streams.get_mut(room) {
                    entry.retain(|s| s != id);
                }
            }
        }
    }

    /// 按 session 批量注销
    ///
    /// 销毁 session 时调用，清理该 session 的所有流。
    pub fn unregister_session(&self, session_id: &str) {
        let to_remove: Vec<StreamId> = self
            .streams
            .iter()
            .filter(|r| r.key().session_id == session_id)
            .map(|r| r.key().clone())
            .collect();

        for id in to_remove {
            self.unregister(&id);
        }
    }

    /// 获取流
    pub fn get(&self, id: &StreamId) -> Option<Arc<MediaStream>> {
        self.streams.get(id).map(|r| r.clone())
    }

    /// 获取 room 内同 kind 的其他流（排除指定 session）
    ///
    /// 用于 SFU 转发：A push RTP 时，查找同 room 其他 session 的流
    pub fn get_room_peer_streams(
        &self,
        room_id: &str,
        exclude_session: &str,
        kind: TrackKind,
    ) -> Vec<Arc<MediaStream>> {
        let mut result = Vec::new();

        if let Some(ids) = self.room_streams.get(room_id) {
            for id in ids.iter() {
                if id.session_id == exclude_session {
                    continue;
                }
                if let Some(stream) = self.streams.get(id) {
                    if stream.kind == kind {
                        result.push(stream.clone());
                    }
                }
            }
        }

        result
    }

    /// 获取 room 内所有流
    pub fn get_room_streams(&self, room_id: &str) -> Vec<Arc<MediaStream>> {
        let mut result = Vec::new();
        if let Some(ids) = self.room_streams.get(room_id) {
            for id in ids.iter() {
                if let Some(stream) = self.streams.get(id) {
                    result.push(stream.clone());
                }
            }
        }
        result
    }

    /// 流数量
    pub fn count(&self) -> usize {
        self.streams.len()
    }

    /// room 数量
    pub fn room_count(&self) -> usize {
        self.room_streams.len()
    }
}

// ============================================================================
// Phase 4: Simulcast — RID 路由 + 层选择 + Dynacast
// ============================================================================

/// Simulcast 层级
///
/// 参考 WebRTC simulcast RID 约定：
/// - "low": 1/4 分辨率（如 320x180）
/// - "mid": 1/2 分辨率（如 640x360）
/// - "high": 原始分辨率（如 1280x720）
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum SimulcastTier {
    Low,
    Mid,
    High,
}

impl SimulcastTier {
    /// 从 RID 字符串解析
    pub fn from_rid(rid: &str) -> Option<Self> {
        match rid.to_lowercase().as_str() {
            "low" | "l" | "quarter" => Some(SimulcastTier::Low),
            "mid" | "m" | "half" => Some(SimulcastTier::Mid),
            "high" | "h" | "full" => Some(SimulcastTier::High),
            _ => None,
        }
    }

    /// 转为 RID 字符串
    pub fn to_rid(&self) -> &'static str {
        match self {
            SimulcastTier::Low => "low",
            SimulcastTier::Mid => "mid",
            SimulcastTier::High => "high",
        }
    }

    /// 优先级数值（用于排序，越大越优先）
    pub fn priority(&self) -> u8 {
        match self {
            SimulcastTier::Low => 0,
            SimulcastTier::Mid => 1,
            SimulcastTier::High => 2,
        }
    }
}

/// Simulcast 层元数据
#[derive(Debug, Clone)]
pub struct LayerMeta {
    pub layer: SimulcastTier,
    pub ssrc: u32,
    pub rid: String,
    /// 估计码率（bps），由 publisher 在 signaling 中声明或由 SFU 测量
    pub bitrate: u32,
    /// 目标码率（kbps），用于自适应层选择
    pub target_bitrate_kbps: u64,
    /// 分辨率
    pub width: u16,
    pub height: u16,
    /// 帧率
    pub fps: u8,
}

impl Default for LayerMeta {
    fn default() -> Self {
        Self {
            layer: SimulcastTier::Mid,
            ssrc: 0,
            rid: String::new(),
            bitrate: 0,
            target_bitrate_kbps: 0,
            width: 0,
            height: 0,
            fps: 0,
        }
    }
}

/// 订阅者层选择策略
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LayerSelectionPolicy {
    /// 固定选择指定层
    Fixed(SimulcastTier),
    /// 根据订阅者带宽自适应（暂用固定层降级）
    Adaptive,
    /// 总是选最高层
    Highest,
    /// 总是选最低层（省带宽）
    Lowest,
}

/// Simulcast 流
///
/// 一个 publisher 的一个视频 track 有多个 simulcast 层。
/// 每个 layer 有独立的 SSRC 和 RID。
///
/// 职责：
/// 1. 按 RID 路由 RTP 包到对应层的 MediaStream
/// 2. 维护各层元数据
/// 3. 为每个订阅者按 LayerSelectionPolicy 选择层
/// 4. 支持层切换（切换时请求关键帧）
/// 5. Dynacast：根据订阅者需求启用/禁用 publisher 层
pub struct SimulcastStreamV1 {
    /// 流标识（不含 layer）
    pub id: StreamId,
    pub room_id: Option<String>,
    pub codec: CodecType,

    /// 各层的 MediaStream
    layers: RwLock<std::collections::HashMap<SimulcastTier, Arc<MediaStream>>>,

    /// 各层元数据
    layer_metas: RwLock<std::collections::HashMap<SimulcastTier, LayerMeta>>,

    /// 订阅者 → 选层策略 + 估计带宽
    subscribers: RwLock<Vec<SubscriberEntry>>,

    /// 当前各层是否启用（Dynacast）
    layer_enabled: RwLock<std::collections::HashMap<SimulcastTier, bool>>,

    /// 关键帧请求器（RTCP PLI）
    keyframe_requester: RwLock<Option<Arc<dyn KeyframeRequester>>>,
}

/// 订阅者条目（sink + 策略 + 估计带宽 kbps）
struct SubscriberEntry {
    sink: Arc<dyn StreamSink>,
    policy: LayerSelectionPolicy,
    /// 订阅者估计可用带宽（kbps），0 = 未知
    estimated_bandwidth_kbps: std::sync::atomic::AtomicU64,
    /// 当前订阅的层 (用于检测层切换)
    current_tier: std::sync::Mutex<SimulcastTier>,
}

impl SimulcastStreamV1 {
    /// 创建 Simulcast 流
    pub fn new(id: StreamId, room_id: Option<String>, codec: CodecType, clock_rate: u32) -> Self {
        let mut layer_enabled = std::collections::HashMap::new();
        layer_enabled.insert(SimulcastTier::Low, true);
        layer_enabled.insert(SimulcastTier::Mid, true);
        layer_enabled.insert(SimulcastTier::High, true);

        Self {
            id,
            room_id,
            codec,
            layers: RwLock::new(std::collections::HashMap::new()),
            layer_metas: RwLock::new(std::collections::HashMap::new()),
            subscribers: RwLock::new(Vec::new()),
            layer_enabled: RwLock::new(layer_enabled),
            keyframe_requester: RwLock::new(None),
        }
    }

    /// 设置关键帧请求器（由 lm-control 在 add_track 时注入）
    pub fn set_keyframe_requester(&self, requester: Arc<dyn KeyframeRequester>) {
        *self.keyframe_requester.write() = Some(requester);
    }

    /// 内部：请求关键帧
    fn send_pli(&self, layer: SimulcastTier) {
        let ssrc = self
            .layer_metas
            .read()
            .get(&layer)
            .map(|m| m.ssrc)
            .unwrap_or(0);
        if let Some(req) = self.keyframe_requester.read().as_ref() {
            req.request_keyframe(ssrc);
            info!(
                stream = ?self.id,
                layer = layer.to_rid(),
                ssrc,
                "PLI sent via keyframe requester"
            );
        } else {
            debug!(
                stream = ?self.id,
                layer = layer.to_rid(),
                "keyframe requested but no requester set"
            );
        }
    }

    /// 注册一个 simulcast 层
    pub fn add_layer(&self, layer: SimulcastTier, meta: LayerMeta, clock_rate: u32) {
        let stream = Arc::new(MediaStream::new(
            StreamId::new(
                format!("{}#{}", self.id.session_id, self.id.track_id),
                layer.to_rid(),
            ),
            self.room_id.clone(),
            self.codec,
            TrackKind::Video,
            meta.ssrc,
            clock_rate,
        ));

        {
            let mut layers = self.layers.write();
            layers.insert(layer, stream);
        }
        {
            let mut metas = self.layer_metas.write();
            metas.insert(layer, meta);
        }

        info!(
            stream = ?self.id,
            layer = layer.to_rid(),
            ssrc = ?self.layer_metas.read().get(&layer).map(|m| m.ssrc),
            "simulcast layer added"
        );
    }

    /// 按 RID 路由 RTP 包到对应层
    ///
    /// 返回 true 如果某层组装出了完整帧
    pub fn push_packet(&self, pkt: &RtpPacket) -> bool {
        // 通过 RID 判断层
        let layer = if !pkt.rid.is_empty() {
            SimulcastTier::from_rid(&pkt.rid)
        } else {
            // 无 RID 时通过 SSRC 匹配
            let metas = self.layer_metas.read();
            metas
                .iter()
                .find(|(_, m)| m.ssrc == pkt.ssrc)
                .map(|(l, _)| *l)
        };

        let layer = match layer {
            Some(l) => l,
            None => return false,
        };

        // 检查层是否启用（Dynacast）
        {
            let enabled = self.layer_enabled.read();
            if !*enabled.get(&layer).unwrap_or(&true) {
                return false; // 层被禁用，丢弃
            }
        }

        // 路由到对应层的 MediaStream
        let layers = self.layers.read();
        if let Some(stream) = layers.get(&layer) {
            let frame_completed = stream.push_packet(pkt);

            // 如果帧完成，分发给选择此层的订阅者
            if frame_completed {
                self.dispatch_to_subscribers(layer);
            }
            true
        } else {
            false
        }
    }

    /// 分发帧给选择指定层的订阅者
    fn dispatch_to_subscribers(&self, layer: SimulcastTier) {
        // 从对应层的 MediaStream 获取最新帧
        // MediaStream 的帧已通过 source_sinks 分发，这里处理 simulcast 订阅者
        let layers = self.layers.read();
        if let Some(stream) = layers.get(&layer) {
            // MediaStream 的 GOP cache 中有最新帧
            // 这里简化处理：simulcast 订阅者直接订阅对应层的 MediaStream
            // 实际实现中，订阅者应该在 add_subscriber 时就订阅到对应层
            let _ = stream;
        }

        // 更新层启用状态（Dynacast）
        self.update_dynacast();
    }

    /// 添加订阅者
    ///
    /// 订阅者会根据 LayerSelectionPolicy 被路由到对应层
    pub fn add_subscriber(&self, sink: Arc<dyn StreamSink>, policy: LayerSelectionPolicy) {
        let layer = self.select_layer_for_policy(&policy, 0);

        // 订阅到对应层的 MediaStream
        let layers = self.layers.read();
        if let Some(stream) = layers.get(&layer) {
            stream.add_source_sink(sink.clone());
        }

        let mut subs = self.subscribers.write();
        subs.push(SubscriberEntry {
            sink,
            policy,
            estimated_bandwidth_kbps: AtomicU64::new(0),
            current_tier: std::sync::Mutex::new(layer),
        });

        info!(
            stream = ?self.id,
            layer = layer.to_rid(),
            subscriber_count = subs.len(),
            "simulcast subscriber added"
        );

        // 新订阅者加入时请求关键帧
        drop(subs);
        self.send_pli(layer);
        self.update_dynacast();
    }

    /// 移除订阅者
    pub fn remove_subscriber(&self, sink: &Arc<dyn StreamSink>) {
        let mut subs = self.subscribers.write();
        subs.retain(|e| !Arc::ptr_eq(&e.sink, sink));
        drop(subs);
        self.update_dynacast();
    }

    /// 更新订阅者的估计带宽（由 RTCP RR 或 TWCC 驱动）
    ///
    /// 当带宽估计变化时，自适应策略可能触发层切换。
    pub fn update_subscriber_bandwidth(&self, sink: &Arc<dyn StreamSink>, bandwidth_kbps: u64) {
        let subs = self.subscribers.read();
        for entry in subs.iter() {
            if Arc::ptr_eq(&entry.sink, sink) {
                entry
                    .estimated_bandwidth_kbps
                    .store(bandwidth_kbps, Ordering::Relaxed);

                // 如果是自适应策略，检查是否需要切换层
                if entry.policy == LayerSelectionPolicy::Adaptive {
                    let new_layer = self.select_layer_for_policy(&entry.policy, bandwidth_kbps);
                    let current_layer = *entry.current_tier.lock().unwrap();
                    if new_layer != current_layer {
                        info!(
                            stream = ?self.id,
                            from = current_layer.to_rid(),
                            to = new_layer.to_rid(),
                            bandwidth_kbps,
                            "adaptive layer switch triggered"
                        );
                        // 更新当前层
                        *entry.current_tier.lock().unwrap() = new_layer;
                        // 切换层时请求关键帧
                        self.send_pli(new_layer);
                    }
                }
                break;
            }
        }
    }

    /// 根据策略和带宽选择层
    ///
    /// 参考 atm0s-media-server select_layer 算法：
    /// 基于目标码率选择满足条件的最高质量层。
    ///
    /// 自适应策略 (Adaptive):
    /// - 带宽未知 (0) 时选最高可用层
    /// - 从高到低尝试，选择带宽能覆盖的第一个层
    /// - 无层元数据成本信息时也选最高可用层
    fn select_layer_for_policy(
        &self,
        policy: &LayerSelectionPolicy,
        bandwidth_kbps: u64,
    ) -> SimulcastTier {
        match policy {
            LayerSelectionPolicy::Fixed(layer) => *layer,
            LayerSelectionPolicy::Highest => {
                let layers = self.layers.read();
                if layers.contains_key(&SimulcastTier::High) {
                    SimulcastTier::High
                } else if layers.contains_key(&SimulcastTier::Mid) {
                    SimulcastTier::Mid
                } else {
                    SimulcastTier::Low
                }
            }
            LayerSelectionPolicy::Lowest => SimulcastTier::Low,
            LayerSelectionPolicy::Adaptive => {
                let metas = self.layer_metas.read();

                // 从高到低尝试，选择带宽能覆盖的最高层
                for &tier in &[SimulcastTier::High, SimulcastTier::Mid, SimulcastTier::Low] {
                    if let Some(meta) = metas.get(&tier) {
                        if bandwidth_kbps == 0 || meta.target_bitrate_kbps == 0 {
                            // 带宽未知或无成本信息时选最高可用层
                            return tier;
                        }
                        if bandwidth_kbps >= meta.target_bitrate_kbps {
                            return tier;
                        }
                    }
                }
                SimulcastTier::Low
            }
        }
    }

    /// Dynacast 自适应层切换算法
    ///
    /// 根据所有订阅者的总带宽需求, 决定 publisher 应该发送哪些层:
    /// - 如果所有订阅者只需要 Low 层, 禁用 Mid/High 层 (省带宽)
    /// - 如果有订阅者需要 High 层, 启用 High 层
    /// - 层切换时请求关键帧
    ///
    /// 算法:
    /// 1. 计算每个层的总需求带宽 = sum(订阅者估计带宽 for 订阅了此层的订阅者)
    /// 2. 计算每个层的编码成本 (从 layer_metas 获取 target_bitrate_kbps)
    /// 3. 启用层条件: 至少有一个订阅者需要此层 且 (总需求带宽 > 编码成本 * 0.5 或 带宽未知)
    /// 4. 禁用层条件: 没有订阅者需要此层, 或总需求带宽 < 编码成本 * 0.5 (且带宽已知)
    /// 5. 层状态变化时请求关键帧
    fn update_dynacast(&self) {
        let subscribers = self.subscribers.read();
        let metas = self.layer_metas.read();

        // 计算每层的总需求带宽和订阅者数量
        let mut demand: std::collections::HashMap<SimulcastTier, u64> =
            std::collections::HashMap::new();
        let mut count: std::collections::HashMap<SimulcastTier, usize> =
            std::collections::HashMap::new();
        for sub in subscribers.iter() {
            let bw = sub.estimated_bandwidth_kbps.load(Ordering::Relaxed);
            let layer = self.select_layer_for_policy(&sub.policy, bw);
            *demand.entry(layer).or_insert(0) += bw;
            *count.entry(layer).or_insert(0) += 1;
        }

        // 决定每层是否启用
        let mut changed = false;
        let mut enabled = self.layer_enabled.write();
        for &tier in &[SimulcastTier::Low, SimulcastTier::Mid, SimulcastTier::High] {
            let demand_bw = demand.get(&tier).copied().unwrap_or(0);
            let subscriber_count = count.get(&tier).copied().unwrap_or(0);
            let cost_bw = metas.get(&tier).map(|m| m.target_bitrate_kbps).unwrap_or(0);
            let was_enabled = *enabled.get(&tier).unwrap_or(&true);

            let should_enable = if subscriber_count == 0 {
                false // 没人需要
            } else if demand_bw == 0 || cost_bw == 0 {
                // 带宽未知或无成本信息, 启用
                true
            } else {
                demand_bw > cost_bw / 2 // 需求 > 成本的一半
            };

            if was_enabled != should_enable {
                changed = true;
                enabled.insert(tier, should_enable);
                if should_enable {
                    info!(
                        stream = ?self.id,
                        layer = tier.to_rid(),
                        "dynacast: enabling needed layer"
                    );
                } else {
                    info!(
                        stream = ?self.id,
                        layer = tier.to_rid(),
                        "dynacast: disabling unused layer"
                    );
                }
            }
        }

        // 层状态变化时请求关键帧
        if changed {
            for (&tier, &is_enabled) in enabled.iter() {
                if is_enabled {
                    self.send_pli(tier);
                }
            }
        }
    }

    /// 切换订阅者的层
    ///
    /// 切换时请求关键帧（通过 RTCP PLI），确保新层可正确解码。
    pub fn switch_layer(&self, sink: &Arc<dyn StreamSink>, new_layer: SimulcastTier) -> bool {
        let mut subs = self.subscribers.write();

        let mut found = false;
        for entry in subs.iter_mut() {
            if Arc::ptr_eq(&entry.sink, sink) {
                entry.policy = LayerSelectionPolicy::Fixed(new_layer);
                found = true;
                break;
            }
        }

        if found {
            info!(
                stream = ?self.id,
                new_layer = new_layer.to_rid(),
                "simulcast subscriber switched layer"
            );
            // 切换层时请求关键帧
            self.send_pli(new_layer);
        }

        drop(subs);
        if found {
            self.update_dynacast();
        }
        found
    }

    /// 请求关键帧
    ///
    /// 通过 RTCP PLI 请求 publisher 发送关键帧。
    /// 用于层切换、新订阅者加入等场景。
    pub fn request_keyframe(&self, layer: SimulcastTier) {
        info!(
            stream = ?self.id,
            layer = layer.to_rid(),
            "requesting keyframe (PLI)"
        );
        self.send_pli(layer);
    }

    /// 获取层元数据
    pub fn get_layer_meta(&self, layer: SimulcastTier) -> Option<LayerMeta> {
        self.layer_metas.read().get(&layer).cloned()
    }

    /// 获取所有层
    pub fn layers(&self) -> Vec<SimulcastTier> {
        self.layers.read().keys().copied().collect()
    }

    /// 获取订阅者数量
    pub fn subscriber_count(&self) -> usize {
        self.subscribers.read().len()
    }

    /// 检查层是否启用
    pub fn is_layer_enabled(&self, layer: SimulcastTier) -> bool {
        *self.layer_enabled.read().get(&layer).unwrap_or(&true)
    }
}

// ============================================================================
// Phase 5: SVC (Scalable Video Coding) — 单流多层
// ============================================================================

/// SVC 空间层 (分辨率)
///
/// 与 Simulcast 不同, SVC 在单个流中通过编码器分层实现多分辨率:
/// - Base: 基础层 (最低分辨率, e.g. 180p), SL=0
/// - Middle: 中间层 (e.g. 360p), SL=1
/// - High: 增强层 (最高分辨率, e.g. 720p), SL=2
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum SvcSpatialLayer {
    /// 基础层 (最低分辨率, e.g. 180p), SL=0
    Base,
    /// 中间层 (e.g. 360p), SL=1
    Middle,
    /// 增强层 (最高分辨率, e.g. 720p), SL=2
    High,
}

impl SvcSpatialLayer {
    /// 从索引值创建 (0=Base, 1=Middle, 2=High)
    pub fn from_idx(idx: u8) -> Self {
        match idx {
            0 => SvcSpatialLayer::Base,
            1 => SvcSpatialLayer::Middle,
            _ => SvcSpatialLayer::High,
        }
    }

    /// 获取索引值
    pub fn idx(&self) -> u8 {
        match self {
            SvcSpatialLayer::Base => 0,
            SvcSpatialLayer::Middle => 1,
            SvcSpatialLayer::High => 2,
        }
    }
}

/// SVC 时间层 (帧率)
///
/// 通过 temporal ID 区分不同帧率的层:
/// - Base: 基础帧率 (e.g. 7.5fps), TL=0
/// - Middle: 中间帧率 (e.g. 15fps), TL=1
/// - Full: 全帧率 (e.g. 30fps), TL=2
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum SvcTemporalLayer {
    /// 基础帧率 (e.g. 7.5fps), TL=0
    Base,
    /// 中间帧率 (e.g. 15fps), TL=1
    Middle,
    /// 全帧率 (e.g. 30fps), TL=2
    Full,
}

impl SvcTemporalLayer {
    /// 从索引值创建 (0=Base, 1=Middle, 2=Full)
    pub fn from_idx(idx: u8) -> Self {
        match idx {
            0 => SvcTemporalLayer::Base,
            1 => SvcTemporalLayer::Middle,
            _ => SvcTemporalLayer::Full,
        }
    }

    /// 获取索引值
    pub fn idx(&self) -> u8 {
        match self {
            SvcTemporalLayer::Base => 0,
            SvcTemporalLayer::Middle => 1,
            SvcTemporalLayer::Full => 2,
        }
    }
}

/// SVC 层标识 (空间 + 时间)
///
/// 唯一标识 SVC 流中的一个层组合。
/// 例如 SvcLayerId { spatial: Middle, temporal: Full } 表示中间分辨率 + 全帧率。
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct SvcLayerId {
    /// 空间层 (分辨率)
    pub spatial: SvcSpatialLayer,
    /// 时间层 (帧率)
    pub temporal: SvcTemporalLayer,
}

impl SvcLayerId {
    /// 从空间和时间索引创建
    pub fn from_spatial_temporal(s: u8, t: u8) -> Self {
        Self {
            spatial: SvcSpatialLayer::from_idx(s),
            temporal: SvcTemporalLayer::from_idx(t),
        }
    }

    /// 获取空间层索引
    pub fn spatial_idx(&self) -> u8 {
        self.spatial.idx()
    }

    /// 获取时间层索引
    pub fn temporal_idx(&self) -> u8 {
        self.temporal.idx()
    }
}

/// SVC 订阅者条目
struct SvcSubscriberEntry {
    sink: Arc<dyn StreamSink>,
    /// 目标空间层
    target_spatial: SvcSpatialLayer,
    /// 目标时间层
    target_temporal: SvcTemporalLayer,
    /// 估计带宽 (kbps)
    estimated_bandwidth_kbps: AtomicU64,
}

/// SVC 可扩展视频流
///
/// 与 SimulcastStreamV1 不同:
/// - SVC 使用单个 SSRC, 通过 RTP header extension 或 codec 特定标识区分层
/// - 空间层: 不同分辨率, 通过 scalability mode (L1T3, L3T3, S2T1 等) 配置
/// - 时间层: 不同帧率, 通过 temporal ID 区分
/// - 订阅者可以只订阅需要的空间+时间层组合
///
/// 职责:
/// 1. 从 RTP 包中提取 SVC 层标识 (VP8/VP9/H.264 SVC/AV1)
/// 2. 缓存各层的帧
/// 3. 为每个订阅者按目标层过滤分发
/// 4. 支持动态层切换 (update_subscriber_layer)
pub struct SvcStream {
    /// 流标识
    pub id: StreamId,
    pub room_id: Option<String>,
    pub codec: CodecType,

    /// SVC 层配置 (e.g. L3T3 = 3空间层 x 3时间层)
    spatial_layers: u8,
    temporal_layers: u8,

    /// 当前 SSRC (SVC 单 SSRC)
    ssrc: u32,

    /// 各层的帧缓冲
    layer_frames: RwLock<std::collections::HashMap<SvcLayerId, Vec<MediaFrame>>>,

    /// 帧组装缓冲: 累积非 marker 包的 payload, marker=1 时组装完整帧
    packet_buffer: std::sync::Mutex<Vec<u8>>,

    /// 订阅者 → SVC 层选择
    subscribers: RwLock<Vec<SvcSubscriberEntry>>,

    /// 关键帧请求器
    keyframe_requester: RwLock<Option<Arc<dyn KeyframeRequester>>>,
}

impl SvcStream {
    /// 创建 SVC 流
    ///
    /// - `spatial_layers`: 空间层数量 (1-3)
    /// - `temporal_layers`: 时间层数量 (1-3)
    pub fn new(
        id: StreamId,
        room_id: Option<String>,
        codec: CodecType,
        ssrc: u32,
        spatial_layers: u8,
        temporal_layers: u8,
    ) -> Self {
        Self {
            id,
            room_id,
            codec,
            spatial_layers,
            temporal_layers,
            ssrc,
            layer_frames: RwLock::new(std::collections::HashMap::new()),
            packet_buffer: std::sync::Mutex::new(Vec::new()),
            subscribers: RwLock::new(Vec::new()),
            keyframe_requester: RwLock::new(None),
        }
    }

    /// 设置关键帧请求器
    pub fn set_keyframe_requester(&self, requester: Arc<dyn KeyframeRequester>) {
        *self.keyframe_requester.write() = Some(requester);
    }

    /// 请求关键帧
    fn send_pli(&self) {
        if let Some(req) = self.keyframe_requester.read().as_ref() {
            req.request_keyframe(self.ssrc);
            info!(
                stream = ?self.id,
                ssrc = self.ssrc,
                "SVC PLI sent via keyframe requester"
            );
        } else {
            debug!(
                stream = ?self.id,
                ssrc = self.ssrc,
                "SVC keyframe requested but no requester set"
            );
        }
    }

    /// 从 RTP 包中提取 SVC 层标识
    ///
    /// 不同编解码器的层标识提取方式:
    /// - VP8: payload header 中的 temporal_idx (PictureID 后的 TL0PICIDX)
    /// - VP9: payload header 中的 spatial_idx + temporal_idx (flexible mode / structure header)
    /// - H.264/SVC: NALU header 中的 dependency_id + quality_id
    /// - AV1: OBU header 中的 spatial_id + temporal_id
    ///
    /// 空间层在 SVC 单 SSRC 模式下通常为 0 (L1T3) 或由编解码器头部指定。
    fn extract_layer_id(&self, pkt: &RtpPacket) -> SvcLayerId {
        let payload = &pkt.payload;

        let (spatial_idx, temporal_idx) = match self.codec {
            CodecType::Vp8 => {
                // VP8 RTP payload descriptor (RFC 7741):
                // Byte 0: X R N S PartID
                // 如果 X=1, 下一个字节是扩展标志
                // 如果 T=1, 后面有 TL0PICIDX (1 byte) = temporal layer index
                let mut temporal = 0u8;
                if !payload.is_empty() {
                    let first = payload[0];
                    let x_bit = (first >> 7) & 0x01;
                    let mut offset = 1;
                    if x_bit == 1 && offset < payload.len() {
                        let ext = payload[offset];
                        let t_bit = (ext >> 5) & 0x01; // T bit
                        offset += 1;
                        // 跳过 M, L, K bits 对应的字节
                        if (ext >> 4) & 0x01 == 1 && offset < payload.len() {
                            // M bit (PictureID)
                            let pic_id = payload[offset];
                            offset += 1;
                            if pic_id & 0x80 != 0 && offset < payload.len() {
                                // 16-bit PictureID
                                offset += 1;
                            }
                        }
                        if t_bit == 1 && offset < payload.len() {
                            temporal = payload[offset];
                        }
                    }
                }
                // VP8 simulcast/SVC: 空间层由 SSRC 区分, 单 SSRC SVC 时 spatial=0
                (0u8, temporal.min(self.temporal_layers.saturating_sub(1)))
            }
            CodecType::Vp9 => {
                // VP9 RTP payload descriptor (RFC 7741):
                // Byte 0: I(7) P(6) L(5) F(4) B(3) E(2) V(1) U(0)
                //   I=1: PictureID follows (1 or 2 bytes, MSB=1 → 16-bit)
                //   P: 0=intra(keyframe), 1=inter
                //   L=1: TL0PICIDX follows (1 byte)
                //   F: flexible mode
                //   B: start of frame
                //   E: end of frame
                //   V=1: spatial/temporal index follows (1 byte: SID[3] TID[3] R U)
                //   U=1: extension byte follows
                let mut temporal = 0u8;
                let mut spatial = 0u8;
                if !payload.is_empty() {
                    let first = payload[0];
                    let i_bit = (first >> 7) & 0x01;
                    let l_bit = (first >> 5) & 0x01;
                    let v_bit = (first >> 1) & 0x01;
                    let u_bit = first & 0x01;
                    let mut offset = 1;
                    // PictureID
                    if i_bit == 1 && offset < payload.len() {
                        let pic_id = payload[offset];
                        offset += 1;
                        if pic_id & 0x80 != 0 && offset < payload.len() {
                            offset += 1; // 16-bit PictureID
                        }
                    }
                    // TL0PICIDX
                    if l_bit == 1 && offset < payload.len() {
                        offset += 1;
                    }
                    // V=1: spatial/temporal index byte: SID(3) TID(3) R(1) U(1)
                    if v_bit == 1 && offset < payload.len() {
                        let idx_byte = payload[offset];
                        spatial = (idx_byte >> 5) & 0x07; // top 3 bits = SID
                        temporal = (idx_byte >> 2) & 0x07; // next 3 bits = TID
                        offset += 1;
                    }
                    // U=1: extension byte (may contain more info, skip)
                    if u_bit == 1 && offset < payload.len() {
                        // extension byte: skip any additional bytes
                        let ext = payload[offset];
                        offset += 1;
                        // extension may have more bytes depending on flags
                        let _ = ext;
                    }
                }
                (
                    spatial.min(self.spatial_layers.saturating_sub(1)),
                    temporal.min(self.temporal_layers.saturating_sub(1)),
                )
            }
            CodecType::Av1 => {
                // AV1 RTP payload (draft-ietf-payload-av1):
                // Byte 0: Z Y W N R T X
                // T bit (bit 2) = temporal_id present
                // X bit (bit 0) = spatial_id present (in extension)
                let mut temporal = 0u8;
                let mut spatial = 0u8;
                if !payload.is_empty() {
                    let first = payload[0];
                    let t_bit = (first >> 2) & 0x01;
                    let x_bit = first & 0x01;
                    let mut offset = 1;
                    if t_bit == 1 && offset < payload.len() {
                        temporal = payload[offset] & 0x07; // 3 bits temporal
                        offset += 1;
                    }
                    if x_bit == 1 && offset < payload.len() {
                        // extension byte contains spatial_id
                        spatial = (payload[offset] >> 5) & 0x03; // 2 bits spatial
                    }
                }
                (
                    spatial.min(self.spatial_layers.saturating_sub(1)),
                    temporal.min(self.temporal_layers.saturating_sub(1)),
                )
            }
            CodecType::H264 => {
                // H.264 SVC (H.264 Annex G):
                // NAL type 14 (Prefix NAL Unit) 或 type 20 (Coded Slice Extension)
                // 包含 SVC extension header: 3 bytes
                //   byte 0: reserved(1) idr_flag(1) priority_id(6)
                //   byte 1: no_inter_layer_pred(1) dependency_id(3) quality_id(4)
                //   byte 2: temporal_id(3) use_ref_base_pic_flag(1) discardable_flag(1) output_flag(1) reserved(2)
                //
                // 对于非 SVC H.264 (单层), dependency_id=0, temporal_id=0
                let mut spatial = 0u8; // dependency_id
                let mut temporal = 0u8;
                if !payload.is_empty() {
                    // H.264 RTP payload (RFC 6184):
                    // For FU-A: byte 0=FU indicator, byte 1=FU header (NAL type in lower 5 bits)
                    // For single NAL: byte 0 = NAL header (type in lower 5 bits)
                    let nal_type = if payload[0] & 0x1F == 28 {
                        // FU-A: NAL type in second byte
                        if payload.len() > 1 {
                            payload[1] & 0x1F
                        } else {
                            0
                        }
                    } else {
                        payload[0] & 0x1F
                    };

                    if nal_type == 14 || nal_type == 20 {
                        // SVC NAL: extension header after NAL header
                        // For single NAL: extension at bytes 1-3
                        // For FU-A: extension at bytes 2-4
                        let ext_offset = if payload[0] & 0x1F == 28 { 2 } else { 1 };
                        if ext_offset + 2 < payload.len() {
                            // byte 1 of extension: no_inter_layer_pred(1) dependency_id(3) quality_id(4)
                            let dep_byte = payload[ext_offset + 1];
                            spatial = (dep_byte >> 4) & 0x07; // dependency_id = spatial layer
                                                              // byte 2 of extension: temporal_id(3) ...
                            let tid_byte = payload[ext_offset + 2];
                            temporal = (tid_byte >> 5) & 0x07; // temporal_id
                        }
                    }
                    // 非 SVC H.264: spatial=0, temporal=0 (默认)
                }
                (
                    spatial.min(self.spatial_layers.saturating_sub(1)),
                    temporal.min(self.temporal_layers.saturating_sub(1)),
                )
            }
            _ => {
                // 其他 codec: 默认 base 层
                (0u8, 0u8)
            }
        };

        SvcLayerId::from_spatial_temporal(spatial_idx, temporal_idx)
    }

    /// 推送 RTP 包
    ///
    /// 从包中提取 SVC 层标识, 组装帧 (累积非 marker 包), 并分发给订阅了对应层的订阅者。
    /// 返回 true 如果帧组装完成。
    pub fn push_packet(&self, pkt: &RtpPacket) -> bool {
        let layer_id = self.extract_layer_id(pkt);

        // 帧组装: 累积 payload, marker=1 时表示帧完整
        let frame_data = {
            let mut buf = self.packet_buffer.lock().unwrap();
            buf.extend_from_slice(&pkt.payload);
            if !pkt.marker {
                return false;
            }
            // marker=1, 帧完整, 取出缓冲数据
            let data = buf.clone();
            buf.clear();
            data
        };

        // 从 payload 判断 keyframe
        let keyframe = self.detect_keyframe(&frame_data);

        let frame = MediaFrame {
            kind: TrackKind::Video,
            codec: self.codec,
            timestamp: pkt.timestamp,
            keyframe,
            spatial_layer: layer_id.spatial_idx(),
            temporal_layer: layer_id.temporal_idx(),
            data: bytes::Bytes::from(frame_data),
            ssrc: pkt.ssrc,
            rid: String::new(),
        };

        // 缓存帧
        {
            let mut frames = self.layer_frames.write();
            let vec = frames.entry(layer_id).or_default();
            vec.push(frame.clone());
            // 限制每层缓冲大小
            if vec.len() > 300 {
                vec.remove(0);
            }
        }

        // 分发给订阅了对应层的订阅者
        self.dispatch_to_subscribers(layer_id, &frame);

        true
    }

    /// 从帧数据判断是否为关键帧
    fn detect_keyframe(&self, data: &[u8]) -> bool {
        if data.is_empty() {
            return false;
        }
        match self.codec {
            CodecType::Vp8 => {
                // VP8: first byte of payload (after RTP descriptor) has bit 0 = P bit
                // P=0 means keyframe. But RTP descriptor varies.
                // 简化: 检查第一个字节 (RTP descriptor) 后的 VP8 payload
                // VP8 RTP descriptor: at least 1 byte, X bit determines extension
                if data.is_empty() {
                    return false;
                }
                let first = data[0];
                let x_bit = (first >> 7) & 0x01;
                let mut offset = 1;
                if x_bit == 1 && offset < data.len() {
                    let ext = data[offset];
                    offset += 1;
                    // I bit (PictureID)
                    if (ext >> 7) & 0x01 == 1 && offset < data.len() {
                        let pic_id = data[offset];
                        offset += 1;
                        if pic_id & 0x80 != 0 && offset < data.len() {
                            offset += 1;
                        }
                    }
                    // L bit (TL0PICIDX)
                    if (ext >> 6) & 0x01 == 1 && offset < data.len() {
                        offset += 1;
                    }
                    // T bit (TID) or V bit
                    if (ext >> 5) & 0x01 == 1 && offset < data.len() {
                        offset += 1;
                    }
                }
                if offset < data.len() {
                    // VP8 payload header: byte 0 bit 0 = P (0=keyframe, 1=inter)
                    let p_bit = data[offset] & 0x01;
                    return p_bit == 0;
                }
                false
            }
            CodecType::Vp9 => {
                // VP9: P bit in RTP descriptor byte 0, bit 6. P=0 = keyframe
                let p_bit = (data[0] >> 6) & 0x01;
                p_bit == 0
            }
            CodecType::H264 => {
                // H.264: NAL type 5 (IDR) or type 7 (SPS) indicates keyframe
                // For FU-A: check NAL type in FU header
                let nal_type = if data[0] & 0x1F == 28 && data.len() > 1 {
                    data[1] & 0x1F
                } else {
                    data[0] & 0x1F
                };
                nal_type == 5 || nal_type == 7 || nal_type == 8 // IDR, SPS, PPS
            }
            CodecType::Av1 => {
                // AV1: keyframe indicated by frame type in OBU
                // 简化: 检查 first byte W bit (0 = keyframe)
                let w = (data[0] >> 4) & 0x03;
                w == 0
            }
            _ => false,
        }
    }

    /// 添加订阅者 (指定目标层)
    ///
    /// 订阅者将只收到 <= target_spatial 且 <= target_temporal 的帧。
    pub fn add_subscriber(
        &self,
        sink: Arc<dyn StreamSink>,
        target_spatial: SvcSpatialLayer,
        target_temporal: SvcTemporalLayer,
    ) {
        let mut subs = self.subscribers.write();
        subs.push(SvcSubscriberEntry {
            sink,
            target_spatial,
            target_temporal,
            estimated_bandwidth_kbps: AtomicU64::new(0),
        });

        info!(
            stream = ?self.id,
            spatial = target_spatial.idx(),
            temporal = target_temporal.idx(),
            subscriber_count = subs.len(),
            "SVC subscriber added"
        );

        // 新订阅者加入时请求关键帧
        drop(subs);
        self.send_pli();
    }

    /// 更新订阅者目标层 (动态切换)
    ///
    /// 切换时请求关键帧, 确保新层可正确解码。
    pub fn update_subscriber_layer(
        &self,
        sink: &Arc<dyn StreamSink>,
        spatial: SvcSpatialLayer,
        temporal: SvcTemporalLayer,
    ) {
        let mut subs = self.subscribers.write();
        let mut found = false;
        for entry in subs.iter_mut() {
            if Arc::ptr_eq(&entry.sink, sink) {
                entry.target_spatial = spatial;
                entry.target_temporal = temporal;
                found = true;
                break;
            }
        }

        if found {
            info!(
                stream = ?self.id,
                spatial = spatial.idx(),
                temporal = temporal.idx(),
                "SVC subscriber layer updated"
            );
            drop(subs);
            self.send_pli();
        }
    }

    /// 移除订阅者
    pub fn remove_subscriber(&self, sink: &Arc<dyn StreamSink>) {
        let mut subs = self.subscribers.write();
        subs.retain(|e| !Arc::ptr_eq(&e.sink, sink));
        info!(
            stream = ?self.id,
            subscriber_count = subs.len(),
            "SVC subscriber removed"
        );
    }

    /// 分发帧给订阅了对应层的订阅者
    ///
    /// 订阅者收到帧的条件:
    /// - 帧的空间层 <= 订阅者的目标空间层
    /// - 帧的时间层 <= 订阅者的目标时间层
    ///
    /// 这是因为 SVC 的层间依赖: 高层依赖低层,
    /// 要解码高层必须先收到低层, 所以低层帧也要发给高层订阅者。
    fn dispatch_to_subscribers(&self, layer_id: SvcLayerId, frame: &MediaFrame) {
        let subs = self.subscribers.read();
        for entry in subs.iter() {
            // 订阅者收到所有 <= 其目标层的帧
            if layer_id.spatial <= entry.target_spatial
                && layer_id.temporal <= entry.target_temporal
            {
                entry.sink.on_frame(frame);
            }
        }
    }

    /// 更新订阅者估计带宽 (由 RTCP RR 或 TWCC 驱动)
    pub fn update_subscriber_bandwidth(&self, sink: &Arc<dyn StreamSink>, bandwidth_kbps: u64) {
        let subs = self.subscribers.read();
        for entry in subs.iter() {
            if Arc::ptr_eq(&entry.sink, sink) {
                entry
                    .estimated_bandwidth_kbps
                    .store(bandwidth_kbps, Ordering::Relaxed);
                break;
            }
        }
    }

    /// 获取订阅者数量
    pub fn subscriber_count(&self) -> usize {
        self.subscribers.read().len()
    }

    /// 获取空间层数量
    pub fn spatial_layers(&self) -> u8 {
        self.spatial_layers
    }

    /// 获取时间层数量
    pub fn temporal_layers(&self) -> u8 {
        self.temporal_layers
    }

    /// 获取 SSRC
    pub fn ssrc(&self) -> u32 {
        self.ssrc
    }

    /// 获取指定层的缓冲帧数
    pub fn layer_frame_count(&self, layer: SvcLayerId) -> usize {
        self.layer_frames
            .read()
            .get(&layer)
            .map(|v| v.len())
            .unwrap_or(0)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use lm_core::Backpressure;

    /// 测试用的帧收集器
    struct FrameCollector {
        frames: parking_lot::Mutex<Vec<MediaFrame>>,
    }

    impl FrameCollector {
        fn new() -> Self {
            Self {
                frames: parking_lot::Mutex::new(Vec::new()),
            }
        }

        fn frames(&self) -> Vec<MediaFrame> {
            self.frames.lock().clone()
        }
    }

    impl StreamSink for FrameCollector {
        fn on_frame(&self, frame: &MediaFrame) {
            self.frames.lock().push(frame.clone());
        }

        fn backpressure(&self) -> Backpressure {
            Backpressure::DropOldest
        }
    }

    #[test]
    fn test_gop_cache_video() {
        let mut cache = GopCache::new(2, 2000);

        // 第一个 keyframe
        let kf1 = MediaFrame::video(CodecType::Vp8, 1000, bytes::Bytes::from(vec![1]), 1, true);
        cache.save(&kf1);

        // P frames
        let pf1 = MediaFrame::video(CodecType::Vp8, 2000, bytes::Bytes::from(vec![2]), 1, false);
        cache.save(&pf1);
        let pf2 = MediaFrame::video(CodecType::Vp8, 3000, bytes::Bytes::from(vec![3]), 1, false);
        cache.save(&pf2);

        assert_eq!(cache.frame_count(), 3);

        // 第二个 keyframe → 新 GOP
        let kf2 = MediaFrame::video(CodecType::Vp8, 4000, bytes::Bytes::from(vec![4]), 1, true);
        cache.save(&kf2);

        assert_eq!(cache.frame_count(), 4); // 2 GOPs: [kf1,pf1,pf2] + [kf2]

        // 第三个 keyframe → 移除最旧 GOP
        let kf3 = MediaFrame::video(CodecType::Vp8, 5000, bytes::Bytes::from(vec![5]), 1, true);
        cache.save(&kf3);

        // gop_count=2，所以只有 2 个 GOP
        assert_eq!(cache.frame_count(), 2); // [kf2] + [kf3]
    }

    #[test]
    fn test_gop_cache_replay() {
        let mut cache = GopCache::new(1, 2000);

        let kf = MediaFrame::video(
            CodecType::Vp8,
            1000,
            bytes::Bytes::from(vec![1, 2, 3]),
            1,
            true,
        );
        cache.save(&kf);
        let pf = MediaFrame::video(
            CodecType::Vp8,
            2000,
            bytes::Bytes::from(vec![4, 5, 6]),
            1,
            false,
        );
        cache.save(&pf);

        let collector = Arc::new(FrameCollector::new());
        cache.replay(collector.as_ref());

        let frames = collector.frames();
        assert_eq!(frames.len(), 2);
        assert_eq!(frames[0].data.as_ref(), &[1, 2, 3]);
        assert_eq!(frames[1].data.as_ref(), &[4, 5, 6]);
    }

    #[test]
    fn test_gop_cache_audio_no_leak() {
        // 纯音频流：无关键帧，靠帧数限制轮转
        let mut cache = GopCache::new(1, 5); // max 5 frames per GOP

        for i in 0..20 {
            let frame = MediaFrame::audio(
                CodecType::Opus,
                i * 960,
                bytes::Bytes::from(vec![i as u8]),
                1,
            );
            cache.save(&frame);
        }

        // 应该只有 1 个 GOP，最多 5 帧
        assert_eq!(cache.frame_count(), 5);
    }

    #[test]
    fn test_stream_vp8_frame_assembly() {
        let stream = MediaStream::new(
            StreamId::new("session1", "track1"),
            Some("room1".into()),
            CodecType::Vp8,
            TrackKind::Video,
            12345,
            90000,
        );

        // 添加源流订阅者
        let collector = Arc::new(FrameCollector::new());
        stream.add_source_sink(collector.clone());

        // 推入 VP8 keyframe（2 个 RTP 包）
        let pkt1 = RtpPacket {
            ssrc: 12345,
            payload_type: 96,
            sequence_number: 1000,
            timestamp: 30000,
            marker: false,
            payload: bytes::Bytes::from(vec![
                0x90, 0x80, 0x00, // VP8 descriptor (3 bytes)
                0xf0, 0x51, 0x00, // frame_tag (keyframe)
                0x9d, 0x01, 0x2a, // sync code
                0x80, 0x02, 0xe0, 0x01, // 640x480
            ]),
            rid: String::new(),
        };
        stream.push_packet(&pkt1);

        let pkt2 = RtpPacket {
            ssrc: 12345,
            payload_type: 96,
            sequence_number: 1001,
            timestamp: 30000,
            marker: true,
            payload: bytes::Bytes::from(vec![
                0x80, 0x80, 0x01, // VP8 descriptor (3 bytes)
                0x39, 0x5f, 0x00, 0x23, // VP8 payload
            ]),
            rid: String::new(),
        };
        let frame_completed = stream.push_packet(&pkt2);

        assert!(frame_completed);

        let frames = collector.frames();
        assert_eq!(frames.len(), 1);
        assert!(frames[0].keyframe);
        assert_eq!(frames[0].ssrc, 12345);
        assert_eq!(frames[0].timestamp, 30000);
    }

    #[test]
    fn test_stream_gop_replay_on_subscribe() {
        let stream = MediaStream::new(
            StreamId::new("session1", "track1"),
            Some("room1".into()),
            CodecType::Vp8,
            TrackKind::Video,
            12345,
            90000,
        );

        // 先推入一帧（填充 GOP 缓存）
        let pkt = RtpPacket {
            ssrc: 12345,
            payload_type: 96,
            sequence_number: 1000,
            timestamp: 30000,
            marker: true,
            payload: bytes::Bytes::from(vec![
                0x90, 0x80, 0x00, // VP8 descriptor (3 bytes)
                0xf0, 0x51, 0x00, 0x9d, 0x01, 0x2a, 0x80, 0x02, 0xe0, 0x01,
            ]),
            rid: String::new(),
        };
        stream.push_packet(&pkt);

        // 现在订阅 — 应该收到 GOP 缓存中的帧
        let collector = Arc::new(FrameCollector::new());
        stream.add_source_sink(collector.clone());

        let frames = collector.frames();
        assert_eq!(frames.len(), 1, "should receive 1 frame from GOP cache");
        assert!(frames[0].keyframe);
    }

    #[test]
    fn test_stream_registry() {
        let registry = StreamRegistry::new();

        let stream1 = Arc::new(MediaStream::new(
            StreamId::new("session1", "track1"),
            Some("room1".into()),
            CodecType::Vp8,
            TrackKind::Video,
            111,
            90000,
        ));
        let stream2 = Arc::new(MediaStream::new(
            StreamId::new("session2", "track2"),
            Some("room1".into()),
            CodecType::Opus,
            TrackKind::Audio,
            222,
            48000,
        ));

        registry.register(stream1);
        registry.register(stream2);

        assert_eq!(registry.count(), 2);
        assert_eq!(registry.room_count(), 1);

        // 查找 room1 中排除 session1 的视频流
        let peers = registry.get_room_peer_streams("room1", "session1", TrackKind::Video);
        assert_eq!(peers.len(), 0); // session2 是音频流，不匹配视频

        // 查找 room1 中排除 session1 的音频流
        let peers = registry.get_room_peer_streams("room1", "session1", TrackKind::Audio);
        assert_eq!(peers.len(), 1);
        assert_eq!(peers[0].id.session_id, "session2");
    }

    #[test]
    fn test_simulcast_layer_from_rid() {
        assert_eq!(SimulcastTier::from_rid("low"), Some(SimulcastTier::Low));
        assert_eq!(SimulcastTier::from_rid("mid"), Some(SimulcastTier::Mid));
        assert_eq!(SimulcastTier::from_rid("high"), Some(SimulcastTier::High));
        assert_eq!(SimulcastTier::from_rid("unknown"), None);
    }

    #[test]
    fn test_simulcast_layer_priority() {
        assert!(SimulcastTier::High.priority() > SimulcastTier::Mid.priority());
        assert!(SimulcastTier::Mid.priority() > SimulcastTier::Low.priority());
    }

    #[test]
    fn test_simulcast_add_layer_and_route() {
        let sim = SimulcastStreamV1::new(
            StreamId::new("session1", "video"),
            Some("room1".into()),
            CodecType::Vp8,
            90000,
        );

        // 添加 low 和 high 层
        sim.add_layer(
            SimulcastTier::Low,
            LayerMeta {
                layer: SimulcastTier::Low,
                ssrc: 111,
                rid: "low".into(),
                bitrate: 150_000,
                target_bitrate_kbps: 150,
                width: 320,
                height: 180,
                fps: 15,
            },
            90000,
        );
        sim.add_layer(
            SimulcastTier::High,
            LayerMeta {
                layer: SimulcastTier::High,
                ssrc: 333,
                rid: "high".into(),
                bitrate: 1_500_000,
                target_bitrate_kbps: 1500,
                width: 1280,
                height: 720,
                fps: 30,
            },
            90000,
        );

        assert_eq!(sim.layers().len(), 2);
        assert!(sim.get_layer_meta(SimulcastTier::Low).is_some());
        assert!(sim.get_layer_meta(SimulcastTier::High).is_some());

        // 路由 low 层的 RTP 包
        let pkt = RtpPacket {
            ssrc: 111,
            payload_type: 96,
            sequence_number: 1000,
            timestamp: 30000,
            marker: true,
            payload: bytes::Bytes::from(vec![
                0x90, 0x80, 0x00, // VP8 descriptor
                0xf0, 0x51, 0x00, 0x9d, 0x01, 0x2a, 0x80, 0x02, 0xe0, 0x01,
            ]),
            rid: "low".into(),
        };
        let result = sim.push_packet(&pkt);
        assert!(result);
    }

    #[test]
    fn test_simulcast_dynacast() {
        let sim = SimulcastStreamV1::new(
            StreamId::new("session1", "video"),
            Some("room1".into()),
            CodecType::Vp8,
            90000,
        );

        sim.add_layer(
            SimulcastTier::Low,
            LayerMeta {
                layer: SimulcastTier::Low,
                ssrc: 111,
                rid: "low".into(),
                bitrate: 150_000,
                target_bitrate_kbps: 150,
                width: 320,
                height: 180,
                fps: 15,
            },
            90000,
        );
        sim.add_layer(
            SimulcastTier::High,
            LayerMeta {
                layer: SimulcastTier::High,
                ssrc: 333,
                rid: "high".into(),
                bitrate: 1_500_000,
                target_bitrate_kbps: 1500,
                width: 1280,
                height: 720,
                fps: 30,
            },
            90000,
        );

        // 初始状态：所有层启用
        assert!(sim.is_layer_enabled(SimulcastTier::Low));
        assert!(sim.is_layer_enabled(SimulcastTier::High));

        // 添加一个只订阅 low 层的订阅者
        let collector: Arc<dyn StreamSink> = Arc::new(FrameCollector::new());
        sim.add_subscriber(collector, LayerSelectionPolicy::Fixed(SimulcastTier::Low));

        // high 层应该被禁用（Dynacast）
        assert!(sim.is_layer_enabled(SimulcastTier::Low));
        assert!(!sim.is_layer_enabled(SimulcastTier::High));
    }

    #[test]
    fn test_simulcast_layer_switch() {
        let sim = SimulcastStreamV1::new(
            StreamId::new("session1", "video"),
            Some("room1".into()),
            CodecType::Vp8,
            90000,
        );

        sim.add_layer(
            SimulcastTier::Low,
            LayerMeta {
                layer: SimulcastTier::Low,
                ssrc: 111,
                rid: "low".into(),
                bitrate: 150_000,
                target_bitrate_kbps: 150,
                width: 320,
                height: 180,
                fps: 15,
            },
            90000,
        );
        sim.add_layer(
            SimulcastTier::High,
            LayerMeta {
                layer: SimulcastTier::High,
                ssrc: 333,
                rid: "high".into(),
                bitrate: 1_500_000,
                target_bitrate_kbps: 1500,
                width: 1280,
                height: 720,
                fps: 30,
            },
            90000,
        );

        let collector: Arc<dyn StreamSink> = Arc::new(FrameCollector::new());
        sim.add_subscriber(
            collector.clone(),
            LayerSelectionPolicy::Fixed(SimulcastTier::Low),
        );

        // 切换到 high 层
        let switched = sim.switch_layer(&collector, SimulcastTier::High);
        assert!(switched);

        // 切换后 high 层应该启用
        assert!(sim.is_layer_enabled(SimulcastTier::High));
    }

    // ========================================================================
    // SVC 测试
    // ========================================================================

    #[test]
    fn test_svc_layer_id_from_indices() {
        let layer = SvcLayerId::from_spatial_temporal(0, 0);
        assert_eq!(layer.spatial, SvcSpatialLayer::Base);
        assert_eq!(layer.temporal, SvcTemporalLayer::Base);
        assert_eq!(layer.spatial_idx(), 0);
        assert_eq!(layer.temporal_idx(), 0);

        let layer = SvcLayerId::from_spatial_temporal(1, 2);
        assert_eq!(layer.spatial, SvcSpatialLayer::Middle);
        assert_eq!(layer.temporal, SvcTemporalLayer::Full);
        assert_eq!(layer.spatial_idx(), 1);
        assert_eq!(layer.temporal_idx(), 2);

        let layer = SvcLayerId::from_spatial_temporal(2, 1);
        assert_eq!(layer.spatial, SvcSpatialLayer::High);
        assert_eq!(layer.temporal, SvcTemporalLayer::Middle);
        assert_eq!(layer.spatial_idx(), 2);
        assert_eq!(layer.temporal_idx(), 1);

        // 超出范围的索引应 clamp 到最高层
        let layer = SvcLayerId::from_spatial_temporal(255, 255);
        assert_eq!(layer.spatial, SvcSpatialLayer::High);
        assert_eq!(layer.temporal, SvcTemporalLayer::Full);
    }

    #[test]
    fn test_svc_stream_creation() {
        let stream = SvcStream::new(
            StreamId::new("session1", "video"),
            Some("room1".into()),
            CodecType::Vp9,
            12345,
            3,
            3,
        );

        assert_eq!(stream.ssrc(), 12345);
        assert_eq!(stream.spatial_layers(), 3);
        assert_eq!(stream.temporal_layers(), 3);
        assert_eq!(stream.subscriber_count(), 0);
    }

    #[test]
    fn test_svc_extract_layer_id() {
        let stream = SvcStream::new(
            StreamId::new("session1", "video"),
            Some("room1".into()),
            CodecType::Vp8,
            12345,
            1,
            3,
        );

        // VP8 payload without temporal info → Base/Base
        let pkt = RtpPacket {
            ssrc: 12345,
            payload_type: 96,
            sequence_number: 1,
            timestamp: 30000,
            marker: true,
            payload: bytes::Bytes::from(vec![0x00]), // no X bit
            rid: String::new(),
        };
        let layer = stream.extract_layer_id(&pkt);
        assert_eq!(layer.spatial, SvcSpatialLayer::Base);
        assert_eq!(layer.temporal, SvcTemporalLayer::Base);

        // VP8 payload with X=1, T=1, TL0PICIDX=2 → temporal=2 (Full)
        // Byte 0: X=1 (0x80), R=0, N=0, S=0, PartID=0 → 0x80
        // Byte 1 (ext): T=1 (0x20), M=0, L=0, K=0 → 0x20
        // Byte 2: TL0PICIDX = 2
        let pkt = RtpPacket {
            ssrc: 12345,
            payload_type: 96,
            sequence_number: 2,
            timestamp: 31000,
            marker: true,
            payload: bytes::Bytes::from(vec![0x80, 0x20, 0x02]),
            rid: String::new(),
        };
        let layer = stream.extract_layer_id(&pkt);
        assert_eq!(layer.temporal, SvcTemporalLayer::Full);
    }

    #[test]
    fn test_svc_add_subscriber() {
        let stream = SvcStream::new(
            StreamId::new("session1", "video"),
            Some("room1".into()),
            CodecType::Vp9,
            12345,
            3,
            3,
        );

        let collector: Arc<dyn StreamSink> = Arc::new(FrameCollector::new());
        stream.add_subscriber(collector, SvcSpatialLayer::High, SvcTemporalLayer::Full);

        assert_eq!(stream.subscriber_count(), 1);
    }

    #[test]
    fn test_svc_update_subscriber_layer() {
        let stream = SvcStream::new(
            StreamId::new("session1", "video"),
            Some("room1".into()),
            CodecType::Vp9,
            12345,
            3,
            3,
        );

        let collector: Arc<dyn StreamSink> = Arc::new(FrameCollector::new());
        stream.add_subscriber(
            collector.clone(),
            SvcSpatialLayer::Base,
            SvcTemporalLayer::Base,
        );

        // 更新到更高层
        stream.update_subscriber_layer(&collector, SvcSpatialLayer::High, SvcTemporalLayer::Full);

        assert_eq!(stream.subscriber_count(), 1);
    }

    #[test]
    fn test_svc_dispatch_to_subscribers() {
        let stream = SvcStream::new(
            StreamId::new("session1", "video"),
            Some("room1".into()),
            CodecType::Vp9,
            12345,
            3,
            3,
        );

        // 订阅者 A: 只订阅 Base/Base
        let collector_a = Arc::new(FrameCollector::new());
        stream.add_subscriber(
            collector_a.clone() as Arc<dyn StreamSink>,
            SvcSpatialLayer::Base,
            SvcTemporalLayer::Base,
        );

        // 订阅者 B: 订阅 High/Full (应收到所有层)
        let collector_b = Arc::new(FrameCollector::new());
        stream.add_subscriber(
            collector_b.clone() as Arc<dyn StreamSink>,
            SvcSpatialLayer::High,
            SvcTemporalLayer::Full,
        );

        // 推送 Base/Base 层帧 (VP8 简化 payload, marker=true)
        let pkt_base = RtpPacket {
            ssrc: 12345,
            payload_type: 96,
            sequence_number: 1,
            timestamp: 30000,
            marker: true,
            payload: bytes::Bytes::from(vec![0x00]), // no temporal info → Base/Base
            rid: String::new(),
        };
        stream.push_packet(&pkt_base);

        // 两个订阅者都应收到 Base/Base 帧
        let frames_a = collector_a.frames();
        let frames_b = collector_b.frames();
        assert_eq!(frames_a.len(), 1, "subscriber A should receive base frame");
        assert_eq!(frames_b.len(), 1, "subscriber B should receive base frame");
    }

    // ========================================================================
    // Dynacast 测试
    // ========================================================================

    /// 辅助: 创建带 Low + High 层的 SimulcastStreamV1
    fn create_simulcast_with_layers() -> SimulcastStreamV1 {
        let sim = SimulcastStreamV1::new(
            StreamId::new("session1", "video"),
            Some("room1".into()),
            CodecType::Vp8,
            90000,
        );
        sim.add_layer(
            SimulcastTier::Low,
            LayerMeta {
                layer: SimulcastTier::Low,
                ssrc: 111,
                rid: "low".into(),
                bitrate: 150_000,
                target_bitrate_kbps: 150,
                width: 320,
                height: 180,
                fps: 15,
            },
            90000,
        );
        sim.add_layer(
            SimulcastTier::High,
            LayerMeta {
                layer: SimulcastTier::High,
                ssrc: 333,
                rid: "high".into(),
                bitrate: 1_500_000,
                target_bitrate_kbps: 1500,
                width: 1280,
                height: 720,
                fps: 30,
            },
            90000,
        );
        sim
    }

    #[test]
    fn test_dynacast_disable_unused_layer() {
        let sim = create_simulcast_with_layers();

        // 初始状态: 所有层启用
        assert!(sim.is_layer_enabled(SimulcastTier::Low));
        assert!(sim.is_layer_enabled(SimulcastTier::High));

        // 添加一个只订阅 low 层的订阅者
        let collector: Arc<dyn StreamSink> = Arc::new(FrameCollector::new());
        sim.add_subscriber(collector, LayerSelectionPolicy::Fixed(SimulcastTier::Low));

        // high 层应该被禁用 (Dynacast), low 层保持启用
        assert!(sim.is_layer_enabled(SimulcastTier::Low));
        assert!(!sim.is_layer_enabled(SimulcastTier::High));
    }

    #[test]
    fn test_dynacast_enable_on_demand() {
        let sim = create_simulcast_with_layers();

        // 先只订阅 low → high 被禁用
        let collector_low: Arc<dyn StreamSink> = Arc::new(FrameCollector::new());
        sim.add_subscriber(
            collector_low,
            LayerSelectionPolicy::Fixed(SimulcastTier::Low),
        );
        assert!(!sim.is_layer_enabled(SimulcastTier::High));

        // 再添加一个订阅 high 层的订阅者 → high 重新启用
        let collector_high: Arc<dyn StreamSink> = Arc::new(FrameCollector::new());
        sim.add_subscriber(
            collector_high,
            LayerSelectionPolicy::Fixed(SimulcastTier::High),
        );

        assert!(sim.is_layer_enabled(SimulcastTier::High));
        assert!(sim.is_layer_enabled(SimulcastTier::Low));
    }

    /// 测试用关键帧请求计数器
    struct CountingKeyframeRequester {
        count: AtomicU64,
    }

    impl CountingKeyframeRequester {
        fn new() -> Self {
            Self {
                count: AtomicU64::new(0),
            }
        }

        fn count(&self) -> u64 {
            self.count.load(Ordering::Relaxed)
        }
    }

    impl KeyframeRequester for CountingKeyframeRequester {
        fn request_keyframe(&self, _ssrc: u32) {
            self.count.fetch_add(1, Ordering::Relaxed);
        }
    }

    #[test]
    fn test_dynacast_keyframe_on_change() {
        let sim = create_simulcast_with_layers();

        let requester = Arc::new(CountingKeyframeRequester::new());
        sim.set_keyframe_requester(requester.clone());

        let initial_count = requester.count();

        // 添加订阅者触发 dynacast 变化 (high 被禁用)
        let collector: Arc<dyn StreamSink> = Arc::new(FrameCollector::new());
        sim.add_subscriber(collector, LayerSelectionPolicy::Fixed(SimulcastTier::Low));

        // 层状态变化时应请求关键帧 (至少 Low 层保持启用会请求)
        let after_add = requester.count();
        assert!(
            after_add > initial_count,
            "keyframe should be requested on layer state change"
        );

        // 切换到 high 层 → high 重新启用, 应再次请求关键帧
        let collector2: Arc<dyn StreamSink> = Arc::new(FrameCollector::new());
        sim.add_subscriber(collector2, LayerSelectionPolicy::Fixed(SimulcastTier::High));

        let after_switch = requester.count();
        assert!(
            after_switch > after_add,
            "keyframe should be requested when high layer re-enabled"
        );
    }

    // ========================================================================
    // 自适应层选择测试
    // ========================================================================

    #[test]
    fn test_adaptive_layer_selection_high_bandwidth() {
        let sim = create_simulcast_with_layers();

        // 带宽 2000 kbps >= high (1500) → 应选 High
        let layer = sim.select_layer_for_policy(&LayerSelectionPolicy::Adaptive, 2000);
        assert_eq!(layer, SimulcastTier::High);
    }

    #[test]
    fn test_adaptive_layer_selection_low_bandwidth() {
        let sim = create_simulcast_with_layers();

        // 带宽 100 kbps < low (150) → 应选 Low
        let layer = sim.select_layer_for_policy(&LayerSelectionPolicy::Adaptive, 100);
        assert_eq!(layer, SimulcastTier::Low);
    }

    #[test]
    fn test_adaptive_layer_selection_unknown_bandwidth() {
        let sim = create_simulcast_with_layers();

        // 带宽未知 (0) → 应选最高可用层 (High)
        let layer = sim.select_layer_for_policy(&LayerSelectionPolicy::Adaptive, 0);
        assert_eq!(layer, SimulcastTier::High);
    }
}
