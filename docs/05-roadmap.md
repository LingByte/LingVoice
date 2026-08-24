# 05 — 路线图

分布式从第一天**设计**，但交付分阶段。每阶段都是一条**端到端可演示**的纵切，不横向铺所有模块。

## Phase 1 — RustPBX 底座接入 + Egress 改造 + 单节点语音 Agent 纵切

**目标**：以 RustPBX 媒体层为底座，改造 EgressSource 为多订阅者，补 VAD，上面建 Go 中台层。浏览器 WebSocket/WebRTC 接入 → VAD → Mock ASR → Mock LLM → Mock TTS → 播放，支持打断。验证底座改造与双语言分层。

### Rust 侧（media/）— 基于 RustPBX 底座
- **fork `rustpbx-media` crate**：作为 `media/crates/rustpbx-media/`
- **核心改造 `egress.rs`**：`EgressSource` 互斥枚举 → `EgressSubscriber` 订阅者列表（保留 ptime 节奏器 + RewriteRelay 零拷贝路径）
- **保留不动**：`ingress_tap.rs` / `recorder.rs` / `negotiate.rs` / `leg.rs` / `media_bridge.rs` / `conference_mixer.rs` / `audio-codec` / `rustrtc` / `rsipstack`
- **新增 VAD 模块**：`media/crates/vad/`，作为 `IngressTap` 的附加观察者
- **新增 `media-node`**：gRPC server，实现 `MediaNode` service 的 `CreateSession`/`AddTrack`/`PublishAudio`/`SubscribeAudio`/`HandleSignaling`，复用 RustPBX 的 Leg/Bridge
- `bin/media-node`：可独立启动

### Go 侧（control/）
- `proto/`：定义全部契约（即使 Phase 1 只用一部分）
- `session`：`AgentSession` + `TurnManager`（端点检测、barge-in）
- `capability`：`ASR`/`TTS`/`LLM` 接口
- `plugin`：进程内 registry + loader（YAML 配置）
- `orchestrate`：`AgentLoop`（listen→ASR→LLM→TTS→speak，含打断）
- `control`：最小 API server（HTTP/WS 信令）+ 本地 media node 调度（同进程或 localhost）
- `cmd/server`：Go 进程 fork Rust media-node 子进程，localhost gRPC
- mock 插件：`mock-asr`（回显固定文本）、`mock-tts`（生成正弦波）、`mock-llm`（回显）

### 交付演示
浏览器 WS 连 `cmd/server`，推 opus 音频 → RustPBX 底座接收 → VAD 检测说话结束 → Go AgentLoop 调 mock ASR/LLM/TTS → Rust 播放 TTS 音频回浏览器。说话中打断 TTS 播放。

### 不做
- etcd、多节点、SIP 通话（底座有但 Phase 1 不接）、RTMP、会议混音、真实 ASR/TTS、进程外插件、录制

---

## Phase 2 — 控制面/数据面分离 + 多节点 + 真实插件

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
