# 当前流转状态与后续路线

> 本文档记录截至 2026-08-25 的实际实现状态、媒体流转路径、各协议接入情况，以及后续开发路线。

---

## 1. 整体架构（当前实际）

```mermaid
graph TB
    subgraph Browser["浏览器（2 个参与者）"]
        A["Participant A<br/>pub: audio+video<br/>sub: audio+video"]
        B["Participant B<br/>pub: audio+video<br/>sub: audio+video"]
    end

    subgraph Go["Go 控制面 / Pion"]
        PA["Publisher A<br/>接收 A 的 RTP"]
        PB["Publisher B<br/>接收 B 的 RTP"]
        SA["Subscriber A<br/>发给 A 的 RTP"]
        SB["Subscriber B<br/>发给 B 的 RTP"]
        Bridge["rustbridge Client<br/>per-track push/pull"]
    end

    subgraph Rust["Rust 媒体面 (lm-control)"]
        SM["SessionManager"]
        Room["Room: demo-room"]
        SessA["Session A<br/>tracks: audio+video"]
        SessB["Session B<br/>tracks: audio+video"]
        BC_A["Track broadcast<br/>(A 的 track)"]
        BC_B["Track broadcast<br/>(B 的 track)"]
    end

    A -->|"WebRTC RTP"| PA
    B -->|"WebRTC RTP"| PB
    SA -->|"WebRTC RTP"| A
    SB -->|"WebRTC RTP"| B

    PA -->|"PushRtp (gRPC)"| Bridge
    PB -->|"PushRtp (gRPC)"| Bridge
    Bridge -->|"push_rtp stream"| SessA
    Bridge -->|"push_rtp stream"| SessB

    BC_A -->|"PullRtp (gRPC)"| Bridge
    BC_B -->|"PullRtp (gRPC)"| Bridge
    Bridge -->|"RTP"| SA
    Bridge -->|"RTP"| SB

    SM --> Room
    Room --> SessA
    Room --> SessB
    SessA --> BC_A
    SessB --> BC_B
```

---

## 2. 单个 RTP 包的完整流转（以 A 的视频包为例）

```mermaid
sequenceDiagram
    participant BA as Browser A
    participant PA as Publisher A (Pion)
    participant Bridge as rustbridge Client
    participant Rust as Rust lm-control
    participant BB as Browser B

    BA->>PA: WebRTC RTP (video, VP8, 90kHz)
    PA->>PA: readLoop() 解析 RTP → MediaFrame
    PA->>Bridge: OnMediaFrame(trackID, frame)
    Bridge->>Rust: PushRtpPacket (gRPC client stream)

    Note over Rust: push_rtp() 收到包
    Rust->>Rust: 查 source track → kind=Video
    Rust->>Rust: get_room_peer_tracks_by_kind(A, Video)
    Note over Rust: 遍历 room "demo-room"<br/>排除 A 自己<br/>找到 B 的 video track
    Rust->>Rust: peer_track.rtp_broadcast.send(pkt)
    Note over Rust: 包进入 B 的 video track<br/>broadcast channel

    Note over Rust: B 的 pull_rtp forwarder 被唤醒
    Rust-->>Bridge: PullRtp stream 返回 RtpPacket
    Bridge->>Bridge: 构造 MediaFrame (kind=Video, codec=VP8)
    Bridge->>BB: SendMediaFrame(subTrackID, frame)
    BB->>BB: writeRTP → Pion TrackLocalStaticRTP
    BB->>BB: Pion 重写 SSRC/PT → 发给浏览器
```

---

## 3. Rust 内部路由机制

```mermaid
graph LR
    subgraph Push["push_rtp (Go → Rust)"]
        P1["A push audio"]
        P2["A push video"]
        P3["B push audio"]
        P4["B push video"]
    end

    subgraph Session["SessionManager"]
        direction TB
        Room["Room: demo-room"]
        Room --> SA["Session A"]
        Room --> SB["Session B"]

        SA --> TA_A["Track: A-audio<br/>kind=Audio<br/>broadcast"]
        SA --> TA_V["Track: A-video<br/>kind=Video<br/>broadcast"]
        SB --> TB_A["Track: B-audio<br/>kind=Audio<br/>broadcast"]
        SB --> TB_V["Track: B-video<br/>kind=Video<br/>broadcast"]
    end

    subgraph Route["kind-aware 路由规则"]
        R1["A-audio 包 →<br/>只到 B-audio track"]
        R2["A-video 包 →<br/>只到 B-video track"]
        R3["B-audio 包 →<br/>只到 A-audio track"]
        R4["B-video 包 →<br/>只到 A-video track"]
    end

    subgraph Pull["pull_rtp (Rust → Go)"]
        L1["A pull A-audio<br/>→ 收到 B 的 audio"]
        L2["A pull A-video<br/>→ 收到 B 的 video"]
        L3["B pull B-audio<br/>→ 收到 A 的 audio"]
        L4["B pull B-video<br/>→ 收到 A 的 video"]
    end

    P1 --> R1
    P2 --> R2
    P3 --> R3
    P4 --> R4

    R1 --> TB_A
    R2 --> TB_V
    R3 --> TA_A
    R4 --> TA_V

    TA_A --> L1
    TA_V --> L2
    TB_A --> L3
    TB_V --> L4
```

---

## 4. 关键设计决策

```mermaid
graph TD
    subgraph 设计决策
        D1["每 participant 一个 Session<br/>（非共享 session）"]
        D2["Session 加入同一 Room"]
        D3["每 track 一个 broadcast channel<br/>tokio::broadcast (512 buffer)"]
        D4["push 写别人的 broadcast<br/>pull 读自己的 broadcast"]
        D5["kind-aware 过滤<br/>audio → audio, video → video"]
        D6["排除 source session<br/>避免自回声"]
    end

    D1 --> E1["独立生命周期<br/>单独 mute/控制"]
    D2 --> E2["room 级路由<br/>N-1 转发"]
    D3 --> E3["多订阅者 fan-out<br/>（未来多观看者）"]
    D4 --> E4["推拉分离<br/>背压独立"]
    D5 --> E5["音频不污染视频<br/>视频不污染音频"]
    D6 --> E6["A 听不到自己<br/>B 听不到自己"]
```

---

## 5. 各协议接入状态

| 协议 | 信令实现 | Demo | Rust 媒体集成 | 端到端媒体流 | 状态 |
|------|---------|------|-------------|------------|------|
| **WebRTC** | ✅ Pion 完整 | ✅ webrtc-rust-demo | ✅ rustbridge push/pull | ✅ 音频+视频 | **跑通** |
| **WebSocket** | ✅ offer/answer/start/stop + chat | ✅ ws-rust-demo | ✅ rustbridge push/pull | ✅ 音频 + 文本 | **跑通** |
| **SIP** | ✅ sipgo (INVITE/BYE/REGISTER) | ✅ protocol-server | ❌ | ❌ | 信令通，无 RTP |
| **RTMP** | ✅ gortmplib (publish/play) | ✅ protocol-server | ❌ | ❌ | 信令通，无媒体 |
| **WHIP** | ✅ Pion (POST/DELETE) | ✅ protocol-server | ❌ | ❌ | 信令通，无媒体 |
| **WHEP** | ✅ Pion (POST/DELETE) | ✅ protocol-server | ❌ | ❌ | 信令通，无媒体 |
| **MQTT** | ✅ paho (signal/media topic) | ✅ protocol-server | ❌ | ❌ | 信令通，无媒体 |
| **REST API** | ✅ session CRUD | ✅ protocol-server | ❌ | N/A | 管理面通 |

**结论：WebRTC 和 WebSocket 已跑通端到端媒体流（Go↔Rust）。其余协议的信令层都已实现，但媒体帧未接入 Rust 媒体节点。**

### 协议适用场景

| 协议 | 音频 | 视频 | 文本 | 适用场景 |
|------|:----:|:----:|:----:|---------|
| **WebRTC** | ✅ | ✅ | ❌ (需 DataChannel) | 浏览器实时音视频通话 |
| **WebSocket** | ✅ | ❌ | ✅ | 语音 Agent + 文本交互（低复杂度接入） |
| **SIP** | ✅ | ✅ | ❌ | 传统电话互通、SIP 外呼 |
| **RTMP** | ✅ | ✅ | ❌ | 直播推流/拉流 |
| **WHIP** | ✅ | ✅ | ❌ | WebRTC 推流入站 |
| **WHEP** | ✅ | ✅ | ❌ | WebRTC 拉流出站 |
| **MQTT** | ✅ | ❌ | ✅ | IoT 设备语音接入 |

> **WebSocket 不适合传视频**：基于 TCP，无拥塞控制/带宽估计，视频关键帧（几十 KB）会导致队头阻塞和累积延迟。实时视频应使用 WebRTC / WHIP / WHEP（基于 UDP + SRTP）。WS 协议层虽保留了 video 帧定义，但仅用于协议完整性，不推荐用于实时视频传输。

---

## 6. 当前完成度 vs 计划

```mermaid
pie title Phase 1 完成度（约 45%）
    "已完成" : 45
    "未完成" : 55
```

| 模块 | 状态 | 说明 |
|------|------|------|
| Rust 媒体基础 (10 crates) | ✅ | core/transport/codecs/router/pipeline/mixer/recorder/dsp/control/telemetry |
| gRPC 控制面 (lm-control) | ✅ | session/room/track CRUD + push/pull RTP + kind-aware 路由 |
| Go 协议层 (信令) | ✅ | 7 种协议 adapter 全部实现 |
| Go↔Rust 桥接 (rustbridge) | ✅ | per-track push/pull，已修复 pull stream map 泄漏 |
| WebRTC 端到端 demo | ✅ | 音频+视频双向 room 路由 |
| WebSocket 端到端 demo | ✅ | 音频+文本，WS 协议加 chat 消息类型 |
| audio-codec vendor | ✅ | 源码内嵌，不再依赖 crates.io |
| 单元测试 | ✅ | Rust 18 + Go 40 = 58 tests |
| **Go 控制面 `control/`** | ❌ | 无 AgentSession/TurnManager/AgentLoop |
| **插件系统** | ❌ | 无 ASR/TTS/LLM 接口、无 registry、无 mock 插件 |
| **协议→Rust 媒体集成** | ❌ | 仅 WebRTC + WebSocket，其余 5 种协议未接 |
| **EgressSubscriber fan-out** | ❌ | trait 定义了但无具体实现 |
| **协议业务逻辑** | ❌ | 无 auth/router/transfer |

---

## 7. 后续路线

### Phase 1 收尾（近期）

```mermaid
graph LR
    subgraph P1["Phase 1 收尾"]
        direction TB
        A1["1. Go 控制面<br/>control/"] --> A2["2. 插件系统<br/>capability + mock"]
        A2 --> A3["3. 协议→Rust 集成<br/>WS/SIP/WHIP/WHEP"]
        A3 --> A4["4. EgressSubscriber<br/>Relay/Recorder/ASR"]
        A4 --> A5["5. 端到端 demo<br/>browser→Rust→ASR→LLM→TTS→playback"]
    end
```

**优先级排序：**

1. **Go 控制面 `control/`** — P0
   - `control/session.go` — AgentSession 生命周期管理
   - `control/orchestrate.go` — AgentLoop + TurnManager + barge-in
   - `control/protocol/` — 协议 Event 上报到控制面，做业务决策（auth/router/transfer）

2. **插件系统** — P0
   - `control/capability.go` — ASR/TTS/LLM/Recorder/Detector 接口定义
   - `control/plugin/registry.go` — 插件注册 + YAML 配置加载
   - `cmd/mock-asr/`、`cmd/mock-tts/`、`cmd/mock-llm/` — Mock 插件实现

3. **协议→Rust 媒体集成** — P1
   - WebSocket → rustbridge（最简单，已有 conversation-demo 基础）
   - WHIP/WHEP → rustbridge（Pion 已有，模式与 WebRTC 一致）
   - SIP → rustbridge（需要 RTP bridge，sipgo 不处理媒体）

4. **EgressSubscriber 实现** — P1
   - RelaySubscriber — 零拷贝 RTP 转发（当前 demo 的 Rust 路由是雏形）
   - RecorderSubscriber — 接 lm-recorder
   - AsrSubscriber — 接插件 ASR

5. **端到端 demo** — P1
   - browser → Go/Pion → Rust → VAD → mock ASR → mock LLM → mock TTS → playback + barge-in

### Phase 2（中期）

| 项目 | 说明 |
|------|------|
| 视频编解码 | VP8/H.264 encoder/decoder（当前只透传 RTP payload） |
| SRTP/DTLS | Rust 侧原生 WebRTC 媒体面（或继续依赖 Pion） |
| Jitter buffer | lm-pipeline 中加自适应抖动缓冲 |
| SIP 通话 | SIP INVITE → Rust RTP → WebRTC 混音 |
| 录制 | RecorderSubscriber → WAV/MP4 落盘 |
| 会议混音 | lm-mixer 接入 EgressPipeline |

### Phase 3+（远期）

| 项目 | 说明 |
|------|------|
| 分布式 | etcd 注册 + 多节点调度 + SFU 级联 |
| RTMP 推流 | RTMP ingest → Rust → WebRTC 播放 |
| 多租户 | tenant 隔离 + 配额 + 计费 |
| 进程外插件 | gRPC sidecar 插件框架 |
| 真实 AI 插件 | OpenAI ASR/TTS、其他 LLM 接入 |

---

## 8. 推拉流模型说明

当前 Rust 部分本质是**推拉流模型**：

| 环节 | 实现 |
|------|------|
| **推 (push)** | Go 每个 publisher track 开一个 gRPC client stream (`PushRtp`)，持续把 RTP 发给 Rust |
| **Rust 路由** | 收到包后查 source track 的 kind → 找同 room 其他 session 的同 kind track → `broadcast.send()` |
| **拉 (pull)** | Go 每个 publisher track 开一个 gRPC server stream (`PullRtp`)，Rust 的 forwarder 从 broadcast channel 读包发回 Go |
| **写回浏览器** | Go 把收到的 RTP 通过 `TrackLocalStaticRTP.WriteRTP()` 写入 subscriber track，Pion 重写 SSRC/PT 后发给浏览器 |

**核心枢纽是 broadcast channel**：push 写入别人的 channel，pull 从自己的 channel 读。这天然支持未来多订阅者（一个 track 可以有多个 broadcast receiver）。
