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
use tracing::{debug, info};

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
            let mut entry = self.room_streams.entry(room.clone()).or_insert_with(Vec::new);
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
pub enum SimulcastLayer {
    Low,
    Mid,
    High,
}

impl SimulcastLayer {
    /// 从 RID 字符串解析
    pub fn from_rid(rid: &str) -> Option<Self> {
        match rid.to_lowercase().as_str() {
            "low" | "l" | "quarter" => Some(SimulcastLayer::Low),
            "mid" | "m" | "half" => Some(SimulcastLayer::Mid),
            "high" | "h" | "full" => Some(SimulcastLayer::High),
            _ => None,
        }
    }

    /// 转为 RID 字符串
    pub fn to_rid(&self) -> &'static str {
        match self {
            SimulcastLayer::Low => "low",
            SimulcastLayer::Mid => "mid",
            SimulcastLayer::High => "high",
        }
    }

    /// 优先级数值（用于排序，越大越优先）
    pub fn priority(&self) -> u8 {
        match self {
            SimulcastLayer::Low => 0,
            SimulcastLayer::Mid => 1,
            SimulcastLayer::High => 2,
        }
    }
}

/// Simulcast 层元数据
#[derive(Debug, Clone)]
pub struct LayerMeta {
    pub layer: SimulcastLayer,
    pub ssrc: u32,
    pub rid: String,
    /// 估计码率（bps），由 publisher 在 signaling 中声明或由 SFU 测量
    pub bitrate: u32,
    /// 分辨率
    pub width: u16,
    pub height: u16,
    /// 帧率
    pub fps: u8,
}

/// 订阅者层选择策略
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LayerSelectionPolicy {
    /// 固定选择指定层
    Fixed(SimulcastLayer),
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
pub struct SimulcastStream {
    /// 流标识（不含 layer）
    pub id: StreamId,
    pub room_id: Option<String>,
    pub codec: CodecType,

    /// 各层的 MediaStream
    layers: RwLock<std::collections::HashMap<SimulcastLayer, Arc<MediaStream>>>,

    /// 各层元数据
    layer_metas: RwLock<std::collections::HashMap<SimulcastLayer, LayerMeta>>,

    /// 订阅者 → 选层策略
    subscribers: RwLock<Vec<(Arc<dyn StreamSink>, LayerSelectionPolicy)>>,

    /// 当前各层是否启用（Dynacast）
    /// 如果没有任何订阅者需要某层，可以请求 publisher 停止发送该层
    layer_enabled: RwLock<std::collections::HashMap<SimulcastLayer, bool>>,
}

impl SimulcastStream {
    /// 创建 Simulcast 流
    pub fn new(
        id: StreamId,
        room_id: Option<String>,
        codec: CodecType,
        clock_rate: u32,
    ) -> Self {
        let mut layer_enabled = std::collections::HashMap::new();
        layer_enabled.insert(SimulcastLayer::Low, true);
        layer_enabled.insert(SimulcastLayer::Mid, true);
        layer_enabled.insert(SimulcastLayer::High, true);

        Self {
            id,
            room_id,
            codec,
            layers: RwLock::new(std::collections::HashMap::new()),
            layer_metas: RwLock::new(std::collections::HashMap::new()),
            subscribers: RwLock::new(Vec::new()),
            layer_enabled: RwLock::new(layer_enabled),
        }
    }

    /// 注册一个 simulcast 层
    pub fn add_layer(&self, layer: SimulcastLayer, meta: LayerMeta, clock_rate: u32) {
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
            SimulcastLayer::from_rid(&pkt.rid)
        } else {
            // 无 RID 时通过 SSRC 匹配
            let metas = self.layer_metas.read();
            metas.iter().find(|(_, m)| m.ssrc == pkt.ssrc).map(|(l, _)| *l)
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
    fn dispatch_to_subscribers(&self, layer: SimulcastLayer) {
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
        let layer = self.select_layer_for_policy(&policy);

        // 订阅到对应层的 MediaStream
        let layers = self.layers.read();
        if let Some(stream) = layers.get(&layer) {
            stream.add_source_sink(sink.clone());
        }

        let mut subs = self.subscribers.write();
        subs.push((sink, policy));

        info!(
            stream = ?self.id,
            layer = layer.to_rid(),
            subscriber_count = subs.len(),
            "simulcast subscriber added"
        );

        // 更新 Dynacast
        drop(subs);
        self.update_dynacast();
    }

    /// 移除订阅者
    pub fn remove_subscriber(&self, sink: &Arc<dyn StreamSink>) {
        let mut subs = self.subscribers.write();
        subs.retain(|(s, _)| !Arc::ptr_eq(s, sink));
        drop(subs);
        self.update_dynacast();
    }

    /// 根据策略选择层
    fn select_layer_for_policy(&self, policy: &LayerSelectionPolicy) -> SimulcastLayer {
        match policy {
            LayerSelectionPolicy::Fixed(layer) => *layer,
            LayerSelectionPolicy::Highest => {
                // 选可用的最高层
                let layers = self.layers.read();
                if layers.contains_key(&SimulcastLayer::High) {
                    SimulcastLayer::High
                } else if layers.contains_key(&SimulcastLayer::Mid) {
                    SimulcastLayer::Mid
                } else {
                    SimulcastLayer::Low
                }
            }
            LayerSelectionPolicy::Lowest => SimulcastLayer::Low,
            LayerSelectionPolicy::Adaptive => {
                // 简化：默认选 mid 层
                // TODO: 根据订阅者带宽、RTT、丢包率自适应
                let layers = self.layers.read();
                if layers.contains_key(&SimulcastLayer::Mid) {
                    SimulcastLayer::Mid
                } else {
                    SimulcastLayer::Low
                }
            }
        }
    }

    /// Dynacast：根据订阅者需求启用/禁用 publisher 层
    ///
    /// 如果没有任何订阅者需要某层，标记为禁用。
    /// 实际禁用通过 RTCP PLI 或 signaling 通知 publisher 停止发送。
    fn update_dynacast(&self) {
        let subs = self.subscribers.read();
        let mut needed_layers = std::collections::HashSet::new();

        for (_, policy) in subs.iter() {
            let layer = self.select_layer_for_policy(policy);
            needed_layers.insert(layer);
        }

        let mut enabled = self.layer_enabled.write();
        for layer in [SimulcastLayer::Low, SimulcastLayer::Mid, SimulcastLayer::High] {
            let is_needed = needed_layers.contains(&layer);
            let was_enabled = *enabled.get(&layer).unwrap_or(&true);
            *enabled.entry(layer).or_insert(true) = is_needed;

            if was_enabled && !is_needed {
                info!(
                    stream = ?self.id,
                    layer = layer.to_rid(),
                    "dynacast: disabling unused layer"
                );
            } else if !was_enabled && is_needed {
                info!(
                    stream = ?self.id,
                    layer = layer.to_rid(),
                    "dynacast: enabling needed layer"
                );
            }
        }
    }

    /// 切换订阅者的层
    ///
    /// 切换时需要请求关键帧（通过 RTCP PLI）
    pub fn switch_layer(
        &self,
        sink: &Arc<dyn StreamSink>,
        new_layer: SimulcastLayer,
    ) -> bool {
        let mut subs = self.subscribers.write();

        // 找到订阅者并更新策略
        let mut found = false;
        for (s, policy) in subs.iter_mut() {
            if Arc::ptr_eq(s, sink) {
                *policy = LayerSelectionPolicy::Fixed(new_layer);
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
            // TODO: 请求关键帧（RTCP PLI）
            // 切换层时需要关键帧才能正确解码
        }

        drop(subs);
        if found {
            self.update_dynacast();
        }
        found
    }

    /// 请求关键帧
    ///
    /// 通过 RTCP PLI 请求 publisher 发送关键帧
    /// 用于层切换、新订阅者加入等场景
    pub fn request_keyframe(&self, layer: SimulcastLayer) {
        info!(
            stream = ?self.id,
            layer = layer.to_rid(),
            "requesting keyframe (PLI)"
        );
        // TODO: 发送 RTCP PLI 包到 publisher
        // 当前由 Go/Pion 层处理 RTCP，需要通过 gRPC 通知 Go 层发送 PLI
    }

    /// 获取层元数据
    pub fn get_layer_meta(&self, layer: SimulcastLayer) -> Option<LayerMeta> {
        self.layer_metas.read().get(&layer).cloned()
    }

    /// 获取所有层
    pub fn layers(&self) -> Vec<SimulcastLayer> {
        self.layers.read().keys().copied().collect()
    }

    /// 获取订阅者数量
    pub fn subscriber_count(&self) -> usize {
        self.subscribers.read().len()
    }

    /// 检查层是否启用
    pub fn is_layer_enabled(&self, layer: SimulcastLayer) -> bool {
        *self.layer_enabled.read().get(&layer).unwrap_or(&true)
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

        let kf = MediaFrame::video(CodecType::Vp8, 1000, bytes::Bytes::from(vec![1, 2, 3]), 1, true);
        cache.save(&kf);
        let pf = MediaFrame::video(CodecType::Vp8, 2000, bytes::Bytes::from(vec![4, 5, 6]), 1, false);
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
            let frame = MediaFrame::audio(CodecType::Opus, i * 960, bytes::Bytes::from(vec![i as u8]), 1);
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
        assert_eq!(SimulcastLayer::from_rid("low"), Some(SimulcastLayer::Low));
        assert_eq!(SimulcastLayer::from_rid("mid"), Some(SimulcastLayer::Mid));
        assert_eq!(SimulcastLayer::from_rid("high"), Some(SimulcastLayer::High));
        assert_eq!(SimulcastLayer::from_rid("unknown"), None);
    }

    #[test]
    fn test_simulcast_layer_priority() {
        assert!(SimulcastLayer::High.priority() > SimulcastLayer::Mid.priority());
        assert!(SimulcastLayer::Mid.priority() > SimulcastLayer::Low.priority());
    }

    #[test]
    fn test_simulcast_add_layer_and_route() {
        let sim = SimulcastStream::new(
            StreamId::new("session1", "video"),
            Some("room1".into()),
            CodecType::Vp8,
            90000,
        );

        // 添加 low 和 high 层
        sim.add_layer(
            SimulcastLayer::Low,
            LayerMeta {
                layer: SimulcastLayer::Low,
                ssrc: 111,
                rid: "low".into(),
                bitrate: 150_000,
                width: 320,
                height: 180,
                fps: 15,
            },
            90000,
        );
        sim.add_layer(
            SimulcastLayer::High,
            LayerMeta {
                layer: SimulcastLayer::High,
                ssrc: 333,
                rid: "high".into(),
                bitrate: 1_500_000,
                width: 1280,
                height: 720,
                fps: 30,
            },
            90000,
        );

        assert_eq!(sim.layers().len(), 2);
        assert!(sim.get_layer_meta(SimulcastLayer::Low).is_some());
        assert!(sim.get_layer_meta(SimulcastLayer::High).is_some());

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
        let sim = SimulcastStream::new(
            StreamId::new("session1", "video"),
            Some("room1".into()),
            CodecType::Vp8,
            90000,
        );

        sim.add_layer(
            SimulcastLayer::Low,
            LayerMeta {
                layer: SimulcastLayer::Low,
                ssrc: 111,
                rid: "low".into(),
                bitrate: 150_000,
                width: 320,
                height: 180,
                fps: 15,
            },
            90000,
        );
        sim.add_layer(
            SimulcastLayer::High,
            LayerMeta {
                layer: SimulcastLayer::High,
                ssrc: 333,
                rid: "high".into(),
                bitrate: 1_500_000,
                width: 1280,
                height: 720,
                fps: 30,
            },
            90000,
        );

        // 初始状态：所有层启用
        assert!(sim.is_layer_enabled(SimulcastLayer::Low));
        assert!(sim.is_layer_enabled(SimulcastLayer::High));

        // 添加一个只订阅 low 层的订阅者
        let collector: Arc<dyn StreamSink> = Arc::new(FrameCollector::new());
        sim.add_subscriber(collector, LayerSelectionPolicy::Fixed(SimulcastLayer::Low));

        // high 层应该被禁用（Dynacast）
        assert!(sim.is_layer_enabled(SimulcastLayer::Low));
        assert!(!sim.is_layer_enabled(SimulcastLayer::High));
    }

    #[test]
    fn test_simulcast_layer_switch() {
        let sim = SimulcastStream::new(
            StreamId::new("session1", "video"),
            Some("room1".into()),
            CodecType::Vp8,
            90000,
        );

        sim.add_layer(
            SimulcastLayer::Low,
            LayerMeta {
                layer: SimulcastLayer::Low,
                ssrc: 111,
                rid: "low".into(),
                bitrate: 150_000,
                width: 320,
                height: 180,
                fps: 15,
            },
            90000,
        );
        sim.add_layer(
            SimulcastLayer::High,
            LayerMeta {
                layer: SimulcastLayer::High,
                ssrc: 333,
                rid: "high".into(),
                bitrate: 1_500_000,
                width: 1280,
                height: 720,
                fps: 30,
            },
            90000,
        );

        let collector: Arc<dyn StreamSink> = Arc::new(FrameCollector::new());
        sim.add_subscriber(collector.clone(), LayerSelectionPolicy::Fixed(SimulcastLayer::Low));

        // 切换到 high 层
        let switched = sim.switch_layer(&collector, SimulcastLayer::High);
        assert!(switched);

        // 切换后 high 层应该启用
        assert!(sim.is_layer_enabled(SimulcastLayer::High));
    }
}
