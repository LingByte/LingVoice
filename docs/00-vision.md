# 00 — 愿景与边界

## 一句话定位

LingVoice 是一个**协议无关、能力插件化、分布式原生**的语音中台。它把"音视频流的接入、传输、处理"和"对话智能的编排"分离开，让业务方用统一 API 接入任意协议，按需插拔 ASR/TTS/LLM/录制等能力。

## 目标场景

| 场景 | 描述 | 关键能力 |
|------|------|----------|
| 语音 Agent | 实时对话 AI：ASR→LLM→TSS 低延迟轮次，支持打断/barge-in | turn-taking、端点检测、流式 ASR/TTS |
| 会议/转写 | 多方音视频会议、实时字幕、录音 | 混音、多轨录制、转写 sink |
| SIP 通话/外呼 | SIP trunk 接入、IVR、坐席、通话录音 | SIP/RTP、外呼 workflow、坐席转接 |
| 直播/推流 | 推拉流、RTMP/WHIP/WHEP 转发 | RTMP、WHIP/WHEP、转推 |

首期纵切打通**语音 Agent**，其余场景作为插件/transport 逐步补齐。

## 非目标（首期不做）

- 不自研 SIP 软交换（接 Asterisk/FreeSWITCH 或纯 SIP trunk 即可）
- 不做 MCU 跨节点无缝迁移（媒体流不可迁移，业界惯例是故障重建）
- 不做自有 LLM/ASR 模型（全部走插件接第三方）
- 不做前端 SDK（提供协议接入，前端用标准 WebRTC/WS 客户端）

## 双语言分层：为什么不是纯 Go

Go 在语音中台里有一个真实痛点：**音频处理与传输**。

### Go 的短板

- GC 暂停（即便 <10ms）在 20ms 音频帧粒度下会引入抖动
- 零拷贝音频管线表达力弱，slice 复制多
- WebRTC SFU 生态（pion）虽好，但高并发下 CPU/内存不如 Rust 原生
- DSP（VAD/resample/AEC/AGC）在 Go 里要么手写要么 cgo，cgo 抹平了 Go 的优势

### Rust 的长板

- 零拷贝、无 GC、确定性延迟
- 成熟的纯 Rust WebRTC/RTP 生态（webrtc-rs、str0m 等）
- `audio-codec` 编解码生态（Opus/G711/G722/G729 + 重采样）原生
- 内存安全 + 高并发（tokio）

### Go 仍然不可替代的地方

- 信令网关：大量 WS/HTTP/gRPC 连接，goroutine 模型极佳
- ASR/TTS/LLM 集成：IO 密集，SDK 生态丰富
- 业务编排、插件系统、API server：开发效率高
- 分布式协调（etcd client、scheduler）

### 结论：分层而非混写

```
Rust 实时媒体面                   Go 控制面 + 中台层
(媒体传输 / 编解码 / 管线 / DSP)    (信令协调 / 会话 / ASR/TTS/LLM / 编排 / 插件 / 分布式)
       ↑                                    ↑
       └────────── gRPC 契约 ───────────────┘
        (04-interface-contract.md)
```

**关键设计**：音频流可以 Rust↔插件直连，**绕过 Go**。Go 只做控制决策，不代理媒体字节。这样 Go 的 GC 抖动不影响音频路径。详见 [04-interface-contract.md](./04-interface-contract.md)。

### 三层归属定义

| 层 | 语言 | 职责 | 实时性 |
|----|------|------|--------|
| **实时媒体面** | Rust | 媒体传输协议、SIP 收发+SDP 协商、编解码、媒体处理（转码/混音/VAD）、媒体管线（ptime 节奏器/EgressPipeline）、录音、媒体监控 | 20ms 硬保证 |
| **控制面** | Go | 业务决策（SIP 路由/鉴权/转接）、信令协调（WebRTC SDP/ICE、信令 WS）、会话管理、分布式协调（etcd/调度/多租户） | 100ms 级 |
| **中台层** | Go | AI 编排（ASR/TTS/LLM/Agent Loop/打断）、插件系统（registry/loader/rpc）、业务数据（CDR/通话记录） | 100ms 级 |

**协议归属的关键区分**：传的是控制消息还是媒体字节。WebRTC 的 SDP/ICE 协调在 Go，PeerConnection 媒体在 Rust；WebSocket 作为信令通道在 Go，作为媒体通道（voip_bridge PCM16）在 Rust；SIP 收发在 Rust，SIP 业务决策在 Go。详见 [01-architecture.md](./01-architecture.md)。

## 设计原则

1. **接口先行，跨层不 import 实现**：每层只依赖下层抽象接口。
2. **协议无关**：transport 是插件，核心不写死 WebRTC/SIP。
3. **能力可插拔**：ASR/TTS/LLM/Recorder 全是插件，进程内或进程外同接口。
4. **分布式原生**：控制/数据面分离，会话状态在 etcd，数据面水平扩展。
5. **可观测**：每个会话有 trace，媒体路径有诊断（丢包/抖动/延迟）。
6. **纵切优先**：每阶段交付一条端到端可演示链路，不横向铺所有模块。

## 与参考项目的关系

**Rust 媒体面从零自研**，参考 Xiu（协议转换与流媒体枢纽）、atm0s-media-server（分布式 SFU）、Waterbus（会议 SFU + HLS 输出）三个开源项目的架构思想，构建 `lm-core` / `lm-stream` / `lm-depacketizer` / `lm-protocol` / `lm-recorder` 等 crate。核心抽象：`MediaFrame`（帧级）+ `Depacketizer`（RTP→Frame）+ `StreamSink`（订阅者）+ `Remuxer`（帧→协议输出）。详见 [14-streaming-media-redesign.md](./14-streaming-media-redesign.md)。

参考 LiveKit（SFU + Agent）、LiveKit SIP、Eino（编排）、LiveKit Agents 的思路，**不直接集成其代码**。LiveKit 的 Rust SFU 和 Agents 的 Go/Python plugin 模型是分层的重要参考。
