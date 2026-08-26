# 08 — RustPBX 调研与架构选型（历史档案）

> **⚠️ 已废弃**：本文档为早期选型调研记录。最终实现未采用 RustPBX 作为底座，而是从零自研 Rust 媒体面（`lm-core` / `lm-stream` / `lm-depacketizer` / `lm-protocol` / `lm-recorder` 等 crate），参考 Xiu、atm0s-media-server、Waterbus 的架构思想。本文档保留作为决策档案，仅供历史参考。当前架构以 [14-streaming-media-redesign.md](./14-streaming-media-redesign.md) 为准。

本文档记录对 RustPBX 开源项目的实地调研、架构对比分析、钢人论证过程，以及最终的核心架构决策：**混合模型——RustPBX 的 ptime 节奏器 + RewriteRelay 零拷贝 + pub/sub 的多订阅者 fan-out**。

## 一、RustPBX 是什么

- **项目**: `github.com/restsend/rustpbx`，MIT 协议，Rust 实现的软件定义 PBX
- **版本**: 0.5.0-rc.1（2026-08 实地编译运行）
- **定位**: 高性能 SIP PBX + AI 原生通信平台，Voice Agent 功能已移出到独立项目 `active-call`
- **性能**: 5000 并发 SIP 通话，~3.8 核心，0% 丢包；800 并发 WebRTC↔RTP，0% 丢包
- **技术栈**: `rustrtc`（WebRTC/RTP）+ `rsipstack`（SIP）+ `audio-codec`（编解码）+ 自研 `rustpbx-media` crate

## 二、RustPBX 底层音频处理能力盘点

### ✅ 健全且完善的部分

| 能力 | 实现质量 | 关键代码 |
|------|----------|----------|
| SIP 信令栈 | 完整 | `rsipstack`：UDP/TCP/TLS/WS，注册/鉴权/路由/REFER/Replaces |
| WebRTC | 完整 | `rustrtc`：DTLS-SRTP/ICE/STUN/TURN，800 并发实测 |
| RTP 中继 | 生产级 | `EgressSource::RewriteRelay`：零拷贝 RTP header rewrite，5000 并发 0 丢包 |
| 编解码 | 完整 | `audio-codec` crate：Opus/PCMU/PCMA/G722/G729 + 重采样 |
| 转码 | 完整 | `EgressSource::TranscodePeer`：解码→重采样→编码，Opus↔PCMU 实测 |
| SDP 协商 | 完整 | `negotiate.rs`（2679 行）：编解码/传输模式/视频能力协商 |
| 录音 | 生产级 | `IngressTap`（lock-free 双向 RTP 观察）+ `Recorder` + `WavWriter` |
| DTMF | 完整 | RFC 4733 telephone-event 检测 + 事件总线 |
| 会议混音 | 基本可用 | `ConferenceAudioMixer`：MCU N 方混音 + `route_gain` 按 (src,dst) 控制音量 |
| IVR 播放 | 完整 | `AudioSource`（文件/HTTP/MP3）+ `EgressSource::Media` |
| RTCP 质量监控 | 完整 | `leg_stats.rs`：jitter/RTT/丢包 + `telemetry.rs` 聚合 |
| 视频中继 | 有限 | `video_codecs` 协商 + RewriteRelay（同 codec 直转，不转码） |
| PCM 外置通道 | 完整 | `voip_bridge:ws://` 双向 PCM16 + `LegPcmStream` 拉流 |
| Comfort noise | 完整 | 静音时 CNG 帧保持 ptime 节奏 |

### ⚠️ 有但不够完善

| 能力 | 现状 | 缺什么 |
|------|------|--------|
| 会议 mute | 命令层有，混音层未接通 | `set_muted` 存在但 RWI mute 命令没调它 |
| Supervisor | 命令层有，音频混合 TODO | listen/whisper/barge 的 route_gain 框架在，但没接通 |
| 视频转码 | 只能同 codec 直转 | 异 codec 视频不转码，直接丢弃 |
| SDP 重协商 | hold/reinvite TODO | 通话保持/恢复不完整 |

### ❌ 底层缺失

| 能力 | 影响 | 补齐方式 |
|------|------|----------|
| VAD（语音活动检测） | 语音 Agent 端点检测需要 | 加 VAD 模块作为 `IngressTap` 的附加观察者 |
| AGC/AEC（增益/回声消除） | 依赖终端硬件 | 中台层可选补齐 |
| 跨节点 SFU 级联 | 会议不能跨节点 | 后期加级联 |
| RTMP/WHIP/WHEP | 直播推流缺失 | 加新 transport + sink |
| 多声道/立体声 | 混音器和录音都是 mono | 按需扩展 |

### 关键架构特征：EgressSource 互斥

`egress.rs` 的核心是 `EgressSource` 枚举，**同一时刻只有一种模式**：

```rust
pub enum EgressSource {
    RewriteRelay { ... },    // 零拷贝 RTP 直转
    Silence { ... },         // 静音/保持
    Media { ... },           // IVR 播放
    Inject { ... },          // 外部推 PCM (MCU/app)
    TranscodePeer { ... },   // 异 codec 转码
}
```

**一条 Leg 同一时刻只能做一件事**：要么中继给对端，要么推 PCM 给 ASR，要么播放音频。不能同时中继 + 录音 + 推 ASR。这是后续架构选型的关键分歧点。

## 三、架构对比：RustPBX vs pub/sub Track

### 两种模型的本质

**RustPBX（定时器驱动的单出口策略机）**：

```
Leg ──▶ EgressPipeline ──▶ [当前唯一 EgressSource] ──▶ PeerConnection
         ↑ 20ms tick 必出帧（静音兜底）

模式互斥: Relay | Transcode | Media播放 | Inject | Silence
```

- 驱动方式：**定时器驱动**（20ms tick，主动拉帧）
- 控制流：**命令式**（外部设 EgressSource，节奏器执行）
- 出口数量：**1（互斥）**
- 节奏保证：**强**（tick 必出帧，静音兜底，远端解码器不断流）

**pub/sub Track（数据流驱动的多出口发布订阅）**：

```
Track ──┬──▶ 订阅者1 (ASR, DropOldest)
        ├──▶ 订阅者2 (录音, Block)
        ├──▶ 订阅者3 (混音, DropOldest)
        └──▶ 订阅者4 (Relay, DropOldest)

源驱动 publish, 订阅者各自消费, 各自背压
```

- 驱动方式：**数据流驱动**（源产生帧，主动推给订阅者）
- 控制流：**声明式**（订阅者注册，源 publish 自动 fan-out）
- 出口数量：**N（并行）**
- 节奏保证：**弱**（取决于订阅者消费速度）

### 逐维度对比

| 维度 | RustPBX 互斥 | pub/sub Track |
|------|-------------|---------------|
| 2-party 同 codec 中继 | ✅ 极致：RewriteRelay 零拷贝，不解码不进 PCM 域 | ❌ 过度：要解码到 PCM 再 fan-out |
| 2-party 异 codec 转码 | ✅ 直接：TranscodePeer 单管道 | ⚠️ 绕路：多一次 channel 跳 |
| ptime 节奏保证 | ✅ 强保证：每 tick 必出帧，静音兜底 | ⚠️ 弱保证：需额外补节奏器 |
| 同时录音+中继+ASR | ❌ 不行：互斥 | ✅ 原生：多订阅者 |
| 动态加录音/转写 | ❌ 要切换模式，中断当前路径 | ✅ subscribe 热加 |
| 会议 N 方混音 | ⚠️ 独立模块，和 Bridge 不统一 | ✅ 混音是订阅者之一 |
| 直播推流 | ❌ 没有抽象 | ✅ 推流是订阅者之一 |
| CPU 效率（2-party） | ✅ 极高：零拷贝直转，0.14% core/call | ⚠️ 低：channel 开销 |
| CPU 效率（多订阅者） | ❌ 做不到 | ✅ 一份解码多份用 |
| 实现复杂度 | ✅ 简单：互斥枚举，状态机清晰 | ⚠️ 复杂：channel + 背压 + 生命周期 |
| 实时性保证 | ✅ 强：ptime 硬保证 | ⚠️ 取决于调度 |

### 关键洞察

**两者的真正分歧不在"推 vs 拉"，而在"一对一互斥" vs "一对多 fan-out"。**

RustPBX 的 ptime 节奏推送本质也是推模型——源驱动，20ms tick 推一帧。它和 pub/sub 的源驱动 publish 是同构的。真正的差异是出口结构：互斥单出口 vs 并行多出口。

**对语音中台来说，RustPBX 的定时器驱动更对**——电话音频是连续流，不能断，ptime 节奏是硬保证。pub/sub 的数据流驱动更适合"事件可能来也可能不来"的场景。

## 四、钢人论证

### 支持以 RustPBX 为底座的最强论证

RustPBX 的底层音频处理能力**足够健全**——SIP/RTP/WebRTC/编解码/转码/录音/DTMF/会议混音/RTCP 监控，经过 5000 并发 0 丢包验证。自己重写这些至少 6-12 个月，而且很难写得比它更好。

它缺的底层能力（VAD/直播/跨节点）是可以补的：
- VAD：加一个 `audio-codec` 之上的 VAD 模块，作为 `IngressTap` 的附加观察者
- 直播：加 RTMP/WHIP transport，作为新的 sink
- 跨节点：后期加 SFU 级联

EgressSource 互斥是唯一需要改造的核心点——把互斥枚举改成可组合的订阅者列表。这是对 `egress.rs` 的结构性改造，但不是重写。

### 反对以 RustPBX 为底座的最强论证

RustPBX 的核心抽象是 **Call（2-party B2BUA）**——`MediaBridge` 持有恰好两条 Leg，所有操作围绕 A/B。多方是附加模块 `ConferenceAudioMixer`，不是一等公民。`EgressSource` 互斥意味着一条腿同一时刻只能一种 egress 模式。

如果以它为底座，改造范围会扩大：
- EgressSource 互斥→多订阅者：重写 egress 核心逻辑
- 加直播：没有 Sink 抽象，要新造一套
- 加分布式：没有控制/数据面分离，要拆进程 + 加 etcd
- 加流式 TTS：当前 TTS 是文件缓存模式，要改成流式

这些改动加起来，是否等于重写核心？

### 论证结论

**不等于重写核心，因为底层组件（codec/DSP/transport/recorder/mixer/SIP 栈/WebRTC 栈）不需要动。** 需要改的是 egress 这一个核心点 + 上层中台层（本来就要自建）。底层组件是"与架构无关的叶子组件"，它们的实现不依赖 EgressSource 是互斥还是多订阅者。

**关键变量是"改造 egress 的成本"vs"重写整个 Rust 媒体层的成本"。前者是改一个文件的核心逻辑，后者是从零写 18000+ 行。结论明确：改造 egress。**

## 五、最终架构决策：混合模型

### 核心结论

**最优解 = RustPBX 的 ptime 节奏器 + RewriteRelay 零拷贝 + pub/sub 的多订阅者 fan-out。**

- RustPBX 的 ptime 节奏器 + RewriteRelay 零拷贝 → **对**，保留
- RustPBX 的 EgressSource 互斥 → **错**，改成多订阅者
- pub/sub 的多订阅者 fan-out + 各自背压 → **对**，吸收
- pub/sub 的"源驱动 publish，订阅者各自节奏" → **部分对**，但 ptime 强节奏不能丢

### 改造后的 EgressPipeline

```
ptime 节奏器 (20ms tick, 必出帧, 静音兜底)  ← 保留 RustPBX 的强节奏保证
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

### 保留的优势

1. **ptime 节奏器的强保证**：每 tick 必出帧，远端解码器不 PLC（Packet Loss Concealment）
2. **RewriteRelay 零拷贝**：同 codec 直转，改 RTP header 不进 PCM 域，0.14% core/call

### 吸收的优势

1. **多订阅者 fan-out**：同时中继 + 录音 + ASR + 推流
2. **动态加/减订阅者**：不中断当前路径，subscribe/unsubscribe 热变
3. **各自背压**：录音 Block（不丢帧），ASR/混音 DropOldest（实时优先）

### 关键设计点

**RelaySubscriber 是特殊的——它不进 PCM 域，直接在 RTP 层 rewrite header。** 其他订阅者都从 PCM 帧（或解码后的帧）消费。这样 2-party 同 codec 的热路径仍然是零拷贝，不被 pub/sub 的 channel 开销拖累。

### 驱动方式选择

**定时器驱动（RustPBX 的方式），不是数据流驱动（pub/sub 的方式）。**

电话音频是连续流，不能断。ptime 节奏器是硬保证——不管源有没有帧都出帧（静音兜底），远端解码器永远不会断流。这是电话系统的正确做法，保留。

## 六、整体架构定论

```
我们的中台层 (Go: 编排/插件/分布式/多租户/AI 对话)
        │ gRPC + voip_bridge WS
        ▼
RustPBX 底座 (Rust: rustpbx-media + rsipstack + rustrtc + audio-codec)
  ├── 保留: SIP/WebRTC/RTP/编解码/转码/录音/DTMF/会议混音/RTCP/ptime 节奏器
  ├── 改造: EgressSource 互斥→多订阅者 (核心改造点, egress.rs)
  ├── 新增: VAD 模块 (底层补齐, 作为 IngressTap 附加观察者)
  ├── 新增: RTMP/WHIP transport (直播补齐)
  └── 新增: 跨节点 SFU 级联 (后期)
```

### 底座选型

| 组件 | 来源 | 说明 |
|------|------|------|
| SIP 栈 | `rsipstack` | 直接用 RustPBX 的 |
| WebRTC/RTP | `rustrtc` | 直接用 RustPBX 的 |
| 编解码 | `audio-codec` | 直接用 RustPBX 的 |
| 录音 | `IngressTap` + `Recorder` | 直接用 |
| 混音 | `ConferenceAudioMixer` | 直接用，补 mute 接通 |
| SDP 协商 | `negotiate.rs` | 直接用 |
| EgressPipeline | **改造** | 互斥→多订阅者 |
| VAD | **新增** | 补齐 |
| RTMP/WHIP | **新增** | 补齐 |
| 中台层 | **自建** | Go 编排/插件/分布式 |

### 改造范围

| 改造点 | 范围 | 风险 |
|--------|------|------|
| EgressSource→多订阅者 | `egress.rs` 核心逻辑 | 中：要保留 ptime 节奏器和 RewriteRelay 路径 |
| 会议 mute 接通 | `conference_mixer.rs` + RWI 命令 | 低：框架已在 |
| VAD 模块 | 新文件 | 低：独立模块 |
| RTMP/WHIP transport | 新 crate | 中：新协议栈 |
| 跨节点 SFU 级联 | 后期 | 高：架构级 |

## 七、对设计文档的影响

本文档的结论将更新以下文档：
- `01-architecture.md`：Rust 流媒体层从"自研"改为"基于 RustPBX 底座改造"
- `07-media-pipeline.md`：媒体管线从"pub/sub Track"改为"ptime 节奏器 + 多订阅者出口"
- `05-roadmap.md`：Phase 1 从"自研 Rust 媒体层"改为"改造 RustPBX egress + 补 VAD"
