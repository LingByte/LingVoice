# 13 - 大规模会议架构设计

## 问题

当前纯 SFU 转发架构是 O(N²)：

```
N=10:  每人 1 push + 9 pull  = 10×10  = 100   gRPC stream
N=50:  每人 1 push + 49 pull = 50×50  = 2500  gRPC stream
N=100: 每人 1 push + 99 pull = 100×100 = 10000 gRPC stream
```

每个 RTP 包要被复制 N-1 份广播。压测验证：50 人时 push throughput 从 497pps 降到 1096pps（目标 2500），100 人时更差。

## 目标

| 规模 | 方案 | 音频连接数 | 视频连接数 |
|------|------|-----------|-----------|
| ≤8 人 | 纯 SFU 转发（当前） | O(N²) 但 N 小 | O(N²) 但 N 小 |
| 8-100 人 | 音频混音 + 视频按需订阅 | O(N) | O(N×K), K≤4 |
| 100+ 人 | 分片 SFU + 主发言者模式 | O(N) | O(N) |

---

## Phase 1: 音频混音（8-100 人）

### 核心思路

```
当前 (SFU):
  A push → Rust broadcast → B pull (A的音频)
                        → C pull (A的音频)
                        → D pull (A的音频)
  B push → Rust broadcast → A pull (B的音频)
                        → C pull (B的音频)
                        → D pull (B的音频)
  ...N²路连接

目标 (SFU + Mixer):
  A push Opus → Rust 解码→PCM → Mixer ─┐
  B push Opus → Rust 解码→PCM → Mixer ─┤
  C push Opus → Rust 解码→PCM → Mixer ─┤─→ 混音 → 编码Opus → A pull (1路)
  D push Opus → Rust 解码→PCM → Mixer ─┘─→ 混音 → 编码Opus → B pull (1路)
                                        ─→ 混音 → 编码Opus → C pull (1路)
                                        ─→ 混音 → 编码Opus → D pull (1路)
  N路push + N路pull = O(N)
```

### 混音策略：主发言者 + Top-K

不全混所有人（CPU 随 N 线性增长），只混音量最大的 K 路：

```
每 20ms tick:
  1. 对所有参与者的 PCM 帧做 VAD 能量检测
  2. 按能量排序，取 Top-K (K=5)
  3. 只混这 K 路 PCM
  4. 其他人静音
  5. 编码混音 → 每人 pull 1 路
```

CPU 恒定（不随 N 增长），音质好（只混有声音的人）。

### 数据流

```
                        ┌─────────────────────────────────────┐
                        │           Rust 媒体节点              │
                        │                                     │
  A push Opus ──────────┤→ IngressPipeline ─→ PCM ─┐          │
                        │                          │          │
  B push Opus ──────────┤→ IngressPipeline ─→ PCM ─┤          │
                        │                          │          │
  C push Opus ──────────┤→ IngressPipeline ─→ PCM ─┤          │
                        │                          ↓          │
                        │              ┌─── ConferenceMixer ───┤
                        │              │   (Top-K VAD 混音)    │
                        │              │                       │
                        │              │   N-1 混音输出:        │
                        │              │   A收: B+C+D (除A)    │
                        │              │   B收: A+C+D (除B)    │
                        │              │   C收: A+B+D (除C)    │
                        │              │   D收: A+B+C (除D)    │
                        │              └───────────────────────┤
                        │                          │          │
                        │              EgressPipeline          │
                        │              PCM → 编码Opus          │
                        │                          │          │
  A pull Opus ◄─────────┤──────────────────────────┘          │
  B pull Opus ◄─────────┤──────────────────────────┘          │
  C pull Opus ◄─────────┤──────────────────────────┘          │
  D pull Opus ◄─────────┤──────────────────────────┘          │
                        └─────────────────────────────────────┘
```

### 现有基础设施（已实现，可复用）

| 组件 | 状态 | 位置 |
|------|------|------|
| Opus 解码器 | ✅ 完整 | `audio-codec/src/opus.rs` OpusDecoder |
| Opus 编码器 | ✅ 完整 | `audio-codec/src/opus.rs` OpusEncoder |
| PCMU 编码器 (µ-law 查表) | ✅ 完整 | `audio-codec/src/pcmu.rs` linear_to_ulaw |
| IngressPipeline (RTP→解码→PCM) | ✅ 完整 | `lm-pipeline/src/lib.rs` |
| EgressPipeline (PCM→编码→Pacer) | ✅ 完整 | `lm-pipeline/src/lib.rs` |
| ConferenceMixer (N-1 混音 + Top-K) | ✅ 完整 | `lm-mixer/src/lib.rs` |
| VadDetector (能量VAD) | ✅ 完整 | `lm-dsp/src/lib.rs` |
| proto Mix 接口 | ✅ 已定义 | `proto/media_node.proto` |
| lm-control Mix gRPC | ✅ 完整实现 | `lm-control/src/service.rs` |
| Go rustbridge Mix 方法 | ✅ 完整 | `pkg/media/rustbridge/client.go` |
| Go demo 混音调用 | ✅ 完整 | `cmd/webrtc-rust-demo/main.go` |
| Top-K VAD 混音优化 | ✅ 完整 | `lm-mixer/src/lib.rs` with_max_speakers |
| 可配置输出编码 (Opus/PCMU) | ✅ 完整 | `lm-control/src/mixer.rs` run_egress_bridge |
| 静音跳过 (silence skip) | ✅ 完整 | `lm-control/src/mixer.rs` run_egress_bridge |
| 零拷贝混音 (zero-clone mix) | ✅ 完整 | `lm-mixer/src/lib.rs` mixing_loop |
| LingEdge 混音压测 | ✅ 完整 | `LingEdge/src/protocol/media.rs` |

### 需要实现的工作

#### 1. Rust: lm-control 混音 gRPC 实现

将 stub 替换为真实实现，连接 push_rtp → IngressPipeline → ConferenceMixer → EgressPipeline → pull_rtp。

**关键设计：双模式路由**

push_rtp 收到音频包时，根据 session 是否在某个 mix 中决定路由：

```rust
// service.rs push_rtp 热路径
if let Some(mix_id) = self.sessions.get_mix_for_track(&session_id, &track_id) {
    // 混音模式：解码 → PCM → mixer input
    let pcm = ingress_pipeline.handle_rtp(&packet)?;
    mixer_input_tx.send(pcm).await?;
} else {
    // SFU 模式：纯转发（当前行为，≤8人房间或视频）
    let peer_tracks = get_room_peer_tracks_by_kind(...);
    for peer in peer_tracks {
        peer.rtp_broadcast.send(packet.clone());
    }
}
```

**StartMix 实现**:

```rust
async fn start_mix(&self, req: StartMixRequest) -> StartMixResponse {
    let mix_id = uuid::Uuid::new_v4().to_string();
    let mixer = Arc::new(ConferenceMixer::new(&mix_id, req.sample_rate));
    mixer.start();
    
    // 存入 mixers map
    self.mixers.insert(mix_id.clone(), MixerState {
        mixer,
        room_id: req.room_id.clone(),
        participants: DashMap::new(),  // session_id → (ingress, egress, mixer_channels)
    });
    
    StartMixResponse { mix_id }
}
```

**AddMixParticipant 实现**:

```rust
async fn add_mix_participant(&self, req: AddMixParticipantRequest) {
    let mixer_state = self.mixers.get(&req.mix_id)?;
    let session = self.sessions.get_session(&req.session_id)?;
    
    // 1. 创建该参与者的 IngressPipeline（Opus → PCM）
    let (pcm_tx, pcm_rx) = mpsc::channel(100);
    let ingress = IngressPipeline::new(CodecType::Opus, pcm_tx);
    
    // 2. 创建该参与者的 EgressPipeline（PCM → Opus）
    let (mixed_pcm_tx, mixed_pcm_rx) = mpsc::channel(100);
    let (opus_tx, opus_rx) = mpsc::channel(100);
    let egress = EgressPipeline::new(CodecType::Opus, 48000, mixed_pcm_rx, opus_tx);
    tokio::spawn(egress.run());
    
    // 3. 注册到 ConferenceMixer
    let (mixer_input_tx, mixer_output_rx) = mixer.add_participant(&req.session_id).await?;
    
    // 4. 桥接：ingress PCM → mixer input, mixer output → egress input
    // ingress pcm_rx → mixer_input_tx
    tokio::spawn(async move {
        while let Some(frame) = pcm_rx.recv().await {
            if mixer_input_tx.send(frame).await.is_err() { break; }
        }
    });
    // mixer output_rx → egress mixed_pcm_tx
    tokio::spawn(async move {
        while let Some(frame) = mixer_output_rx.recv().await {
            if mixed_pcm_tx.send(frame).await.is_err() { break; }
        }
    });
    
    // 5. egress opus_rx → 该 session 的 pull_rtp broadcast
    // 注册一个特殊的 "mix-audio" track，pull_rtp 订阅它
    let mix_track_id = format!("mix-{}", req.session_id);
    // ... 创建 track，将 opus_rx 的数据写入 track 的 broadcast
    
    mixer_state.participants.insert(req.session_id, ParticipantState {
        ingress, mix_track_id, ...
    });
}
```

#### 2. Rust: Top-K VAD 混音优化

当前 `ConferenceMixer` 混所有人。改为只混 Top-K：

```rust
// lm-mixer mixing_loop 修改
async fn mixing_loop(ctx: MixingLoopContext) {
    let mut vad_detectors: HashMap<ParticipantId, VadDetector> = ...;
    
    loop {
        // 1. 收集所有 PCM 帧 + VAD 能量
        let mut energies: Vec<(ParticipantId, f32)> = Vec::new();
        for (pid, frame) in &participant_audio {
            let vad_result = vad_detectors.entry(pid).or_default().detect(frame);
            if vad_result.speech {
                energies.push((pid.clone(), vad_result.energy));
            }
        }
        
        // 2. Top-K 选择
        energies.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap());
        let active_speakers: HashSet<&ParticipantId> = energies.iter()
            .take(ctx.max_speakers)  // K=5
            .map(|(pid, _)| pid)
            .collect();
        
        // 3. 只混 active_speakers 的音频
        for output_pid in &participant_ids {
            let input_frames: Vec<_> = participant_audio.iter()
                .filter(|(pid, _)| pid != output_pid && active_speakers.contains(*pid))
                .map(|(_, frame)| frame.samples.clone())
                .collect();
            // ... 混音 + 发送
        }
    }
}
```

#### 3. Go: rustbridge 添加 Mix 方法

```go
// pkg/media/rustbridge/client.go

func (c *Client) StartMix(ctx context.Context, roomID string, sampleRate uint32, frameSize uint32) (string, error) {
    resp, err := c.stub.StartMix(ctx, &mediav1.StartMixRequest{
        RoomId:     roomID,
        SampleRate: sampleRate,
        FrameSize:  frameSize,
    })
    if err != nil {
        return "", err
    }
    return resp.MixId, nil
}

func (c *Client) AddMixParticipant(ctx context.Context, mixID, sessionID, trackID string, muted bool) error {
    _, err := c.stub.AddMixParticipant(ctx, &mediav1.AddMixParticipantRequest{
        MixId:      mixID,
        SessionId:  sessionID,
        TrackId:    trackID,
        Muted:      muted,
    })
    return err
}

func (c *Client) RemoveMixParticipant(ctx context.Context, mixID, sessionID string) error { ... }
func (c *Client) StopMix(ctx context.Context, mixID string) error { ... }
func (c *Client) SetMixGain(ctx context.Context, mixID, srcSession, dstSession string, gain float32) error { ... }
```

#### 4. Go: WebRTC demo 混音模式切换

当 room 人数 > 阈值（如 8 人）时，自动切换到混音模式：

```go
// cmd/webrtc-rust-demo/main.go

const audioMixThreshold = 8  // 超过 8 人启用混音

func (h *rustHandler) onParticipantJoin(sessionID, roomID string) {
    count := h.countRoomParticipants(roomID)
    if count == audioMixThreshold {
        // 触发混音模式
        mixID, _ := h.bridge.StartMix(ctx, roomID, 48000, 960)
        h.roomMixs[roomID] = mixID
        
        // 将所有现有音频 participant 加入 mix
        for sid := range h.getRoomSessions(roomID) {
            h.bridge.AddMixParticipant(ctx, mixID, sid, "audio", false)
        }
    } else if count > audioMixThreshold {
        // 新参与者直接加入 mix
        mixID := h.roomMixs[roomID]
        h.bridge.AddMixParticipant(ctx, mixID, sessionID, "audio", false)
    }
}
```

### 预期性能

| 场景 | 当前 (SFU) | 目标 (Mixer) |
|------|-----------|-------------|
| 10人音频 | 100 stream, 900 pps | 20 stream, 500 pps |
| 50人音频 | 2500 stream, ~1096 pps | 100 stream, 2500 pps |
| 100人音频 | 10000 stream, ~649 pps | 200 stream, 5000 pps |
| CPU | 随 N² 增长（纯转发） | 随 N 线性增长（解码+混音+编码），Top-K 恒定混音部分 |

---

## Phase 2: 视频按需订阅（8-100 人）

### 核心思路

```
当前 (SFU 全订阅):
  A push video → Rust → B pull (A的高清)
                      → C pull (A的高清)
                      → D pull (A的高清)
  每人收 N-1 路高清视频 = O(N²) 带宽

目标 (Simulcast + 按需订阅):
  A push 3层 (low/mid/high) → Rust
  B 订阅 A=high (A在说话), C=low, D=low
  C 订阅 A=mid, B=low, D=low
  每人收 K 路视频 (K≤4) = O(N×K) 带宽
```

### Simulcast 层

```
low:  180p  15fps  ~150kbps   (RID: "q")
mid:  360p  30fps  ~500kbps   (RID: "h")
high: 720p  30fps  ~1500kbps  (RID: "f")
```

### 订阅策略

```
发言者 (dominant speaker): 高清 (high)
其他可见参与者: 低清 (low)
不可见参与者: 不订阅

可见性规则:
  - 最多显示 4 路视频 (2×2 grid)
  - 当前发言者占 1 个大窗
  - 最近发言的 3 人占小窗
  - 其他人不订阅
```

### 现有基础设施

| 组件 | 状态 |
|------|------|
| Pion simulcast RID 接收 | ✅ publisher.go 已读取 RID |
| proto RtpPacket.rid 字段 | ✅ 已定义 |
| Go TrackInfo.Layers 字段 | ✅ 已定义，但未填充 |
| Rust 按 RID 路由 | ❌ 需实现 |
| Go 按 RID 订阅 | ❌ 需实现 |
| 前端 simulcast 发送 | ❌ 需实现 |
| 前端按需订阅 UI | ❌ 需实现 |

### 需要实现的工作

#### 1. 前端：发送 simulcast

```javascript
// testpage.html
const sender = pc.addTransceiver(track, {
  sendEncodings: [
    { rid: "q", maxBitrate: 150000, maxFramerate: 15, scaleResolutionDownBy: 4 },
    { rid: "h", maxBitrate: 500000, maxFramerate: 30, scaleResolutionDownBy: 2 },
    { rid: "f", maxBitrate: 1500000, maxFramerate: 30 },
  ]
});
```

#### 2. Rust：按 RID 路由

```rust
// session.rs TrackState 增加 per-RID sub-track
pub struct TrackState {
    // ... 现有字段
    pub simulcast_layers: DashMap<String, Arc<TrackState>>,  // RID → sub-track
}

// service.rs push_rtp 按 RID 分流
if !packet.rid.is_empty() {
    // simulcast: 路由到对应 RID 的 sub-track
    if let Some(layer_track) = track.simulcast_layers.get(&packet.rid) {
        layer_track.rtp_broadcast.send(packet);
    }
}
```

#### 3. Go：按需订阅 + 层切换

```go
// 新增 RPC: SubscribeTrack(sessionID, trackID, rid) 
// 让 Go 告诉 Rust "我要订阅 A 的 mid 层"

// 层切换逻辑
func (h *rustHandler) onDominantSpeakerChange(roomID, speakerSessionID string) {
    // 发言者 → high
    h.setSubscriptionLayer(speakerSessionID, "f")
    // 其他人 → low
    for _, sid := range h.getRoomSessions(roomID) {
        if sid != speakerSessionID {
            h.setSubscriptionLayer(sid, "q")
        }
    }
}
```

#### 4. proto 新增

```protobuf
message SubscribeTrackRequest {
  string session_id = 1;      // 订阅者 session
  string track_id = 2;        // 要订阅的 track
  string rid = 3;             // simulcast 层 ("q"/"h"/"f")，空=非simulcast
}

message SubscribeTrackResponse {}
```

---

## Phase 3: 分片 SFU + 主发言者模式（100+ 人）

### 核心思路

```
单个 Rust 节点扛不住 100+ 人的混音+转发。
拆成多个 Rust 节点，每个节点负责一部分参与者。

  ┌──────────────┐
  │ Rust Node 1  │  ← 参与者 A,B,C,...,M (50人)
  │ 混音 + 转发   │
  └──────┬───────┘
         │ 节点间级联（只传混音流 + 主发言者视频）
  ┌──────┴───────┐
  │ Rust Node 2  │  ← 参与者 N,O,P,...,Z (50人)
  │ 混音 + 转发   │
  └──────────────┘

  Node1 的混音 → Node2 的参与者听到
  Node2 的混音 → Node1 的参与者听到
  只传 1 路混音 + 1 路主发言者视频，不是 N 路
```

### 节点间级联

```
Node1 (50人):
  - 本地混音: A+B+...+M → mixed_1
  - 发送 mixed_1 到 Node2
  - 接收 Node2 的 mixed_2
  - 本地参与者听到: mixed_1(自己人) + mixed_2(Node2的人)

Node2 (50人):
  - 本地混音: N+O+...+Z → mixed_2
  - 发送 mixed_2 到 Node1
  - 接收 Node1 的 mixed_1
  - 本地参与者听到: mixed_2(自己人) + mixed_1(Node1的人)
```

### 主发言者视频转发

```
Node1 检测到 A 是主发言者:
  - 将 A 的高清视频转发到 Node2
  - Node2 的参与者看到 A 的视频

Node2 检测到 N 是主发言者:
  - 将 N 的高清视频转发到 Node1
  - Node1 的参与者看到 N 的视频
```

### 需要

- Rust 节点间 gRPC 级联（已有 proto BridgeSessions，需扩展为 room 级别）
- Go 控制面做 room 分配和节点选择
- 负载均衡：新参与者加入人数最少的节点
- 主发言者检测：基于 VAD 能量，跨节点聚合

---

## 实现优先级

```
Phase 1 (音频混音)
  ├── 1a. lm-control 混音 gRPC 真实实现          ✅ 完成
  ├── 1b. Go rustbridge 添加 Mix 方法            ✅ 完成
  ├── 1c. Go demo 混音模式自动切换               ✅ 完成
  ├── 1d. Top-K VAD 混音优化                    ✅ 完成
  ├── 1e. 压测验证 50/100 人音频                 ✅ 完成
  └── 1f. 混音性能优化 (多策略)                  ✅ 完成

Phase 2 (视频按需订阅)
  ├── 2a. 前端 simulcast 发送
  ├── 2b. Rust 按 RID 路由
  ├── 2c. Go 按需订阅 + 层切换
  ├── 2d. proto 新增 SubscribeTrack
  └── 2e. 前端订阅 UI

Phase 3 (分片 SFU)
  ├── 3a. Rust 节点间级联
  ├── 3b. Go room 分配 + 负载均衡
  ├── 3c. 跨节点主发言者检测
  └── 3d. 分布式压测
```

## Phase 1 压测结果

### 测试环境

- **媒体节点**: Rust media-node (release 模式), macOS Darwin 21.6.0
- **压测工具**: LingEdge `media` 子命令 (release 模式)
- **参数**: 50pps/session (Opus 20ms), 10s 持续, 160 bytes/packet (PCMU) 或 960 bytes (Opus 48kHz)
- **混音输入**: PCMU 8kHz (raw payload 可直接 µ-law 解码)
- **Top-K**: K=5, energy=varied (线性振幅梯度, session 0=最响→session N-1=最轻)

### 优化策略

| 策略 | 描述 | 实现位置 |
|------|------|----------|
| Top-K VAD | 每 tick 只混能量最高的 K 路 PCM, 300ms hysteresis hold | `lm-mixer/src/lib.rs` mixing_loop |
| 可配置输出编码 | Opus (浏览器) 或 PCMU (SIP/极致性能, µ-law 查表零成本) | `lm-control/src/mixer.rs` run_egress_bridge |
| 静音跳过 | RMS < 50 时不编码不发送, 只推进时间戳 | `lm-control/src/mixer.rs` run_egress_bridge |
| 零拷贝混音 | 直接用引用混音到预分配 buffer, 消除 N×K Vec clone/tick | `lm-mixer/src/lib.rs` mixing_loop |

### 50 人会议

| 模式 | Push pps | Recv pps | Recv Mbps | Delivery | 带宽 vs SFU |
|------|----------|----------|-----------|----------|-------------|
| SFU (N²转发) | 2501 | 102,031 | 130.6 | 83.3% | 1× (基准) |
| Mix-Opus (全混音) | 2503 | 1,893 | 2.4 | 75.6% | **54.4× 节省** |
| Mix-PCMU (全混音) | 2500 | 1,861 | 2.4 | 74.5% | **54.4× 节省** |
| TopK-Opus K=5 | 2503 | 2,124 | 2.7 | **84.8%** | **48.4× 节省** |
| TopK-PCMU K=5 | 2501 | 2,102 | 2.7 | 84.0% | **48.4× 节省** |

### 100 人会议

| 模式 | Push pps | Recv pps | Recv Mbps | Delivery | 带宽 vs SFU |
|------|----------|----------|-----------|----------|-------------|
| SFU (N²转发) | 4938 | 395,845 | 506.7 | 81.0% | 1× (基准) |
| Mix-Opus (全混音) | 4997 | 2,473 | 3.2 | 49.5% | **158× 节省** |
| Mix-PCMU (全混音) | 4997 | 2,743 | 3.5 | 54.9% | **145× 节省** |
| TopK-Opus K=5 | 5001 | 3,883 | 5.0 | **77.6%** | **101× 节省** |
| TopK-PCMU K=5 | 5000 | 3,862 | 4.9 | 77.3% | **103× 节省** |

### 关键发现

1. **SFU 带宽爆炸**: 100 人 SFU 需要 506.7 Mbps (O(N²)=9900 路转发), 生产环境不可行
2. **Top-K 显著优于全混音**: 100 人时 Top-K delivery 77.6% vs 全混音 49.5%, 因为 mixing loop 只处理 5 路而非 100 路
3. **带宽节省巨大**: TopK 100 人仅 5.0 Mbps vs SFU 506.7 Mbps = **101× 带宽节省**
4. **PCMU vs Opus**: PCMU 在全混音模式有轻微优势 (54.9% vs 49.5%), Top-K 模式差异不大 (77.3% vs 77.6%)
5. **瓶颈分析**: 100 人 Top-K 的 77.6% delivery 瓶颈在于 100 个独立 egress bridge task 的调度开销, 非编码本身
6. **静音跳过有效**: 无 active speaker 的目的地跳过编码, 减少 CPU 浪费

### 结论与下一步

- **8-100 人会议**: Top-K (K=5) + Opus 输出是最佳方案, delivery >77%, 带宽节省 100×
- **SIP 场景**: 使用 PCMU 输出, 零成本编码, 适合内部通信
- **100+ 人**: 需要进入 Phase 3 (分片 SFU), 单节点 100 人 Top-K 已接近极限
- **进一步优化方向**: 单任务批量编码 (消除 100 个 task 调度), 共享编码器池 (极限模式, 牺牲音质)

## 不做的事

- 不在 SFU 路径上做视频混流（把多路视频拼成 1 路）—— CPU 开销不可接受
- 不用纯 MCU 替换 SFU —— SFU 对小房间更高效
- 不在 Phase 1 引入神经网络 VAD —— 能量阈值法够用，后续可替换 Silero
