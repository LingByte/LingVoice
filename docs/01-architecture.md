# 01 — 总体架构

## 三层归属定义

在进入架构图之前，先明确三层归属——这决定了每个模块放在 Go 还是 Rust。

### Rust 层 = 实时媒体面（Real-time Media Plane）

负责**媒体字节和媒体协议的收发处理**，不负责业务决策。

| 类别 | 具体内容 | 来源 |
|------|----------|------|
| 媒体传输协议 | RTP/SRTP/WebRTC/WS-media(voip_bridge)/RTMP/WHIP | rustrtc + 新增 |
| SIP 收发 + SDP 协商 | SIP UDP/TCP/TLS/WS 收发 + SDP 解析/组装/协商 | rsipstack + negotiate.rs |
| 编解码 | Opus/PCMU/PCMA/G722/G729 + 重采样 | audio-codec |
| 媒体处理 | 转码/混音/VAD/comfort noise | rustpbx-media + 新增 VAD |
| 媒体管线 | ptime 节奏器/EgressPipeline/IngressTap | rustpbx-media（改造 egress） |
| 媒体持久化 | 录音/录像（WAV 写文件） | Recorder + WavWriter |
| 媒体监控 | jitter/RTT/丢包/RTCP 统计 | leg_stats + telemetry |

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

## RustPBX 底座：要什么和不要什么

RustPBX 是一个完整的 PBX 产品，我们只要它的**媒体底座**，其余由 Go 中台层自建。

### ✅ 我们要的（实时媒体面，约 18000 行）

| 模块 | crate/路径 | 作用 |
|------|-----------|------|
| 媒体处理 | `rustpbx-media` | Leg/Bridge/EgressPipeline/IngressTap/Mixer/Recorder |
| SIP 收发 | `rsipstack` | SIP UDP/TCP/TLS/WS 收发 + SDP 协商 |
| WebRTC/RTP | `rustrtc` | DTLS-SRTP/ICE/STUN/TURN/RTP/RTCP |
| 编解码 | `audio-codec` | Opus/PCMU/PCMA/G722/G729 + 重采样 |
| DTMF | `telephone_event` | RFC 4733 |
| WAV 读写 | `wav_reader`/`wav_writer` | 录音文件 |
| 媒体监控 | `leg_stats`/`telemetry` | jitter/RTT/丢包/RTCP 统计 |

### ❌ 我们不要的（属于 Go 中台层或控制面）

| 模块 | 路径 | 为什么不要 | 归属 |
|------|------|-----------|------|
| IVR 系统 | `src/call/app/ivr/` | 业务逻辑（菜单/收集/转接） | Go 中台层 |
| 队列系统 | `src/proxy/queue/` | 业务逻辑（排队/分配/坐席） | Go 中台层 |
| 路由系统 | `src/proxy/data/` (routes/trunks/acl) | 业务决策（路由表/中继/ACL） | Go 控制面 |
| 用户管理 | `src/proxy/user/` | 业务概念（分机/密码/注册位置） | Go 控制面 |
| 数据库 | `crates/rustpbx-storage/` + sea-orm | 控制面状态存储，我们用 etcd + Go 侧存储 | Go 控制面 |
| Web Console | `src/console/` | 管理界面，中台层自己做 | Go 控制面 |
| AMI HTTP API | `src/handler/ami.rs` | 控制面 API，中台层自己做 | Go 控制面 |
| RWI WebSocket | `src/rwi/` | 控制面 WebSocket，我们用 gRPC 替代 | Go 控制面 |
| 通话记录 | `src/callrecord/` | 业务数据（CDR） | Go 中台层 |
| Transcription | `src/call/transcription/` | AI 能力（接 Deepgram），我们插件化 | Go 中台层插件 |
| TTS | `src/call/tts/` | AI 能力（HTTP/CLI driver），我们插件化 | Go 中台层插件 |
| ACME/SSL | `src/addons/ssl/` | 运维 | Go 控制面或网关 |
| Cluster 同步 | `src/handler/ami.rs` cluster 部分 | 分布式协调，我们用 etcd | Go 控制面 |

### ⚠️ 模糊地带（已决策）

| 模块 | 决策 | 理由 |
|------|------|------|
| 录音管理 | 媒体处理留 Rust，CDR 业务留 Go | 写 WAV 是媒体处理，CDR 元数据是业务 |
| 会议管理 | 混音留 Rust，会议状态留 Go | MCU 混音是媒体处理，会议状态是控制 |
| SIP 注册 | 收发留 Rust，注册位置存储留 Go | registrar 收发在 Rust，位置表通过 gRPC 同步到 Go |
| Prometheus 媒体指标 | 保留在 Rust | 媒体面自己的 jitter/丢包/RTCP 指标 |

### 一张图看清

```
RustPBX 完整产品:
┌─────────────────────────────────────────────────┐
│  Web Console / AMI / RWI          ❌ 不要       │  控制面 API
│  IVR / Queue / Routing / User     ❌ 不要       │  业务逻辑
│  Storage / Database               ❌ 不要       │  控制面存储
│  Transcription / TTS              ❌ 不要       │  AI 能力
│  CallRecord / CDR                 ❌ 不要       │  业务数据
│  ACME/SSL / Cluster sync          ❌ 不要       │  运维/分布式
├─────────────────────────────────────────────────┤
│  rustpbx-media                    ✅ 要         │  媒体处理
│  rsipstack                        ✅ 要         │  SIP 收发
│  rustrtc                          ✅ 要         │  WebRTC/RTP
│  audio-codec                      ✅ 要         │  编解码
│  telemetry (媒体指标)             ✅ 要         │  媒体监控
└─────────────────────────────────────────────────┘
        │
        │  我们只取下面这层, 上面全部由 Go 中台层自建
        ▼
我们的 Rust 实时媒体面 = RustPBX 的媒体底座 + 改造 egress + 新增 VAD/RTMP
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
├── media/                     # Rust: 流媒体层 (基于 RustPBX 底座改造)
│   ├── Cargo.toml
│   ├── crates/
│   │   ├── rustpbx-media/     # ← fork 自 RustPBX, 改造 EgressSource→多订阅者
│   │   │   ├── egress.rs      # 改造: 互斥枚举→订阅者列表 (核心改造点)
│   │   │   ├── ingress_tap.rs # 保留: lock-free 双向 RTP 观察
│   │   │   ├── recorder.rs    # 保留: 录音
│   │   │   ├── conference_mixer.rs  # 保留+补: mute 接通
│   │   │   ├── negotiate.rs   # 保留: SDP 协商
│   │   │   ├── leg.rs         # 保留: 单腿 PeerConnection
│   │   │   ├── media_bridge.rs # 保留: 2-party B2BUA
│   │   │   └── ...
│   │   ├── audio-codec/       # ← 直接用 RustPBX 依赖: Opus/G711/G722/G729 + resample
│   │   ├── vad/               # 新增: VAD 模块 (作为 IngressTap 附加观察者)
│   │   ├── transport-rtmp/    # 新增: RTMP/WHIP/WHEP (直播补齐)
│   │   ├── media-node/        # 新增: 节点进程 gRPC server + 管线编排
│   │   └── media-proto/       # gRPC 契约 (从 proto 生成)
│   ├── deps/
│   │   ├── rustrtc/           # ← RustPBX 依赖: WebRTC/RTP (直接用)
│   │   └── rsipstack/         # ← RustPBX 依赖: SIP 栈 (直接用)
│   └── bin/
│       └── media-node/        # 数据面二进制
│
└── proto/                     # 共享 protobuf (Rust↔Go 契约)
    ├── media.proto            # MediaNode service
    ├── audio.proto            # AudioFrame / TrackRef / SinkConfig
    └── signal.proto           # 信令透传
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
