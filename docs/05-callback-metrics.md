# 05 · Callback 与 Metrics（Eino 风格）

参考 Eino `callbacks` + `components/model/callback_extra`，LingVoice 在 L0 之上增加了可观测层。

## 包结构

```
pkg/llm/
├── callback/           # Handler、RunInfo、InitRun、HandlerBuilder
│   └── model/        # ChatModel 专用 CallbackInput/Output
├── metrics/          # RunRecord、MemoryStore（异步写入）、Snapshot
└── instrument/       # Wrap(ChatModel) 自动触发 callback + metrics
```

## 异步指标

`metrics.MemoryStore` 后台 goroutine 消费 `Enqueue`，**不阻塞** Generate/Stream 热路径。

调用方可：

```go
store := metrics.NewMemoryStore()
defer store.Close()

ctx, slot := metrics.WithRunSlot(ctx)
chat := instrument.Wrap(openaiModel, metrics.NewHandler(store))

out, err := chat.Generate(ctx, msgs)
// slot.ID 即本次 run id
rec, ok := store.Get(slot.ID)   // 稍等异步落盘
snap := store.Snapshot()        // 聚合：总次数、token、平均耗时/TTFT/tokens/s、按错误类型计数
list := store.List(10)          // 最近 10 条
```

## RunRecord 字段

| 字段 | 说明 |
|------|------|
| `error_type` | `timeout` / `rate_limit` / `permission` / `model_unavailable` / `content_filter` / `network` / `unknown` |
| `error_code` | 稳定错误码（HTTP 或 provider JSON `error.code`） |
| `ttft_ms` | 首 token 时间（流式=首个 chunk；非流式=上游返回首包，约等于 `upstream_latency_ms`） |
| `upstream_latency_ms` | 上游 HTTP 往返（非流式整段；流式为 Stream 返回前） |
| `tokens_per_second` | 非流式：`total_tokens / duration`；流式：`completion_tokens / (duration - ttft)` |
| `duration_ms` | 端到端耗时 |

`ClassifyError` 会从 `context` 超时、`httputil.HTTPError`、网络错误及 message 启发式归类。

## Callback 用法（类似 Eino）

```go
h := callback.NewHandlerBuilder().
    OnStartFn(func(ctx context.Context, info *callback.RunInfo, in callback.CallbackInput) context.Context {
        mi := modelcb.ConvCallbackInput(in)
        // ...
        return ctx
    }).
    OnEndFn(func(ctx context.Context, info *callback.RunInfo, out callback.CallbackOutput) context.Context {
        mo := modelcb.ConvCallbackOutput(out)
        // ...
        return ctx
    }).
    Build()

chat := instrument.Wrap(inner, h, metrics.NewHandler(store))
```

全局 handler：

```go
callback.AppendGlobalHandlers(metrics.NewHandler(metrics.Default))
```

## llm-demo

默认 `-metrics=true`，会在 stderr 打印单次 `RunRecord` JSON 与 `Snapshot`：

```bash
DASHSCOPE_API_KEY=... go run ./cmd/llm-demo \
  -provider openai -model qwen-plus \
  -base-url https://dashscope.aliyuncs.com/compatible-mode/v1 \
  -prompt "用一句话介绍 LLM 编排"
```

## 下一步（继续复刻 Eino）

| Eino | LingVoice 计划 |
|------|----------------|
| `compose.Chain` | `pkg/llm/compose/chain.go` |
| `compose.Graph` | Phase 2 |
| `compose.ToolNode` | Phase 2 + ToolLoop |
| `callbacks` stream copy | 已简化：instrument 内部 concat 后 OnEndWithStreamOutput |
