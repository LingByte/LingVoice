# 01 · 分层栈

自底向上，能力逐层叠加。**L0–L2 + A2A/RAG 扩展已实现；L3+ 待建设。**

```
┌─────────────────────────────────────────────────────────┐
│ L5 控制面     租户、Campaign、脚本编辑器、监控            │  未开始
├─────────────────────────────────────────────────────────┤
│ L4 媒体接入   SIP / WebRTC Gateway、RTP、Dispatch        │  未开始
├─────────────────────────────────────────────────────────┤
│ L3 语音运行时  ASR / TTS / VAD / Turn / Barge-in         │  未开始
├─────────────────────────────────────────────────────────┤
│ L2 编排图     Graph、Pregel BSP、HITL、Runnable 四模式   │  ← 已完成
├─────────────────────────────────────────────────────────┤
│ L1 最小编排   Chain、Pipeline、ToolLoop、Parallel        │  ← 已完成
├─────────────────────────────────────────────────────────┤
│ L0 LLM 基座   Message、Document、Model、Stream           │  ← 已完成
└─────────────────────────────────────────────────────────┘
```

## L0 · LLM 基座（已完成）

| 原语 | 说明 |
|------|------|
| `Message` | 对话消息（role + content + tool） |
| `Document` | RAG 检索文档单元 |
| `ChatModel` | Generate / Stream / WithTools |
| `Run` / `Context` | 调用生命周期与 trace |

**包**：`pkg/protocol`、`pkg/llm/openai`、`anthropic`、`metrics`、`callback`

## L1 · 最小编排（已完成）

| 能力 | 说明 |
|------|------|
| `Chain` / `ChainBuilder` | 顺序执行；CompileGraph 注册 ChatModel 流式 |
| `GenericChain[I,O]` / `GenericGraph[I,O]` | 类型化 I/O |
| `Pipeline` / `ToolLoop` | Template → Branch → ReAct |
| `MessageRunnable` | Invoke / Stream / Transform / Collect 统一入口 |

**包**：`pkg/llm/compose`、`prompt`、`tool`

## L2 · 编排图（已完成）

| 能力 | 说明 |
|------|------|
| `Graph` + Branch + SubGraph | checkpoint / HITL |
| `ProcessState[S]` | per-run 局部状态 |
| `GraphRun` + Pregel channel | BSP 超步、Last/Append/Reduce/Broadcast/Barrier |
| `ReAct Graph` | StreamFrames + mid-stream resume |
| ADK + Multi-Agent | supervisor、plan-execute、HostCompose |
| **A2A** | `pkg/llm/a2a` JSON-RPC 2.0 + REST + SSE；Client `SubscribeToTask`；push webhook 重试 + dead-letter |
| **RAG / 知识库** | `pkg/knowledge` 策略分片 + Qdrant/Milvus；`embed`/`retrieve` 策略；`Service` 门面；Bleve hybrid + rerank |

**包**：`compose`、`agent`、`adk`、`a2a`、`rag`、`session`、`knowledge`、`search`

## L3 · 语音运行时（M1 进行中）

| 能力 | 说明 |
|------|------|
| `protocol/media` | Utterance、SessionEvent、PlaybackCue |
| `runtime/av` | DualLoopScheduler、SessionRuntime、Bridge Processor |
| `media` | 编解码、MediaSession、EventBus（自 SoulNexus 迁入） |
| `compose/graph_media` | Utterance 输入 / Echo 回复节点 |

**包**：`pkg/protocol/media`、`pkg/runtime/av`、`pkg/media`、`compose/graph_media.go`

详见 [08-av-llm-orchestration.md](./08-av-llm-orchestration.md)。

## L4+ · 后续层

- **L4 Media**：SIP / WebRTC Gateway；可与 A2A 互操作
- **L5 Control**：Campaign、脚本编辑器、监控

## 依赖规则

```
L5 → L4 → L3 → L2 → L1 → L0
```

## 测试与文档

```bash
go test ./pkg/llm/... -cover
```

- 能力对照：[07-eino-parity.md](./07-eino-parity.md)
- Demo：[cmd/README.md](../cmd/README.md)
