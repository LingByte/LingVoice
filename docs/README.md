# LingVoice 设计文档

LingVoice 是一个**通用语音/流媒体中台**：以插件化架构支持 WebRTC / WebSocket / SIP / RTMP / RTSP / SRT / GB28181 / WHIP / WHEP 等多种协议接入，覆盖语音 Agent、会议转写、SIP 通话/外呼、直播推流/拉流、安防监控五大场景，分布式从第一天设计。

## 核心架构决策

1. **双语言分层**：Rust 实现流媒体层（传输/管线/DSP/录制/转封装），Go 实现信令与 AI 编排层（会话/ASR/TTS/LLM/插件）。两者通过 gRPC 契约解耦。
2. **自研 Rust 媒体面**：Rust 流媒体层从零设计，参考 Xiu、atm0s-media-server、Waterbus 等开源项目的架构思想，构建 `lm-core` / `lm-stream` / `lm-depacketizer` / `lm-protocol` / `lm-recorder` 等 crate。核心抽象：`MediaFrame`（帧级）+ `Depacketizer`（RTP→Frame）+ `StreamSink`（订阅者）+ `Remuxer`（帧→协议输出）。
3. **混合媒体管线**：ptime 节奏器（电话音频的强节奏保证）+ 零拷贝 RTP 直转（同 codec 不进 PCM 域）+ 多订阅者 fan-out（pub/sub 的核心优势）。详见 [07-media-pipeline.md](./07-media-pipeline.md)。
4. **控制面 / 数据面分离**：控制面无状态（etcd 存会话路由），数据面（Rust media node）水平扩展、按会话亲和。
5. **协议边界清晰**：Go 负责协议监听/握手/信令，Rust 只处理帧级媒体。Go 解包后通过 gRPC 把 RTP 喂给 Rust，Rust 组装为 `MediaFrame` 后做路由/混音/录制/转封装。
6. **插件化**：能力（ASR/TTS/LLM/Recorder/Detector）均为插件，支持进程内（Go registry）与进程外（gRPC sidecar）两种形态，上层接口一致。
7. **库 + 可选 server**：核心是库，同时提供开箱即用的 server 二进制。

## 文档索引

| 文档 | 内容 |
|------|------|
| [00-vision.md](./00-vision.md) | 目标、边界、双语言分层动机、设计原则 |
| [01-architecture.md](./01-architecture.md) | 总体架构：控制/数据面、Rust/Go 边界、分层与目录 |
| [02-plugin-system.md](./02-plugin-system.md) | 插件系统：双形态、能力契约、加载与发现 |
| [03-distributed.md](./03-distributed.md) | 分布式：会话调度、集群状态、故障转移、SFU 级联 |
| [04-interface-contract.md](./04-interface-contract.md) | Rust↔Go gRPC 契约（最关键的边界） |
| [05-roadmap.md](./05-roadmap.md) | 分阶段路线图 |
| [06-transport-unification.md](./06-transport-unification.md) | 传输协议归一化：WS/RTP/RTMP 统一到 MediaFrame、时钟域 |
| [07-media-pipeline.md](./07-media-pipeline.md) | 媒体管线：ptime 节奏器 + 多订阅者出口（混合模型） |
| [08-rustpbx-research.md](./08-rustpbx-research.md) | 历史调研记录（RustPBX 选型已废弃，保留作为决策档案） |
| [09-protocol-layer.md](./09-protocol-layer.md) | 协议层设计：SIP/WebRTC/WS 拆分边界、Go 协议应用层、统一事件抽象 |
| [10-rust-media-architecture.md](./10-rust-media-architecture.md) | Rust 媒体面架构：crate 设计、传输/编解码/管线/路由 |
| [11-current-flow-and-status.md](./11-current-flow-and-status.md) | 当前流转状态：实际媒体流路径、各协议接入情况、后续路线 |
| [12-websocket-demo.md](./12-websocket-demo.md) | WebSocket 协议设计、消息格式、ws-rust-demo 完整链路 |
| [13-scaling-architecture.md](./13-scaling-architecture.md) | 大规模会议架构：音频混音(MCU)、视频按需订阅(simulcast)、分片SFU |
| [14-streaming-media-redesign.md](./14-streaming-media-redesign.md) | 流媒体层重构方案与实施记录：MediaFrame + Depacketizer + Stream + GOP + Simulcast + 协议转封装 |

## 阅读顺序

第一次阅读建议按 `00 → 01 → 07 → 09 → 04 → 06 → 14 → 02 → 03 → 05 → 11`。

- `07` 是混合媒体管线的详细设计，理解 ptime 节奏器 + 多订阅者出口的混合模型。
- `09` 是协议层拆分边界，明确协议传输在 Rust、协议应用在 Go。
- `04` 是双语言分离的命门，先看它再回头看分层会更清楚。
- `06` 是传输协议归一化，WS 与 RTP 怎么混合。
- `14` 是流媒体层重构的完整方案与实施记录，包含 MediaFrame/Depacketizer/Stream/GOP/Simulcast/协议转封装的设计与实现状态。
- `08` 是历史调研档案，RustPBX 选型已废弃，保留作为决策记录。
