# 04 · pkg/protocol LLM 协议层

> 参考 [CloudWeGo Eino](https://github.com/cloudwego/eino) 的 `schema` 与 `components/model` 设计，在 LingVoice 内实现独立的协议层，**不 import eino-main**。

## 目录结构

```
pkg/protocol/
├── doc.go
├── schema/                 # 数据协议（≈ eino/schema 子集）
│   ├── role.go
│   ├── message.go
│   ├── part.go             # 多模态 Part（含 audio，为语音预留）
│   ├── tool.go             # ToolCall, ToolInfo, ToolChoice
│   ├── response.go         # ResponseMeta, TokenUsage
│   ├── builder.go          # SystemMessage / UserMessage / …
│   ├── concat.go           # ConcatMessages（流式 chunk 合并）
│   └── stream.go           # Pipe, StreamReader, CollectMessages
└── llm/                    # 模型契约（≈ eino/components/model）
    ├── model.go            # ChatModel, ToolCallingChatModel
    ├── option.go           # Options, WithTools, WithToolChoice
    └── errors.go
```

## 与 Eino 的对照

| Eino | LingVoice protocol | 说明 |
|------|-------------------|------|
| `schema.Message` | `schema.Message` | 文本 + tool + 多模态；暂未做 deprecated MultiContent |
| `schema.ToolCall` | `schema.ToolCall` | 含 Index，支持流式 merge |
| `schema.ToolInfo` | `schema.ToolInfo` | Params 用 map 或 raw JSON Schema |
| `schema.ConcatMessages` | `schema.ConcatMessages` | 流式合并 |
| `schema.StreamReader` | `schema.StreamReader` | 简化版 Pipe/Recv/Close |
| `model.BaseChatModel` | `llm.ChatModel` | Generate + Stream |
| `model.ToolCallingChatModel` | `llm.ToolCallingChatModel` | WithTools 不可变绑定 |
| `model.Option` | `llm.Option` | 温度、tools、tool_choice |

**刻意未实现（后续按需加）：**

- AgenticMessage（ADK 事件流已覆盖主路径）
- Eino 完整 Stream Copy/Merge 全家桶

**已实现（2026-05-30）：**

- `schema.Document` — RAG 文档单元
- `retriever.Retriever` — 可插拔检索（InMemory keyword + `pkg/knowledge` 向量/hybrid）
- `rag.Chain` / `rag.NewKnowledgeChain` / `rag.NewIndexedServiceChain` — 检索 + 消息构建
- `a2a` — JSON-RPC 2.0 / REST / SSE；push retry + dead-letter + redrive
- `knowledge.MemoryHandler` — 本地内存向量库（无需 Qdrant/Milvus）

**仍待扩展：**
- jsonschema 强依赖（Tool 参数仍用 map / raw JSON）

## 使用示例

```go
import (
    "github.com/LingByte/LingVoice/pkg/protocol/schema"
    "github.com/LingByte/LingVoice/pkg/protocol/llm"
)

msgs := []*schema.Message{
    schema.SystemMessage("You are a helpful assistant."),
    schema.UserMessage("Hello"),
}

// Provider 实现 llm.ChatModel 后：
// out, err := model.Generate(ctx, msgs, llm.WithTemperature(0.7))

// 流式合并
sr, sw := schema.Pipe[*schema.Message](8)
go func() {
    defer sw.Close()
    sw.Send(schema.AssistantMessage("Hi", nil), nil)
}()
full, _ := schema.CollectMessages(sr)
```

## 下一步

1. ~~`pkg/llm/openai` — 第一个 ChatModel 实现~~ ✅
2. ~~`pkg/llm/anthropic` — Anthropic Messages API 适配~~ ✅
3. ~~`cmd/llm-demo` — 手工验证~~ ✅
4. `pkg/orchestrate` — Chain / ToolLoop（依赖 protocol/llm）
5. 语音层只通过 `InputPart{Type: PartTypeAudio}` 传协议，不污染 llm 包

## Provider 适配

| 包 | API | 环境变量 |
|----|-----|----------|
| `pkg/llm/openai` | OpenAI Chat Completions（及兼容网关） | `OPENAI_API_KEY` |
| `pkg/llm/anthropic` | Anthropic Messages | `ANTHROPIC_API_KEY` |

### llm-demo

```bash
# OpenAI
OPENAI_API_KEY=sk-... go run ./cmd/llm-demo -provider openai -prompt "Hello"

# Anthropic（流式）
ANTHROPIC_API_KEY=sk-ant-... go run ./cmd/llm-demo -provider anthropic -stream

# 自定义 OpenAI 兼容网关
OPENAI_API_KEY=... go run ./cmd/llm-demo -base-url https://your-gateway/v1
```
