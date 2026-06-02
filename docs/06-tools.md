# 06 · Tool 与 ToolLoop（Eino 风格）

L1 引入可执行 Tool 与最小 ReAct 循环，参考 Eino `components/tool` + `compose.ToolNode` + `flow/agent/react`。

## 包结构

```
pkg/llm/
├── tool/           # BaseTool、InvokableTool、FuncTool
└── compose/
    ├── tool_node.go   # 执行 assistant ToolCalls → tool messages
    └── tool_loop.go   # model ↔ tools 循环
```

## 定义 Tool

```go
add := tool.NewFuncTool(&schema.ToolInfo{
    Name: "add",
    Desc: "add two numbers",
    Params: &schema.ToolParams{ ... },
}, func(ctx context.Context, argsJSON string) (string, error) {
    // parse argsJSON, return string result for model
})

// 或 typed helper
add := tool.JSONFuncTool(&schema.ToolInfo{Name: "add", ...},
    func(ctx context.Context, in AddInput) (AddOutput, error) { ... })
```

## ToolNode

输入：带 `ToolCalls` 的 assistant `Message`  
输出：按调用顺序排列的 `tool` role messages

```go
node, _ := compose.NewToolNode(ctx, &compose.ToolNodeConfig{
    Tools: []tool.InvokableTool{add},
})
toolMsgs, err := node.Invoke(ctx, assistantMsg)
```

## ToolLoop（ReAct）

```go
loop, _ := compose.NewToolLoop(ctx, compose.ToolLoopConfig{
    Model: openaiModel,          // ToolCallingChatModel
    Tools: []tool.InvokableTool{add},
    MaxRounds: 8,
})
final, historyDelta, err := loop.Run(ctx, messages)
```

也可作为 Chain step：

```go
chain := compose.NewChain("agent",
    compose.ToolLoopStep{Loop: loop},
)
```

## 与 L0 的关系

- `schema.ToolInfo` / `ToolCall` 仍在 `pkg/protocol/schema`
- Provider 通过 `ToolCallingChatModel.WithTools` 绑定 schema
- `pkg/llm/internal/tools` 负责 OpenAI/Anthropic API 转换

## 流式 Token 统计

OpenAI 兼容流式请求自动带 `stream_options.include_usage=true`，usage chunk 经 `ConcatMessages` 合并后进入 metrics。

若 provider 未返回 usage，metrics handler 会按文本长度启发式估算（fallback）。

## 下一步

- 最小 Graph（chat ↔ tools 分支，Eino `compose.Graph`）
- Tool 独立 metrics（`ComponentTool`）
- `cmd/llm-demo -tools` 在线示例

详见 [07-eino-parity.md](./07-eino-parity.md)。
