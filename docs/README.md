# LingVoice 设计文档

LingVoice 是一个**通用语音中台**：以插件化架构支持 WebRTC / WebSocket / SIP / RTMP 等多种协议接入，覆盖语音 Agent、会议转写、SIP 通话/外呼、直播推流四大场景，分布式从第一天设计。

## 核心架构决策

1. **双语言分层**：Rust 实现流媒体层（传输/管线/DSP/录制），Go 实现信令与 AI 编排层（会话/ASR/TTS/LLM/插件）。两者通过 gRPC 契约解耦。
2. **RustPBX 底座**：Rust 流媒体层基于 RustPBX 的 `rustpbx-media` + `rsipstack` + `rustrtc` + `audio-codec` 改造，不重造底层音频处理轮子。核心改造点：`EgressSource` 互斥枚举 → 多订阅者列表。详见 [08-rustpbx-research.md](./08-rustpbx-research.md)。
3. **混合媒体管线**：ptime 节奏器（RustPBX 的强节奏保证）+ RewriteRelay 零拷贝（同 codec 直转）+ 多订阅者 fan-out（pub/sub 的核心优势）。详见 [07-media-pipeline.md](./07-media-pipeline.md)。
4. **控制面 / 数据面分离**：控制面无状态（etcd 存会话路由），数据面（Rust media node）水平扩展、按会话亲和。
5. **插件化**：能力（ASR/TTS/LLM/Recorder/Detector）均为插件，支持进程内（Go registry）与进程外（gRPC sidecar）两种形态，上层接口一致。
6. **库 + 可选 server**：核心是库，同时提供开箱即用的 server 二进制。

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
| [08-rustpbx-research.md](./08-rustpbx-research.md) | RustPBX 调研、架构对比、钢人论证、底座选型决策 |
| [09-protocol-layer.md](./09-protocol-layer.md) | 协议层设计：SIP/WebRTC/WS 拆分边界、Go 协议应用层、统一事件抽象 |

## 阅读顺序

第一次阅读建议按 `00 → 01 → 08 → 07 → 09 → 04 → 06 → 02 → 03 → 05`。

- `08` 是架构选型的决策记录，包含 RustPBX 调研、钢人论证、混合模型结论——先看它理解为什么选这条路。
- `07` 是混合媒体管线的详细设计，是 `08` 结论的技术落地。
- `09` 是协议层拆分边界，纠正之前"SIP 全在 Rust"的误解，明确协议传输在 Rust、协议应用在 Go。
- `04` 是双语言分离的命门，先看它再回头看分层会更清楚。
- `06` 是传输协议归一化，WS 与 RTP 怎么混合。
