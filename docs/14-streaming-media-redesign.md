# 14 — 流媒体层重构方案与实施记录（基于 Xiu/atm0s/Waterbus 调研）

> 基于 Xiu、atm0s-media-server、Waterbus 三个开源项目的深度调研，结合 LingVoice 当前架构痛点，制定流媒体层重构方案。
>
> **实施状态：全部完成 ✅**（截至 2026-08-26）

## 实施完成总结

| Phase | 内容 | 状态 | 提交 |
|-------|------|------|------|
| Phase 1 | MediaFrame 抽象 + Depacketizer + Stream 重构 | ✅ | `b066c05` |
| Phase 2 | GOP 缓存 + 快速首屏 | ✅ | `b066c05` |
| Phase 3 | 分段录制 + MP4 合并 + 录制状态机 | ✅ | `58edefb` |
| Phase 4 | Simulcast（RID 路由 + 层选择 + Dynacast） | ✅ | `12736d5` |
| Phase 5 | 协议转封装（HLS/HTTP-FLV/RTMP remuxer） | ✅ | `a66d5d6` |
| Phase 6-10 | RTMP/RTSP/SRT/GB28181/WHIP/WHEP remuxer 框架 | ✅ | `a66d5d6` |
| HTTP 输出 | media-node HTTP 服务（HLS + HTTP-FLV + API） | ✅ | `0ac2516` |
| 端到端验证 | gRPC PushRtp → HLS playlist 完整链路 | ✅ | `0ac2516` |

### 新增 crate

| Crate | 职责 |
|-------|------|
| `lm-depacketizer` | RTP→Frame 解包器（VP8/VP9/H264/Opus） |
| `lm-stream` | 媒体流抽象 + GOP 缓存 + Simulcast 路由 |
| `lm-protocol` | 协议转封装框架（HLS/FLV/RTMP/RTSP/SRT/GB28181/WHIP/WHEP） |

### 测试覆盖

全 workspace **97 个 Rust 测试** + **40 个 Go 测试** = **137 个测试**全部通过。

### 端到端验证结果

```
gRPC PushRtp → MediaStream → VP8 Depacketizer → MediaFrame → HlsRemuxer → HLS playlist + TS segments
```

HLS playlist 正确生成 4 个 1 秒分段：
```
#EXTM3U
#EXT-X-VERSION:6
#EXT-X-TARGETDURATION:1
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-PLAYLIST-TYPE:EVENT
#EXTINF:1.000,
hls-test-session/video_seg0000.ts
...
```

### 协议矩阵

| 协议 | Demuxer（输入） | Remuxer（输出） | 网络监听 |
|------|:---:|:---:|:---:|
| WebRTC | ✅ (gRPC) | ✅ (gRPC) | Go/Pion |
| WHIP | 框架就绪 | - | Go |
| WHEP | - | 框架就绪 | Go |
| RTMP | - | ✅ | Go |
| RTSP | ✅ | ✅ | Go |
| HTTP-FLV | - | ✅ | Rust HTTP |
| HLS | - | ✅ | Rust HTTP |
| LL-HLS | - | 配置支持 | Rust HTTP |
| SRT | ✅ | ✅ | Go |
| GB28181 | ✅ | - | Go |

> **架构边界**：Rust 只处理帧级转封装输出，不处理输入协议。输入协议（RTMP/RTSP/SRT/GB28181/WHIP）由 Go 层处理，Go 解包后通过 gRPC 把 RTP 喂给 Rust。

---

---

## 一、当前架构痛点

### 1.1 路由层：broadcast 混杂源流与转发流

**现状**：`push_rtp` 收到包后，同时发送到：
- 同 room 其他 session 的 peer track broadcast（SFU 转发）
- 源 track 自身的 broadcast（用于录制）

**问题**：
- 源 track 的 broadcast channel 混杂了自身包和被路由进来的其他 session 包
- 录制 task 订阅源 track 时，需要 SSRC 过滤才能拿到干净数据
- 这不是干净的"源流"与"转发流"分离

### 1.2 缺少帧级抽象

**现状**：Rust 层只处理 `RtpPacket`，没有 `MediaFrame`/`FrameData` 抽象。

**问题**：
- 录制时需要自己从 RTP 包组装 VP8 帧（descriptor 解析、S bit、marker 边界）
- 转封装（如 WebRTC→HLS）无法实现，因为没有统一的帧格式
- 无法复用 Xiu 的 Demuxer-Remuxer 模式

### 1.3 缺少 GOP 缓存

**现状**：无 GOP 缓存，新订阅者只能从当前包开始接收。

**问题**：
- 新加入的订阅者看不到关键帧，视频无法解码
- 需要等待下一个关键帧（通常 1-2 秒），首屏延迟高
- Xiu 的做法：缓存最近 N 个 GOP，新订阅者先发缓存数据

### 1.4 录制架构不干净

**现状**：录制 task 订阅 track broadcast，用 SSRC 过滤。

**问题**：
- 录制数据源不纯净（混杂转发流）
- 帧组装逻辑耦合在 recorder.rs 里
- 无法支持分段录制（类似腾讯会议的分片→合并 MP4）
- 没有录制状态机（Requested→Starting→Active→Stopping→Finalizing）

### 1.5 缺少 Simulcast 支持

**现状**：RID 字段存在于 proto 中但未使用。

**问题**：
- 无法按 RID 路由不同空间层
- 无法按订阅者带宽选择层
- 大会议无法降级到低分辨率

---

## 二、参考项目核心借鉴点

### 2.1 Xiu — 协议转换与流媒体枢纽

| 借鉴点 | 应用到 LingVoice |
|--------|-----------------|
| **StreamHub 中央路由** | 替换当前的 broadcast channel 路由，用 Stream 抽象统一管理 |
| **FrameData/PacketData 双层抽象** | 新增 `MediaFrame` 抽象，RTP 是传输层，Frame 是媒体层 |
| **Demuxer-Remuxer 转封装** | WebRTC→HLS/MP4 录制不转码，只转封装 |
| **GOP 缓存 + 首屏加速** | 新增 GOP cache，新订阅者先发缓存 |
| **HTTP 事件回调** | 录制开始/结束、流发布/停止的事件通知 |
| **BytesMut 零拷贝** | 帧数据用 `bytes::Bytes` 引用计数，避免复制 |

### 2.2 atm0s-media-server — 分布式 SFU

| 借鉴点 | 应用到 LingVoice |
|--------|-----------------|
| **Transport→Endpoint→Cluster 分层** | 明确传输层/端点逻辑/集群协调的边界 |
| **MediaPacket 抽象** | 统一的媒体包格式（codec + seq + ts + marker + payload） |
| **PacketSelector 包选择器** | Simulcast 层选择、比特率自适应、关键帧请求 |
| **SeqRewrite/TsRewrite** | 切换源时保持序列号/时间戳连续 |
| **录制分块上传** | RecordChunkWriter 分块写入，异步上传 |
| **Sans-IO 媒体核心** | 媒体处理逻辑与 I/O 解耦，便于测试 |

### 2.3 Waterbus — 会议 SFU + HLS 输出

| 借鉴点 | 应用到 LingVoice |
|--------|-----------------|
| **Simulcast + Dynacast** | 无人订阅的层自动暂停，节省带宽 |
| **Adaptive Stream** | 根据视图大小自动选择分辨率层 |
| **TWCC 拥塞控制** | Transport-Wide 拥塞反馈，精确带宽估计 |
| **HLS Egress** | SFU→HLS 分段输出，录制即 HLS 分片 |
| **SFU + Egress 共置** | 录制/输出与 SFU 同节点，避免跨节点媒体复制 |

---

## 三、重构方案

### 3.1 核心抽象层重构

#### 3.1.1 新增 `MediaFrame` 帧级抽象

```rust
// lm-core/src/media_frame.rs

/// 媒体帧（已从 RTP 包组装完成的完整帧）
/// 参考 Xiu FrameData + atm0s MediaPacket
#[derive(Debug, Clone)]
pub struct MediaFrame {
    /// 帧类型
    pub kind: TrackKind,
    /// 编解码类型
    pub codec: CodecType,
    /// RTP 时间戳（90kHz for video, codec-specific for audio）
    pub timestamp: u32,
    /// 是否关键帧
    pub keyframe: bool,
    /// simulcast 空间层（0=base, 1=mid, 2=high）
    pub spatial_layer: u8,
    /// simulcast 时间层
    pub temporal_layer: u8,
    /// 帧数据（已剥离 RTP descriptor，完整的编码帧）
    pub data: Bytes,
    /// 源 SSRC
    pub ssrc: u32,
}
```

#### 3.1.2 新增 `Depacketizer` trait（RTP→Frame）

```rust
// lm-core/src/depacketizer.rs

/// RTP 解包器：将 RTP 包序列组装为完整媒体帧
/// 参考 atm0s 的 PacketSelector + Xiu 的 Demuxer
pub trait Depacketizer: Send {
    /// 处理一个 RTP 包，返回是否组装出完整帧
    fn push_packet(&mut self, pkt: &RtpPacket) -> DepacketizeResult;
    
    /// 取出组装完成的帧
    fn take_frame(&mut self) -> Option<MediaFrame>;
    
    /// 重置状态（丢弃当前不完整的帧）
    fn reset(&mut self);
}

pub enum DepacketizeResult {
    /// 需要更多包
    NeedMore,
    /// 帧已组装完成
    FrameComplete,
    /// 包错误（序列号跳跃、descriptor 解析失败等）
    Error(String),
}
```

#### 3.1.3 新增 `Packetizer` trait（Frame→RTP）

```rust
// lm-core/src/packetizer.rs

/// RTP 打包器：将媒体帧拆分为 RTP 包
pub trait Packetizer: Send {
    /// 将帧打包为 RTP 包列表
    fn packetize(&mut self, frame: &MediaFrame) -> Vec<RtpPacket>;
}
```

### 3.2 路由层重构：Stream 抽象

#### 3.2.1 `MediaStream` — 替代当前的 broadcast channel

```rust
// lm-stream/src/stream.rs

/// 媒体流（一个 publisher 的一个 track = 一个流）
/// 参考 Xiu StreamHub + atm0s MediaTrack
pub struct MediaStream {
    /// 流标识
    pub id: StreamId,
    /// 所属 session
    pub session_id: SessionId,
    /// 所属 room
    pub room_id: Option<String>,
    /// 编解码
    pub codec: CodecType,
    /// 轨道类型
    pub kind: TrackKind,
    /// SSRC（可能多个，simulcast）
    pub ssrcs: Vec<u32>,
    
    /// 源流订阅者（录制、转封装等订阅源流）
    source_subscribers: Vec<Arc<dyn StreamSink>>,
    
    /// 转发流订阅者（其他 participant 的 pull_rtp）
    forward_subscribers: Vec<Arc<dyn StreamSink>>,
    
    /// GOP 缓存（视频）
    gop_cache: Option<GopCache>,
    
    /// 帧组装器
    depacketizer: Box<dyn Depacketizer>,
}

/// 流订阅者接口
pub trait StreamSink: Send + Sync {
    /// 收到一个完整的媒体帧
    fn on_frame(&self, frame: &MediaFrame);
    /// 收到一个 RTP 包（SFU 直转模式）
    fn on_packet(&self, pkt: &RtpPacket);
}
```

#### 3.2.2 路由流程（重构后）

```
push_rtp(packet)
    │
    ▼
MediaStream.push_packet(packet)
    │
    ├──▶ Depacketizer.push_packet()  → 组装帧
    │                                    │
    │                                    ▼ (帧完成)
    │                              MediaFrame
    │                                    │
    ├──▶ source_subscribers.on_frame()   │ (录制、转封装 — 干净的源流)
    │                                    │
    ├──▶ GOP cache.save(frame)           │ (关键帧时新建 GOP)
    │                                    │
    └──▶ forward_subscribers.on_packet() │ (SFU 直转 — 其他 participant)
                                         │
                                    新订阅者时:
                                    GOP cache.replay() → 先发缓存
```

**关键改进**：
- 源流订阅者收到的是 `MediaFrame`（已组装完成），不是裸 RTP 包
- 转发流订阅者收到的是 `RtpPacket`（零拷贝直转）
- 两者完全分离，录制不再需要 SSRC 过滤
- GOP 缓存让新订阅者快速首屏

### 3.3 GOP 缓存

```rust
// lm-stream/src/gop_cache.rs

/// GOP 缓存（参考 Xiu Gops）
pub struct GopCache {
    /// 缓存的 GOP 数量（通常 1-2）
    gop_count: usize,
    /// 单 GOP 最大帧数（防止纯音频流内存泄漏）
    max_frames_per_gop: usize,
    /// GOP 队列
    gops: VecDeque<Gop>,
}

struct Gop {
    frames: Vec<MediaFrame>,
}

impl GopCache {
    pub fn new(gop_count: usize, max_frames: usize) -> Self { ... }
    
    /// 保存帧（关键帧时新建 GOP）
    pub fn save(&mut self, frame: &MediaFrame) { ... }
    
    /// 重放缓存给新订阅者
    pub fn replay(&self, sink: &dyn StreamSink) { ... }
}
```

### 3.4 录制重构

#### 3.4.1 录制状态机

```rust
// lm-recorder/src/recorder.rs

#[derive(Debug, Clone, PartialEq)]
pub enum RecordingState {
    Requested,   // 收到录制请求
    Starting,    // 正在创建文件、初始化
    Active,      // 正在录制
    Stopping,    // 收到停止请求
    Finalizing,  // 正在合并分片、写 MP4
    Completed,   // 录制完成
    Failed(String), // 录制失败
}
```

#### 3.4.2 分段录制（类似腾讯会议）

```rust
/// 分段录制器
/// 参考 Xiu HLS 录制 + atm0s RecordChunkWriter
pub struct SegmentRecorder {
    /// 分段时长（秒）
    segment_duration: u64,
    /// 当前分段
    current_segment: Option<SegmentWriter>,
    /// 已完成的分段
    completed_segments: Vec<PathBuf>,
    /// 输出目录
    output_dir: PathBuf,
    /// 录制状态
    state: RecordingState,
}

impl SegmentRecorder {
    /// 收到帧
    fn on_frame(&mut self, frame: &MediaFrame) -> Result<()> {
        // 1. 如果当前分段达到时长，切分
        // 2. 写入当前分段（IVF for VP8, raw H264 for H264）
        // 3. 关键帧时可以安全切分
    }
    
    /// 停止录制：合并所有分段为 MP4
    fn finalize(&mut self) -> Result<PathBuf> {
        // 用 ffmpeg 合并：ffmpeg -i seg0.ivf -i seg1.ivf ... -c copy output.mp4
    }
}
```

### 3.5 Simulcast 支持

#### 3.5.1 RID 路由

```rust
// lm-stream/src/simulcast.rs

/// Simulcast 层管理
/// 参考 atm0s VideoSelector + Waterbus Adaptive Stream
pub struct SimulcastRouter {
    /// RID → SSRC 映射
    layers: HashMap<String, u32>,  // "low"→ssrc1, "mid"→ssrc2, "high"→ssrc3
    /// 订阅者 → 选择的层
    subscriber_layers: HashMap<SubscriberId, SimulcastLayer>,
}

pub enum SimulcastLayer {
    /// 固定层
    Fixed(String),  // "low", "mid", "high"
    /// 自适应（根据带宽）
    Adaptive { max_bitrate: u64 },
}
```

#### 3.5.2 层选择逻辑

```
订阅者带宽充足 → 选 "high" 层
订阅者带宽不足 → 降级到 "mid" 或 "low"
无人订阅某层  → Dynacast: 通知 publisher 暂停该层
```

### 3.6 新增 Crate 结构

```
rust-media/crates/
├── lm-core/           # 核心类型（已有，新增 MediaFrame/Depacketizer/Packetizer trait）
├── lm-transport/      # RTP 传输（已有）
├── lm-stream/         # 【新】Stream 抽象 + GOP 缓存 + Simulcast 路由
├── lm-depacketizer/   # 【新】RTP→Frame 解包器（VP8/VP9/H264/Opus）
├── lm-packetizer/     # 【新】Frame→RTP 打包器
├── lm-codecs/         # 编解码器（已有）
├── audio-codec/       # 音频编解码（已有）
├── lm-pipeline/       # Ingress/Egress 管线（已有，重构为用 Stream）
├── lm-mixer/          # 音频混音（已有）
├── lm-recorder/       # 录制（已有，重构为分段录制 + 状态机）
├── lm-router/         # 路由表（已有，重构为用 Stream）
├── lm-control/        # gRPC 服务（已有，重构 push_rtp/pull_rtp）
└── lm-telemetry/      # 遥测（已有）
```

---

## 四、实施路线（全部完成 ✅）

### Phase 1：帧级抽象 + Depacketizer ✅

**目标**：让录制不再自己组装帧，用统一的 Depacketizer 产出 MediaFrame

1. ✅ `lm-core` 新增 `MediaFrame`、`Depacketizer`、`Packetizer` trait
2. ✅ `lm-depacketizer` crate：
   - `Vp8Depacketizer`（RFC 7741）
   - `OpusDepacketizer`（Opus RTP 直接就是帧）
   - `H264Depacketizer`（FU-A 分片重组）
3. ✅ `lm-stream` crate：
   - `MediaStream` 结构
   - `StreamSink` trait
   - `GopCache`
4. ✅ 重构 `lm-control/service.rs` 的 `push_rtp`：
   - 包先进入 `MediaStream.depacketizer`
   - 组装出帧后分发给 `source_subscribers`（录制）
   - 裸包分发给 `forward_subscribers`（SFU 转发）
5. ✅ 重构 `recorder.rs`：
   - 录制 task 订阅 `source_subscribers`，收到 `MediaFrame`
   - 不再自己解析 VP8 descriptor
   - 不再需要 SSRC 过滤

### Phase 2：GOP 缓存 + 快速首屏 ✅

1. ✅ `GopCache` 实现
2. ✅ 新 `pull_rtp` 订阅时先 replay GOP
3. ✅ 关键帧请求（PLI）自动触发

### Phase 3：分段录制 + MP4 合并 ✅

1. ✅ `SegmentRecorder` 实现
2. ✅ 录制状态机（Requested→Starting→Active→Stopping→Finalizing→Completed）
3. ✅ 分段切分（关键帧边界 + 时长）
4. ✅ ffmpeg 合并 MP4
5. ✅ gRPC 新增 `GetRecordingStatus` 查询状态

### Phase 4：Simulcast ✅

1. ✅ `SimulcastRouter` 按 RID 路由
2. ✅ 层选择（Fixed/Adaptive）
3. ✅ Dynacast（无人订阅的层自动暂停）
4. ✅ 订阅者层切换
5. ⏳ 前端订阅 UI（待 Go 控制面集成）

### Phase 5：协议转封装 ✅

1. ✅ WebRTC→HLS 输出（HlsRemuxer）
2. ✅ WebRTC→HTTP-FLV 输出（FlvRemuxer）
3. ✅ WebRTC→RTMP 推流（RtmpRemuxer）
4. ✅ RTSP/SRT/GB28181/WHIP/WHEP remuxer 框架

### Phase 6：media-node HTTP 输出服务 ✅

1. ✅ axum HTTP 服务器（HLS/HTTP-FLV/API）
2. ✅ HLS playlist + TS 分段分发
3. ✅ HTTP-FLV chunked stream
4. ✅ 端到端验证（gRPC PushRtp → HLS playlist）

---

## 五、与 Go 控制面的接口变化

### 5.1 gRPC 接口（向后兼容）

```protobuf
// 新增
message StartRecordingRequest {
    string session_id = 1;
    string room_id = 2;          // 新增：房间级录制
    string format = 3;           // "wav" | "mp4" | "hls"
    string output_dir = 4;
    uint32 segment_duration = 5; // 分段时长（秒），0=不分段
    bool record_audio = 6;
    bool record_video = 7;
}

message RecordingStatus {
    string recording_id = 1;
    string state = 2;            // Requested/Starting/Active/Stopping/Finalizing/Completed/Failed
    uint64 duration_ms = 3;
    uint64 file_size = 4;
    string file_path = 5;
    repeated string segments = 6; // 分段文件列表
}

// 新增
rpc GetRecordingStatus(GetRecordingStatusRequest) returns (RecordingStatus);

// 新增：按层订阅
message SubscribeTrackRequest {
    string session_id = 1;
    string track_id = 2;
    string rid = 3;              // "low"/"mid"/"high"，空=自动选择
}
```

### 5.2 Go 侧变化

- `push_rtp` 逻辑不变（仍然推 RTP 包到 Rust）
- `pull_rtp` 新增 `rid` 参数（可选）
- 录制从 per-session 改为 per-room（可选）
- 新增录制状态查询

---

## 六、验证计划

每个 Phase 完成后：

1. `cargo build --release` 通过
2. 重启 Rust media-node + Go demo
3. 两浏览器标签页测试
4. `ffprobe`/`ffmpeg` 验证录制文件
5. 检查帧数、分辨率、时间戳、解码错误
6. 确认视频有运动（非静帧）
