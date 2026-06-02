# 03 · 最小编排（L1 · 设计草案）

> L0 稳定后再实现。本文先定方向，便于商讨时知道「基座之上第一步是什么」。

## 1. 为什么需要 L1

单次 `ChatModel.Generate` 不够表达：

- 先套模板再调模型
- 模型要调 Tool，拿到结果再继续
- 多步固定流程（仍无分支图）

L1 是 **无环、顺序为主** 的编排，比 Graph 简单，比裸 Model 够用。

## 2. Chain

```go
type Step interface {
    Run(ctx context.Context, state *State) error
}

type Chain struct {
    steps []Step
}

func (c *Chain) Execute(ctx context.Context, state *State) error
```

### State（L1 共享状态）

```go
type State struct {
    Messages []Message           // 累积对话
    Vars     map[string]any      // 模板变量、Tool 结果
    Runs     []llm.Run           // 每次 Model 调用的记录
}
```

典型 Chain：

```
PromptTemplateStep → ChatModelStep → (optional) ToolLoopStep
```

## 3. PromptTemplateStep

输入：`State.Vars` + 模板字符串  
输出：append 一条 system 或 user `Message` 到 `State.Messages`

Phase 1 模板语法：**先只做 Go `text/template`**，不做 Jinja。

## 4. ChatModelStep

从 `State.Messages` 调用 `ChatModel.Generate`，把 assistant 回复 append 回去，并记录 `Run`。

## 5. ToolLoop（L1 核心循环）

最小 ReAct：

```
loop:
  out = model.Generate(messages)
  if out 无 ToolCalls:
      return out
  for each tool call:
      result = tools.Invoke(name, args)
      append tool message
  goto loop
```

**上限**：`MaxToolRound`（默认 8），防止死循环。

### Tool 接口（L1 引入，L0 无）

```go
type Tool interface {
    Name() string
    Description() string
    // Schema 返回 JSON Schema 或简化参数描述 — 待商讨
    Invoke(ctx context.Context, args json.RawMessage) (string, error)
}
```

## 6. L1 仍不做

- 条件分支（if/else 边）
- 并行 Step
- Checkpoint / HITL
- 持久化 Session 存储

这些属于 L2。

## 7. 与语音的关系（预告）

L3 会把「用户 ASR 文本」写入 `State.Messages`（user），把 assistant `Content` 交给 TTS。  
**L1 的 State 与 Chain 不感知 PCM**，只感知文本 Message。

这样 LLM 编排基座可以先独立开发、独立测试。
