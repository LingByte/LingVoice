# 07 — 媒体管线：ptime 节奏器 + 多订阅者出口

> **架构变更说明**：本文档原为"pub/sub Track 推拉流架构"，后修订为**混合模型**——ptime 节奏器 + 零拷贝 RTP 直转 + pub/sub 的多订阅者 fan-out。原 pub/sub Track 设计作为参考保留在附录中。
>
> **实施状态**：当前实现采用 `MediaStream` + `StreamSink` 抽象（见 [14-streaming-media-redesign.md](./14-streaming-media-redesign.md)），源流订阅者收到 `MediaFrame`，转发流订阅者收到裸 `RtpPacket`，两者完全分离。

## 设计决策

媒体管线架构确定为**混合模型**：

| 来源 | 保留/吸收 | 理由 |
|------|-----------|------|
| ptime 节奏器 | ✅ 保留 | 电话音频是连续流，20ms tick 必出帧（静音兜底），远端解码器不断流 |
| RewriteRelay 零拷贝 | ✅ 保留 | 同 codec 直转，改 RTP header 不进 PCM 域 |
| 互斥单出口 | ❌ 改造 | 一条腿同一时刻只能一种模式，不能同时中继+录音+ASR |
| pub/sub 多订阅者 fan-out | ✅ 吸收 | 同时中继+录音+ASR+推流 |
| pub/sub 动态加/减订阅者 | ✅ 吸收 | subscribe/unsubscribe 热变，不中断当前路径 |
| pub/sub 各自背压 | ✅ 吸收 | 录音 Block（不丢），ASR/混音 DropOldest（实时优先） |
| pub/sub 数据流驱动 | ❌ 不采用 | ptime 定时器驱动的强节奏保证更适合电话音频 |

**核心结论：定时器驱动 + 多订阅者出口（pub/sub 的方式）。**

## 改造后的 EgressPipeline

```
ptime 节奏器 (20ms tick, 必出帧, 静音兜底)  ← 强节奏保证
    │
    ▼
当前帧 (PCM 或 RTP)
    │
    ├──▶ RelaySubscriber     (零拷贝直转, 同 codec 时, 不进 PCM 域)
    ├──▶ TranscodeSubscriber (解码→重采样→编码, 异 codec 时)
    ├──▶ RecorderSubscriber  (写文件, Block 背压)
    ├──▶ AsrSubscriber       (推 PCM 给 ASR, DropOldest)
    └──▶ StreamSubscriber    (推流 RTMP/WHIP, DropOldest)
```

### 互斥单出口 vs 多订阅者

```
互斥单出口 (同一时刻一种):
  Leg ──▶ EgressPipeline ──▶ [RewriteRelay | TranscodePeer | Media | Inject | Silence]
  // 一条腿只能做一件事

多订阅者 (同时):
  Leg ──▶ EgressPipeline ──┬──▶ RelaySubscriber     (零拷贝直转)
                           ├──▶ TranscodeSubscriber (异 codec 转码)
                           ├──▶ RecorderSubscriber  (录音)
                           ├──▶ AsrSubscriber       (推 ASR)
                           └──▶ StreamSubscriber    (推流)
```

### TranscodeState 持久转码器（已实现 ✅）

> **2026-08-26 新增**：跨协议转码已在 `lm-control/src/service.rs` 中实现，使用 `TranscodeState` 持久化转码器状态。

**核心问题**：Opus decoder/encoder 有内部帧间预测状态，如果每个 RTP 包都创建新的 decoder/encoder，会丢失预测状态 → 杂音（滋滋声）。Resampler 的历史缓冲区也需要跨帧保持。

**解决方案**：在 `push_rtp` 流生命周期内，按 peer track ID 缓存 `TranscodeState`：

```rust
struct TranscodeState {
    decoder: Option<Box<dyn audio_codec::Decoder>>,  // 源 codec → PCM
    encoder: Option<Box<dyn audio_codec::Encoder>>,  // PCM → 目标 codec
    resampler: Option<audio_codec::BoxedResampler>,  // 采样率转换（持久历史缓冲）
    src_sample_rate: u32,
    dst_sample_rate: u32,
}
```

**转码流程**：
```
源 RTP 包 →
  1. 解码到 PCM（Opus/PCMU/PCMA → PCM samples，用持久 decoder）
  2. 重采样（如果采样率不同，用持久 resampler 保持历史缓冲）
  3. 编码到目标 codec（PCM → Opus/PCMU/PCMA/PCM16，用持久 encoder）
  4. 发送到 peer track 的 broadcast channel
```

**支持的转码矩阵**：Opus ↔ PCM16 ↔ PCMU ↔ PCMA（全部双向支持）
  // 一条腿同时做多件事
```

## 订阅者接口

```rust
/// Egress 订阅者：每 tick 收到一帧，各自处理。
/// 互斥单出口 → 订阅者列表。
pub trait EgressSubscriber: Send + Sync {
    /// 每个 tick 调用一次。frame 可能是 RTP（RelaySubscriber 用）或 PCM（其他用）。
    fn on_frame(&mut self, frame: &EgressFrame) -> Result<()>;

    /// 背压策略：节奏器据此决定满时行为。
    fn backpressure(&self) -> Backpressure;

    /// 订阅者类型标识（诊断/路由用）。
    fn kind(&self) -> SubscriberKind;
}

pub enum EgressFrame {
    /// RTP 层帧（RelaySubscriber 专用，不进 PCM 域）
    Rtp { packet: RtpPacket, codec: CodecType },
    /// PCM 层帧（其他订阅者用）
    Pcm { samples: &[i16], sample_rate: u32, pts: Timestamp },
}

pub enum Backpressure {
    /// 实时路径：满了丢最老帧，保证最新
    DropOldest,
    /// 完整性路径：满了阻塞节奏器，不丢
    Block,
}

pub enum SubscriberKind {
    Relay,       // 零拷贝直转
    Transcode,   // 异 codec 转码
    Recorder,    // 录音
    Asr,         // 推 ASR
    Stream,      // 推流
    Mixer,       // 混音（会议）
}
```

### 关键设计点：RelaySubscriber 不进 PCM 域

**RelaySubscriber 是特殊的——它收到的 `EgressFrame::Rtp`，直接在 RTP 层 rewrite header 转发，不解码不进 PCM 域。** 其他订阅者收到 `EgressFrame::Pcm`，从解码后的 PCM 帧消费。

这样 2-party 同 codec 的热路径仍然是零拷贝（RewriteRelay 性能），不被 pub/sub 的 channel 开销拖累。只有需要多订阅者的场景（录音+ASR+推流同时）才进 PCM 域。

## 节奏器逻辑

```rust
/// 改造后的 EgressPipeline 节奏器。
/// 保留 ptime tick + 静音兜底，出口从互斥改为多订阅者。
pub struct EgressPipeline {
    subscribers: Vec<Box<dyn EgressSubscriber>>,
    ptime_ms: u32,
    // ... pacing 逻辑
}

impl EgressPipeline {
    async fn pacing_loop(&mut self) {
        let interval = Duration::from_millis(self.ptime_ms as u64);
        loop {
            tokio::select! {
                biased;
                _ = self.cancel.cancelled() => break,
                _ = tokio::time::sleep(interval) => {
                    // 1. 取当前帧（从 ingress buffer 或静音兜底）
                    let frame = self.get_current_frame_or_silence();

                    // 2. fan-out 给所有订阅者（保留 ptime 强节奏）
                    for sub in &mut self.subscribers {
                        let _ = sub.on_frame(&frame);  // 各自背压处理
                    }
                }
            }
        }
    }
}
```

**保留的核心**：每 tick 必出帧，静音兜底，远端解码器不断流。
**改造的核心**：出口从互斥枚举改为订阅者列表，fan-out 给所有订阅者。

## 背压策略

| 订阅者 | 策略 | 理由 |
|--------|------|------|
| RelaySubscriber | DropOldest | 实时优先，丢老帧不卡 |
| TranscodeSubscriber | DropOldest | 实时优先 |
| RecorderSubscriber | Block | 完整性优先，不能丢帧 |
| AsrSubscriber | DropOldest | 实时优先，丢老帧不卡 |
| StreamSubscriber | DropOldest | 实时优先，容忍稍大延迟 |
| MixerSubscriber | DropOldest | 实时混音 |

**注意**：Block 策略的订阅者（录音）会阻塞节奏器 tick。这是有意的——录音不能丢帧，宁可 tick 延迟一拍。但 Block 订阅者的 buffer 要足够大（如 1000 帧 = 20 秒），避免频繁阻塞。

## 动态拓扑

订阅者的 subscribe/unsubscribe 是运行时操作：

```rust
impl EgressPipeline {
    /// 运行时加订阅者（如中途加录音）
    pub fn add_subscriber(&mut self, sub: Box<dyn EgressSubscriber>) {
        self.subscribers.push(sub);
    }

    /// 运行时移除订阅者（如停止录音）
    pub fn remove_subscriber(&mut self, kind: SubscriberKind) {
        self.subscribers.retain(|s| s.kind() != kind);
    }
}
```

支持的场景：
- **中途加录音**：会话进行中业务方调 `StartRecording`，加 `RecorderSubscriber`，立即开始录
- **中途加转写**：加 `AsrSubscriber`，立即开始推 ASR
- **中途加推流**：加 `StreamSubscriber`，立即开始推流
- **移除订阅者**：停止录音/转写，remove，订阅者 task 退出

**不需要重建 pipeline，不需要断会话，不需要切换 EgressSource 模式。**

## 完整拓扑示例：语音 Agent

```
[Transport: WS/WebRTC/SIP]
    │
    ▼
[IngressTap] (lock-free 双向 RTP 观察)
    │
    ├──▶ [RecorderSubscriber] (双向录音, Block)
    │
    ▼
[Decode] ──▶ PCM
    │
    ▼
[EgressPipeline ptime 节奏器]
    │
    ├──▶ [RelaySubscriber]     (零拷贝直转给 SIP 对端, 若同 codec)
    ├──▶ [TranscodeSubscriber] (异 codec 转码给对端)
    ├──▶ [AsrSubscriber]       (推 PCM 给 ASR, DropOldest)
    ├──▶ [RecorderSubscriber]  (PCM 录音, Block)
    └──▶ [StreamSubscriber]    (推流, 若需要)

[VAD] (作为 IngressTap 的附加观察者, 不在 egress 路径)
    │
    ▼ events
TurnManager (Go, via gRPC callback)
    │
    ▼ transcript
AgentLoop (Go)
    │
    ▼ text
TTS plugin (Go 调用)
    │
    ▼ audio frames
[IngressFromGo] ──▶ EgressPipeline ──▶ [RelaySubscriber] ──▶ 用户
```

**关键**：
- 用户音频同时给 Relay/Transcode（给对端）+ ASR + 录音 + 推流，fan-out 天然
- 加录音/转写/推流只需 add_subscriber，不中断
- VAD 在 ingress 侧（观察者），不在 egress 侧
- TTS 音频从 Go 回来，经 EgressPipeline 推给用户
- 打断：VAD 事件 → Go 停止 TTS → 移除 TTS 相关订阅者

## 跨节点（级联）

跨 media node 时，一个节点的 EgressPipeline 通过 gRPC stream 推到另一个节点：

```
Node A: EgressPipeline ──add──▶ [EgressRpcSubscriber] ──gRPC stream──▶
                                                              │
Node B: [IngressRpc] ──▶ EgressPipeline ──▶ [其他订阅者]
```

`EgressRpcSubscriber` 是一个特殊订阅者，把帧序列化发 gRPC；对端 `IngressRpc` 收帧后注入本地 EgressPipeline。本质是 pub/sub 的远程版。

## 为什么不用纯 pub/sub 数据流驱动

电话音频是**连续流**，不是离散事件。ptime 节奏器的硬保证——每 tick 必出帧，静音兜底——是电话系统的正确做法：

- 网络抖动导致 ingress 断流时，egress 仍然出静音帧，远端解码器不 PLC
- 节奏器是主动的（定时器驱动），不是被动的（等源产生帧才推）
- 这保证了远端听到的音频是连续的，不会有"卡顿"

纯 pub/sub 的数据流驱动（源产生帧才 publish）在网络抖动时会断流，需要额外补 jitter buffer 或静音填充——而 ptime 节奏器已经内置了这个保证。

## 附录：原 pub/sub Track 设计（参考）

> 以下是最初的纯 pub/sub Track 设计，作为混合模型的参考背景保留。混合模型吸收了其多订阅者 fan-out 和背压策略，但驱动方式改为 ptime 定时器。

原设计的 Track 是 pub/sub 中心，源 `publish`，订阅者 `subscribe` 拿 channel receiver：

```rust
// 原设计（已被混合模型替代）
pub struct Track {
    subscribers: Vec<Subscriber>,
}
struct Subscriber {
    tx: Sender<MediaFrame>,
    policy: BackpressurePolicy,
}
```

混合模型中的 `EgressPipeline.subscribers` 对应原设计的 `Track.subscribers`，但：
- 驱动方式从"源 publish"改为"ptime tick 主动 fan-out"
- 帧类型从统一 `MediaFrame` 改为 `EgressFrame::Rtp`（RelaySubscriber）或 `EgressFrame::Pcm`（其他）
- 节奏保证从"取决于订阅者"改为"tick 必出帧 + 静音兜底"
