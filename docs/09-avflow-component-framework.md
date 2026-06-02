# 09 · AVFlow: 统一 AI 音视频与 LLM 编排框架

本设计致力于实现**“一切皆组件”**的理念，将原先手写的“双环调度器”（`DualLoopScheduler` 与 `Kernel`）重构为统一的、高可扩展的、基于端口与数据流的 **AI 音视频 + LLM 统一编排引擎 (AVFlow)**。

## 1. 核心设计哲学：一切皆组件 (Everything is a Component)

在传统的音视频 AI 架构中，音视频流的处理（如 VAD、ASR、TTS、音效处理）与 LLM 的认知推理（如提示词渲染、大模型调用、RAG 检索、工具链执行）是完全隔离的。这导致了双层调度逻辑极其复杂，难以扩展。

**AVFlow 的核心思想**：
1. **统一拓扑**：无论是实时音频流输入、VAD 门控、ASR 语音转文字、LLM 文本生成、TTS 语音合成，还是 Speaker 播放，都只是 Graph 中的一个**组件 (Component)**。
2. **数据通道**：组件之间通过**输入端口 (Input Ports)** 和**输出端口 (Output Ports)** 相互连接。
3. **统一数据单元**：组件之间流动的数据统一封装为 `avflow.Packet`，它既能承载底层的 `AudioPacket` 帧，也能承载高层的 `schema.Message`、控制信号 (Control Signals) 或任意 JSON 变量。
4. **反应式执行**：每个组件都在独立的 Goroutine 中运行其 `Process` 方法。当输入端口收到 Packet 时被激活，进行处理，然后向输出端口发送新的 Packet。

```
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                              AVFlow Unified Orchestration Graph                        │
│                                                                                        │
│  ┌────────────┐   Audio   ┌───────────┐   Audio   ┌───────────┐                        │
│  │ MicInput   │──────────►│    VAD    │──────────►│    ASR    │                        │
│  └────────────┘           └───────────┘           └───────────┘                        │
│                                                         │                              │
│                                                         │ Text Packet                  │
│                                                         ▼                              │
│  ┌────────────┐   Audio   ┌───────────┐   Text    ┌───────────┐                        │
│  │ SpeakerOut │◄──────────│    TTS    │◄──────────│    LLM    │◄─── [Context & Vars]   │
│  └────────────┘           └───────────┘           └───────────┘                        │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 核心原语定义

### 2.1 统一数据单元 (`avflow.Packet`)
支持多模态数据流动，统一承载音频帧、文本片段、对话消息及打断等控制信号。

```go
package avflow

import "time"

type PacketType string

const (
	PacketTypeAudio   PacketType = "audio"   // 承载 media.AudioPacket 
	PacketTypeVideo   PacketType = "video"   // 承载 视频帧数据
	PacketTypeText    PacketType = "text"    // 承载 media.TextPacket 
	PacketTypeMessage PacketType = "message" // 承载 schema.Message 
	PacketTypeControl PacketType = "control" // 承载控制信号 (如打断、重置)
	PacketTypeGeneric PacketType = "generic" // 承载任意 KV 变量或参数
)

type Packet struct {
	Type      PacketType
	Data      any
	Timestamp time.Time
	Metadata  map[string]any
}
```

### 2.2 统一组件接口 (`avflow.Component`)
任何业务逻辑（从底层的 WebRTC 接入到大语言模型）只需实现这个极简的接口：

```go
type Component interface {
	ID() string                             // 组件唯一标识 (e.g. "asr-node")
	Type() string                           // 组件类型 (e.g. "ASR")
	Inputs() []string                       // 定义所有的输入端口名 (e.g. ["audio_in"])
	Outputs() []string                      // 定义所有的输出端口名 (e.g. ["text_out"])
	
	// 反应式处理函数。Graph 运行器会为其分配独立的 goroutine 运行，
	// inputs 包含与外部连接的输入 channel，outputs 包含发往后续组件的输出 channel。
	Process(ctx context.Context, inputs map[string]<-chan *Packet, outputs map[string]chan<- *Packet) error
}
```

### 2.3 统一编排图 (`avflow.Graph`)
负责管理组件、建立端口连接、编译拓扑，并在多协程下调度整个管道的流式运行。

```go
type Edge struct {
	FromNode string
	FromPort string
	ToNode   string
	ToPort   string
}

type Graph struct {
	name       string
	components map[string]Component
	edges      []Edge
}
```

---

## 3. 重构路线与现有代码兼容

由于我们现有的代码已经完成了：
- `pkg/media`（含 codecs, MediaSession, VAD, EventBus）
- `pkg/llm/compose`（含 Eino Graph, Chain, Lambda）

我们**不需要暴力擦除**现有的所有实现。相反，我们可以通过 **适配器模式 (Adapter Pattern)**，将现有的 media、llm、compose 包装为 `avflow.Component`：
1. **`compose.Graph` 适配器**：将 Eino LLM Graph 包装成一个 `LLMComponent`，它的输入是文本或消息 Packet，输出是流式生成的文本 Packet。
2. **`media.MediaSession` 适配器**：提供 `MicInputComponent` 和 `SpeakerOutputComponent` 两个端点组件，在组件运行时与底层的 `MediaSession` RTP/Transport 进行交互。
3. **`vad.Detector` 适配器**：将现有的能量 VAD 包装为 `VADComponent`，负责过滤静音音频包、检测 Barge-in 并通过输出端口广播打断信号。
4. **ASR / TTS 适配器**：将底层的 ASR/TTS 接口包装成标准组件，通过通道接收/输出数据。

这将极大提高整个框架的**扩展性**：
- 如果我们想加入 **视频多模态组件** (Video Input -> Vision LLM -> TTS)，只需新增 `CameraInputComponent` 和 `VisionLLMComponent`，在 Graph 中进行连线，即可无缝支持视频客服。
- 如果我们想加入 **多语种实时翻译** (ASR -> TranslationComponent -> LLM)，只需在 Graph 拓扑的 `asr` 与 `llm` 节点之间插入一个 `translation` 节点。
- 如果需要做 **人机协作 (HITL) 审批**，可以通过特殊的 `GateComponent`，在指定端口暂存 Packet，待人工审批后继续向下游流动。

---

## 4. 下一步开发计划

1. **新建组件包** `pkg/avflow/` 并实现核心原语、数据通道及 `Graph` 运行器调度。
2. **实现核心的基础组件库**：
   - `LambdaComponent`：方便用户通过快速闭包编写自定义中间处理器。
   - `MicComponent` & `SpeakerComponent`：与现有的 Transport 桥接。
   - `ASRComponent` & `TTSComponent`：完成语音到文本、文本到语音的转换组件。
   - `LLMComponent`：无缝连接 LLM 或 `compose.Graph` 编排。
   - `VADComponent`：实时对 AudioPacket 进行过滤。
   - `BargeInComponent`：打断控制器组件。
3. **编写统一编排的全新 Demo** (`cmd/avflow-demo/`)，构建起一套完全基于组件和连线的多模态实时语音通话流程，证明其扩展性和极高的优雅度。
