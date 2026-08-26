# 01 — 总体架构

## 三层归属定义

在进入架构图之前，先明确三层归属——这决定了每个模块放在 Go 还是 Rust。

### Rust 层 = 实时媒体面（Real-time Media Plane）

负责**媒体字节和媒体协议的收发处理**，不负责业务决策。

| 类别 | 具体内容 | 来源 |
|------|----------|------|
| 媒体传输协议 | RTP/SRTP/WebRTC/WS-media(voip_bridge)/RTMP/WHIP | 自研 lm-transport |
| SIP 收发 + SDP 协商 | SIP UDP/TCP/TLS/WS 收发 + SDP 解析/组装/协商 | Go 层 sipgo + negotiate |
| 编解码 | Opus/PCMU/PCMA/G722/G729 + 重采样 | audio-codec + lm-codecs |
| 媒体处理 | 转码/混音/VAD/comfort noise | 自研 lm-mixer + lm-dsp |
| 媒体管线 | ptime 节奏器/MediaStream/Depacketizer/GOP 缓存 | 自研 lm-stream + lm-depacketizer |
| 媒体持久化 | 录音/录像（WAV/IVF/H.264/分段 MP4） | 自研 lm-recorder |
| 协议转封装 | WebRTC→HLS/HTTP-FLV/RTMP/RTSP/SRT/GB28181 | 自研 lm-protocol |
| 媒体监控 | jitter/RTT/丢包/RTCP 统计 | lm-telemetry |

### Go 层 = 控制面 + 中台层

负责**会话编排、业务决策、AI 能力、分布式协调**，不碰媒体字节。

| 类别 | 具体内容 |
|------|----------|
| 业务决策 | SIP 路由/鉴权/转接策略、录音策略、会议管理 |
| AI 编排 | ASR/TTS/LLM/Agent Loop/打断 |
| 会话管理 | Session/Room/Call 状态、会话生命周期 |
| 信令协调 | WebRTC SDP offer/answer 交换、ICE candidate 协调、信令 WS |
| 插件系统 | registry/loader/rpc（进程内 + 进程外） |
| 分布式协调 | etcd/调度/多租户/集群状态 |
| 业务数据 | CDR/通话记录/录音元数据 |

### 协议归属的关键区分：信令 vs 媒体

"协议层"是模糊的词，必须拆分。**区分标准是"传的是控制消息还是媒体字节"**：

| 协议 | 信令部分（→ Go） | 媒体部分（→ Rust） |
|------|------------------|-------------------|
| WebRTC | SDP offer/answer 交换、ICE candidate 协调 | DTLS-SRTP/RTP/RTCP over UDP |
| WebSocket | WS 作为信令通道（传 SDP/控制消息） | WS 作为媒体通道（voip_bridge 双向 PCM16） |
| SIP | 业务决策（路由/鉴权/转接） | SIP 消息收发 + SDP 协商 + RTP/SRTP |
| RTMP/WHIP | 推流会话建立（Go 协调） | RTMP/WHIP 媒体流收发 |

**WebRTC 的 PeerConnection 生命周期在 Rust**——Go 通过 gRPC 调 `CreatePeerConnection`/`SetRemoteDescription`/`AddIceCandidate`，Rust 执行。SDP 内容的交换协调在 Go，但 PeerConnection 对象在 Rust。这和 SIP 一样：信令收发和协商在 Rust，业务决策在 Go。

**为什么 WebRTC 媒体不能在 Go**：SRTP 解包、RTP 解包、jitter buffer、NACK/PLI 处理是 20ms 实时热路径，Go 的 GC 和跨语言调用开销不可接受。

**为什么 WebSocket 媒体不能在 Go**：voip_bridge 的 PCM16 双向流是 20ms 帧的实时媒体，和 RTP 本质相同，只是传输层从 UDP 换成了 WS。

**唯一在 Go 的 WebSocket 是信令 WS**——客户端连 Go 控制面，传 JSON 控制消息（SDP/ICE/开始录音/停止录音/转接），这不是媒体字节。

### SIP 信令的拆分边界

```
SIP 消息收发 + SDP 解析/组装/协商  →  Rust (rsipstack + negotiate.rs)
         │
         │ 上报事件: "收到 INVITE, from=xxx, to=xxx, SDP=xxx"
         ▼
Go 控制面做业务决策: 路由/鉴权/转接/录音策略
         │
         │ 下发指令: "forward to sip:1002" / "reject" / "transfer to voip_bridge:ws://..."
         ▼
Rust 执行 SIP 动作: 发 INVITE / 发 BYE / REFER / 透传 SDP
```

**SIP 收发在 Rust 不在 Go 的理由**：SIP 和媒体绑定——SDP 协商结果直接决定 RTP 用什么 codec/端口/ICE，这些是媒体面状态。如果 SIP 在 Go，每次 SDP 协商结果要跨 gRPC 同步给 Rust，增加延迟和状态同步复杂度。

**SIP 决策在 Go 不在 Rust 的理由**：决策是业务逻辑（路由规则/鉴权策略/转接目标/是否录音），需要访问中台状态（会话历史/租户配置/插件能力/Agent 状态），且可以容忍 100ms 级延迟（不像媒体帧 20ms 硬要求）。

## 控制面 / 数据面分离

分布式语音中台的核心。所有分布式能力建立在这层分离上。

```
┌──────────────────────────────────────────────────────────────────┐
│  业务方 (你的应用 / 第三方)                                         │
└───────────────┬──────────────────────────────────────────────────┘
                │ HTTP / gRPC / WebSocket API
┌───────────────▼──────────────────────────────────────────────────┐
│  CONTROL PLANE + 中台层  (Go — 信令 + 编排 + 调度 + AI)             │
│  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌──────────────┐   │
│  │ Signaling  │ │ Session    │ │Orchestrator│ │ Plugin       │   │
│  │ Gateway    │ │ Manager    │ │(Agent/IVR/ │ │ Catalog +    │   │
│  │(信令WS/    │ │(Room/Call/ │ │ Workflow)  │ │ Scheduler    │   │
│  │ SDP协调/   │ │ Agent)     │ │            │ │              │   │
│  │ 业务决策)  │ │            │ │            │ │              │   │
│  └────────────┘ └────────────┘ └────────────┘ └──────────────┘   │
│         │            │              │              │             │
│         └────────────┴──────────────┴──────────────┘             │
│                       │  Cluster State (etcd)                     │
│         ┌─────────────┴──────────────┐                           │
│         │  Capability Plugins         │                           │
│         │  (ASR / TTS / LLM / Tool)   │  ← 进程内 Go 或进程外 gRPC │
│         └─────────────────────────────┘                           │
└───────────────────────┬──────────────────────────────────────────┘
                        │  gRPC: 调度会话 + 媒体控制 + 音频直连
┌───────────────────────▼──────────────────────────────────────────┐
│  MEDIA PLANE  (Rust — 实时媒体面, 水平扩展)                         │
│  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌──────────────┐   │
│  │ Transport  │ │ Media      │ │ Media      │ │ Sinks        │   │
│  │ Endpoint   │ │ Pipeline   │ │ Egress/    │ │(Recorder /   │   │
│  │(WebRTC /   │ │(Router /   │ │ Ingress    │ │ Streamer)    │   │
│  │ RTP / WS / │ │ Mixer /    │ │(多订阅者,  │ │              │   │
│  │ RTMP /     │ │ Codec /    │ │ 直连插件,  │ │              │   │
│  │ WHIP +     │ │ DSP / VAD) │ │ 绕过 Go)   │ │              │   │
│  │ SIP收发)   │ │            │ │            │ │              │   │
│  └────────────┘ └────────────┘ └────────────┘ └──────────────┘   │
└──────────────────────────────────────────────────────────────────┘
```

### 职责切分

| | 控制面+中台层 (Go) | 实时媒体面 (Rust) |
|---|---|---|
| 状态 | 无状态（etcd） | 有状态（会话媒体流） |
| 扩展 | 多副本 | 水平扩展 + 会话亲和 |
| 关注 | 业务逻辑、编排、AI 调用、信令协调 | 媒体字节、低延迟、零拷贝、编解码 |
| 语言 | Go | Rust |
| 失败恢复 | 任意副本接管 | 会话重建（媒体不可迁移） |
| 实时性要求 | 100ms 级（信令/决策） | 20ms 硬保证（ptime 节奏器） |

### 会话生命周期（分布式视角）

1. 业务方调控制面 API `CreateSession`
2. Scheduler 选一个 Rust media node（按负载/地域/能力标签）
3. etcd 写入 `sessionID → mediaNodeID` 路由表
4. 控制面向该 media node 发 `CreateSession` gRPC，建立媒体上下文
5. 信令网关把客户端 offer/answer 透传到 media node 的 transport endpoint
6. 媒体流在 media node 内路由；ASR/TTS 音频经 egress 直连插件
7. 会话结束，media node 释放资源，etcd 清理路由

## Rust 媒体面：自研 crate 结构

Rust 流媒体层从零自研，参考 Xiu、atm0s-media-server、Waterbus 的架构思想，构建以下 crate：

### ✅ Rust 媒体面 crate（自研）

| 模块 | crate | 作用 |
|------|-------|------|
| 核心类型 | `lm-core` | MediaFrame / Depacketizer / Packetizer / StreamSink trait |
| 编解码 | `lm-codecs` + `audio-codec` | Opus/PCMU/PCMA/G722 + 重采样，codec 类型映射 |
| 传输 | `lm-transport` | RTP 收发抽象 |
| 解包器 | `lm-depacketizer` | VP8/VP9/H264/Opus RTP→Frame 组装 |
| 流抽象 | `lm-stream` | MediaStream + StreamSink + GopCache + Simulcast 路由 |
| 协议转封装 | `lm-protocol` | HLS/HTTP-FLV/RTMP/RTSP/SRT/GB28181/WHIP/WHEP remuxer |
| 混音 | `lm-mixer` | 音频混音（MCU） |
| 录制 | `lm-recorder` | 分段录制 + MP4 合并 + 录制状态机 |
| DSP | `lm-dsp` | VAD/resample/AEC/AGC |
| gRPC 服务 | `lm-control` | session/room/track CRUD + push/pull RTP + 录制控制 |
| 遥测 | `lm-telemetry` | jitter/RTT/丢包/RTCP 统计 |

### ❌ 不在 Rust 的（属于 Go 中台层或控制面）

| 模块 | 归属 | 理由 |
|------|------|------|
| IVR / 队列 / 路由 / 用户管理 | Go 中台层 | 业务逻辑 |
| 数据库 / Web Console / API | Go 控制面 | 控制面存储与管理 |
| CDR / 通话记录 | Go 中台层 | 业务数据 |
| ASR / TTS / LLM | Go 中台层插件 | AI 能力，插件化 |
| 分布式协调 | Go 控制面 | etcd / 调度 / 多租户 |

### ⚠️ 模糊地带（已决策）

| 模块 | 决策 | 理由 |
|------|------|------|
| 录音管理 | 媒体处理留 Rust，CDR 业务留 Go | 写文件是媒体处理，CDR 元数据是业务 |
| 会议管理 | 混音留 Rust，会议状态留 Go | MCU 混音是媒体处理，会议状态是控制 |
| SIP 注册 | 收发留 Go，注册位置存储留 Go | SIP 信令在 Go 层用 sipgo 处理 |
| Prometheus 媒体指标 | 保留在 Rust | 媒体面自己的 jitter/丢包/RTCP 指标 |

### 协议边界

```
Go 控制面（信令/协议监听/握手）
  │  WebRTC SDP/ICE, SIP INVITE, RTMP handshake, WHIP/WHEP HTTP, SRT/GB28181 信令
  │
  │  gRPC PushRtp（明文 RTP）
  ▼
Rust 媒体面（帧级处理）
  │  Depacketizer → MediaFrame → StreamSink
  │  ├─ SFU 转发（peer RTP 直转）
  │  ├─ 混音（MCU）
  │  ├─ 录制（分段 MP4）
  │  └─ 转封装输出（HLS / HTTP-FLV）
  │
  │  HTTP 输出（Rust 直接对外）
  ▼
客户端 / 播放器
```

## 分层总览

```
┌─────────────────────────────────────────────────┐
│  业务 API  (cmd/server, pkg/control/api)        │  Go
├─────────────────────────────────────────────────┤
│  编排层    orchestrate (agent loop / workflow)  │  Go
├─────────────────────────────────────────────────┤
│  会话层    session (room/call/agent/turn)       │  Go
├─────────────────────────────────────────────────┤
│  能力层    capability (asr/tts/llm/recorder)    │  Go (契约) + 插件
├─────────────────────────────────────────────────┤
│  插件系统  plugin (registry/loader/rpc)         │  Go
├─────────────────────────────────────────────────┤
│  控制面    control (signaling/scheduler/cluster)│  Go
├═════════════════════════════════════════════════╣  ← gRPC 契约边界
│  媒体层    media (frame/router/mixer/codec/dsp) │  Rust
├─────────────────────────────────────────────────┤
│  传输层    transport (webrtc/sip/rtp/rtmp/ws)   │  Rust
└─────────────────────────────────────────────────┘
```

## 目录结构（双语言 monorepo）

```
LingVoice/
├── docs/                      # 设计文档
│
├── control/                   # Go: 控制面 + 编排 + 能力 + 插件
│   ├── go.mod
│   ├── pkg/
│   │   ├── transport-signal/  # 信令侧 (非媒体): ws/sip/whip/http signaling
│   │   ├── session/           # 会话: room/call/agent/turn
│   │   ├── capability/        # 能力契约: asr/tts/llm/recorder/tool
│   │   ├── orchestrate/       # agent loop / pipeline / graph / workflow
│   │   ├── plugin/            # 插件系统: registry/loader/rpc
│   │   ├── control/           # signaling gateway / scheduler / cluster(etcd) / api
│   │   ├── obs/               # metrics/tracing/logging
│   │   └── config/
│   ├── cmd/
│   │   ├── server/            # 控制面+数据面一体 (单节点开箱即用, 媒体走 Rust 子进程或远程)
│   │   ├── control-node/      # 纯控制面
│   │   └── plugins/           # 官方插件子命令 (可选独立进程)
│   └── proto/                 # gRPC 契约 (与 media 共享)
│
├── rust-media/                # Rust: 流媒体层 (自研)
│   ├── Cargo.toml
│   ├── crates/
│   │   ├── lm-core/           # 核心类型: MediaFrame / Depacketizer / StreamSink
│   │   ├── lm-codecs/         # 编解码类型映射
│   │   ├── audio-codec/       # 音频编解码: Opus/G711/G722 + resample
│   │   ├── lm-transport/      # RTP 传输抽象
│   │   ├── lm-depacketizer/   # RTP→Frame 解包: VP8/VP9/H264/Opus
│   │   ├── lm-stream/         # 流抽象 + GOP 缓存 + Simulcast 路由
│   │   ├── lm-protocol/       # 协议转封装: HLS/FLV/RTMP/RTSP/SRT/GB28181/WHIP/WHEP
│   │   ├── lm-mixer/          # 音频混音 (MCU)
│   │   ├── lm-recorder/       # 分段录制 + MP4 合并 + 状态机
│   │   ├── lm-dsp/            # VAD / resample / AEC / AGC
│   │   ├── lm-router/         # 路由表
│   │   ├── lm-pipeline/       # Ingress/Egress 管线
│   │   ├── lm-control/        # gRPC 服务: session/room/track + push/pull RTP
│   │   └── lm-telemetry/      # 媒体监控: jitter/RTT/丢包
│   ├── proto/
│   │   └── media_node.proto   # gRPC 契约
│   └── bin/
│       └── media-node/        # 数据面二进制 (gRPC :50051 + HTTP :8082)
│
└── proto/                     # 共享 protobuf (Rust↔Go 契约)
    └── media/
        └── media_node.proto   # MediaNode service
```

### 为什么 monorepo 而非两仓库

- gRPC 契约（`proto/`）是两者的命门，必须同源同步
- 跨语言改动可在一个 PR 内原子提交
- CI 可分别构建 `control/` 和 `media/`，互不阻塞
- 若后期团队分离，可平滑拆仓（边界已清晰）

## 单节点 vs 多节点部署

| 模式 | 构成 | 适用 |
|------|------|------|
| 单节点 | `cmd/server`（Go）+ `bin/media-node`（Rust，子进程或同机） | 开发/小规模 |
| 多节点 | N × `control-node` + M × `media-node`，etcd 协调 | 生产 |
| 混合 | 控制面多副本，数据面按地域分布 | 跨地域 |

`cmd/server` 是"两者同机"的便捷打包：Go 进程 fork Rust media-node 子进程，通过 localhost gRPC 通信。配置可切换为远程 media-node。
