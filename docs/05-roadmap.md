# 05 — 路线图

分布式从第一天**设计**，但交付分阶段。每阶段都是一条**端到端可演示**的纵切，不横向铺所有模块。

> **注**：原 Phase 1 的 RustPBX 底座方案已废弃，改为自研 Rust 媒体面。实际实施进度见下方"实际实施记录"。

## 实际实施记录

### Phase 1 — Rust 媒体基础 + gRPC 控制面 + WebRTC/WS demo ✅

**目标**：自研 Rust 媒体面基础 crate，gRPC 控制面，WebRTC + WebSocket 端到端 demo。

**已完成**：
- Rust 媒体面 10+ crate：`lm-core` / `lm-codecs` / `lm-transport` / `lm-control` / `lm-mixer` / `lm-recorder` / `lm-dsp` / `lm-router` / `lm-pipeline` / `lm-telemetry`
- gRPC 控制面：session/room/track CRUD + push/pull RTP + kind-aware 路由
- Go 协议层：7 种协议 adapter（WebRTC/WS/SIP/RTMP/WHIP/WHEP/MQTT）
- Go↔Rust 桥接：rustbridge per-track push/pull
- WebRTC 端到端 demo：音频+视频双向 room 路由
- WebSocket 端到端 demo：音频+文本
- WHIP/WHEP/RTMP/MQTT/SIP 端到端 demo

### Phase 2 — 流媒体层重构 ✅

**目标**：参考 Xiu/atm0s/Waterbus，重构流媒体层为帧级抽象。

**已完成**（详见 [14-streaming-media-redesign.md](./14-streaming-media-redesign.md)）：
- Phase 1：`MediaFrame` 抽象 + `Depacketizer`（VP8/H264/Opus）+ `MediaStream` 重构
- Phase 2：GOP 缓存 + 快速首屏
- Phase 3：分段录制 + MP4 合并 + 录制状态机
- Phase 4：Simulcast（RID 路由 + 层选择 + Dynacast）
- Phase 5：协议转封装（WebRTC→HLS/HTTP-FLV/RTMP remuxer）
- Phase 6-10：RTMP/RTSP/SRT/GB28181/WHIP/WHEP remuxer 框架
- media-node HTTP 输出服务：HLS playlist + TS 分段 + HTTP-FLV chunked stream
- 端到端验证：gRPC PushRtp → MediaStream → Depacketizer → MediaFrame → HlsRemuxer → HLS playlist

### Phase 3 — 控制面/数据面分离 + 多节点 + 真实插件（待实施）

**目标**：control-node 与 media-node 分离部署，etcd 协调，多 media node 水平扩展。接 OpenAI 真实 ASR/TTS/LLM。文件录制。

### 新增
- `control/control`：`Scheduler`（选 media node）、`Cluster`（etcd client）、`SignalingGateway`（信令路由）
- `control/cmd/control-node`、`control/cmd/media-node`（Go 侧 wrapper，可选）
- etcd 键空间 + 心跳 + 路由表
- `media/sink`：file recorder（WAV/FLAC）
- `capability/recorder` + `file-recorder` 插件
- `plugin/rpc`：进程外插件 gRPC 协议 + `RemotePlugin` 适配器
- 真实插件：`openai-asr`/`openai-tts`/`openai-llm`（in-process）
- 路径 B 雏形：`StartEgress`/`StartIngress`（Rust 直连插件，先做 ASR 单向）

### 交付演示
两台 media node，控制面调度会话到负载低者；浏览器接任一 control node 信令；真实 OpenAI 对话；通话录音落盘。

---

## Phase 3 — 场景补齐：SIP / 会议 / 直播 / 多供应商

**目标**：四大场景全部可用，插件生态铺开。

### SIP 通话/外呼
- `media/transport/sip`：SIP 信令 + RTP（Rust）
- `control/orchestrate/workflow`：IVR / 外呼 flow（图式）
- SIP trunk 接入演示

### 会议/转写
- `media-core/Mixer`：多方混音
- `session/room`：多参与方会话
- `capability/recorder`：多轨录制 + 混音录制
- 转写 sink：每轨 ASR → 字幕流

### 直播/推流
- `media/transport/rtmp`：RTMP 推/拉
- `media/transport/whip`：WHIP/WHEP
- `media/sink/streamer`：转推

### 多供应商插件
- `deepgram-asr`、`volcengine-asr/tts`、`funasr-asr`（Python，进程外，路径 B）
- `s3-recorder`

### 路径 B 完善
- TTS 双向直连（`StartIngress` 从 TTS 插件拉音频）
- 插件双通道（控制 + 媒体）完整实现

---

## Phase 4 — 规模化与生产化

- SFU 级联（跨 node 多方会议）
- media node 自动伸缩（K8s operator 或自研调度）
- 多租户隔离（资源配额、网络隔离）
- 可观测性完善（OTel trace 贯通、媒体诊断 dashboard）
- 热重载插件、灰度发布
- 安全：TLS、信令鉴权、插件签名
- 故障转移演练、混沌测试

## 阶段验收标准

| 阶段 | 验收 |
|------|------|
| P1 | 单节点 mock 端到端 + 打断；双语言 gRPC 契约跑通；插件 YAML 加载 |
| P2 | 双节点调度；真实 OpenAI 对话；录音落盘；进程外插件框架可用 |
| P3 | SIP/会议/直播各一个端到端 demo；≥3 家 ASR/TTS 插件；路径 B 跑通 |
| P4 | 跨 node 会议；自动伸缩；多租户；故障转移可演示 |
