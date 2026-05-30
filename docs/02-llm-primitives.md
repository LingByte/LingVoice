# 02 · LLM 基座原语（L0）

> **本文档是 Phase 0 的核心。** 商讨时请优先对齐这里的类型与接口，再写代码。

## 1. Message

对话的最小单元。现有种子位于 `pkg/protocol/schema/message.go`，Phase 0 需要扩展但保持向后兼容。

### 1.1 Role

```go
type Role string

const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
    RoleTool      Role = "tool"
)
```

### 1.2 基础结构（当前 + 建议扩展）

**已实现（`pkg/protocol/schema`）：**

```go
type Message struct {
    Role                 RoleType
    Content              string
    UserInputParts       []InputPart   // 多模态输入（含 audio，为语音预留）
    AssistantOutputParts []OutputPart
    ToolCalls            []ToolCall
    ToolCallID, ToolName string
    ResponseMeta         *ResponseMeta
    ReasoningContent     string
    Extra                map[string]any
}
```

**后续可选扩展：**

| 字段 | 类型 | 用途 |
|------|------|------|
| `ID` | string | 单条消息 id，trace 用 |
| 更细粒度 LogProbs | — | 调试 / 评估 |

**Phase 0 明确不做：**

- 多模态 Part（image/audio）— 留空字段或 Phase 1 再加
- Token 用量 — 放在 `Run` 结果里，不塞进 Message

### 1.3 待商讨

- [ ] `RoleType` 与 `Role` 是否合并为一个类型？
- [ ] Message 放在 `pkg/protocol/schema` 还是 `pkg/llm`？
- [ ] Content 是否支持 structured output（JSON mode）单独字段？

---

## 2. ChatModel

模型调用的统一接口。所有供应商（OpenAI、Claude、本地 Ollama）都实现此接口。

```go
// pkg/llm/model.go（规划）

type ChatModel interface {
    // Name 返回提供商标识，如 "openai/gpt-4o-mini"
    Name() string

    // Generate 同步调用，直到模型返回完整回复
    Generate(ctx context.Context, input []Message, opts ...CallOption) (Message, error)

    // Stream 流式调用，返回增量 chunk；调用方负责 Close
    Stream(ctx context.Context, input []Message, opts ...CallOption) (StreamReader[Message], error)
}
```

### 2.1 CallOption

调用级参数，不污染 Message：

```go
type CallOption struct {
    Temperature *float64
    MaxTokens   *int
    Stop        []string
    // JSONMode bool — 可选，Phase 0 可省略
}
```

### 2.2 StreamReader

Phase 0 最小约定：

```go
type StreamReader[T any] interface {
    Recv() (T, error)  // io.EOF 表示结束
    Close() error
}
```

**待商讨：**

- [ ] Stream chunk 是「delta Message」还是「累积 Message」？
- [ ] 是否在 L0 做 Invoke/Stream/Collect 四种模式（参考 Eino Runnable），还是 Phase 0 只做 Generate + Stream？

---

## 3. Run

一次模型调用的 **执行记录**，用于日志、调试、后续语音 turn 对齐。

```go
type Run struct {
    ID        string
    Model     string            // ChatModel.Name()
    Input     []Message         // 送入模型的 messages（快照）
    Output    Message           // 最终 assistant 消息
    StartedAt time.Time
    EndedAt   time.Time
    Error     error             // nil 表示成功
    Usage     *TokenUsage       // 可选
    Metadata  map[string]string // 如 tenant_id, session_id（L0 不强制）
}

type TokenUsage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
}
```

### 3.1 Run 与 Session 的区别

| 概念 | 层级 | 说明 |
|------|------|------|
| **Run** | L0 | 单次 ChatModel 调用 |
| **Session** | L1+ | 多轮 Run + Tool + 状态；Phase 0 不定义 |

Phase 0 只保证：每次 `Generate`/`Stream` 都能产出一条 `Run` 记录。

---

## 4. Context

Go 的 `context.Context` 负责 cancel/timeout；我们额外约定 **可选** 的 trace 字段（通过 context value 或显式 struct）：

```go
type RunContext struct {
    RunID     string
    TraceID   string
    SessionID string // 可选，L0 可留空
}
```

**原则**：L0 不引入全局变量；ChatModel 实现从 ctx 取 RunID 打日志即可。

---

## 5. Provider 注册（Phase 0 极简）

不做 Eino 式大 Registry，Phase 0 仅：

```go
// 工厂函数，按配置构造 ChatModel
type ModelFactory func(cfg ModelConfig) (ChatModel, error)

type ModelConfig struct {
    Provider string // "openai", "ollama", ...
    Model    string
    BaseURL  string
    APIKey   string
}
```

第一个实现：**OpenAI 兼容 HTTP API**（覆盖 OpenAI / 多数国产兼容网关）。

---

## 6. Phase 0 交付清单

| 项 | 说明 |
|----|------|
| `pkg/protocol/schema` | 扩展 Message（ToolCall 可 Phase 1） |
| `pkg/llm/model.go` | ChatModel 接口 |
| `pkg/llm/openai/` | 第一个 Provider |
| `pkg/llm/run.go` | Run 记录与 helper |
| `cmd/llm-demo/` 或测试 | 命令行：读 stdin → 调模型 → 打印 |

**验收**：`go test ./pkg/llm/...` + 手动跑 demo 能对话一轮。

---

## 7. 与参考项目的差异（刻意简化）

| 能力 | Eino | 我们 Phase 0 |
|------|------|--------------|
| Message 多模态 | 有 | 无 |
| Runnable 四模式 | 有 | 仅 Generate + Stream |
| Graph | 有 | 无 |
| Callback 切面 | 有 | 用 Run + 简单 hook 代替 |
| AgenticMessage | 有 | 无 |

复杂能力在 L1/L2 按需加，避免基座过重。
