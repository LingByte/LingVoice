# 商讨 · Phase 0 开放问题

请在开始写 `pkg/llm` 代码前，对下面问题给出倾向。可以在本文件直接批注，或在对话里逐项确认。

---

## A. Message 与协议包

**A1.** `Message` 最终放在哪里？

- **选项 1**：`pkg/protocol/schema`（跨 LLM / 未来语音 / API 共用）
- **选项 2**：`pkg/llm`（仅编排内部用，对外再映射）

**倾向建议**：选项 1，已有 `message.go` 种子。

**A2.** Phase 0 是否加入 `ToolCall` / `ToolCalls` 字段？

- **是**：L1 ToolLoop 不用改 Message
- **否**：Phase 0 极简，Tool 字段 Phase 1 再加

**倾向建议**：Phase 0 只加字段定义，L1 再使用。

---

## B. ChatModel 接口

**B1.** 流式 API 形态？

- **选项 1**：`Stream` 返回 `StreamReader[Message]`，chunk 为 delta
- **选项 2**：Phase 0 只做 `Generate`，Stream 后补

**B2.** 是否需要 Eino 式 Runnable（Invoke / Stream / Collect / Transform）？

- **倾向建议**：Phase 0 **不需要**，YAGNI。

---

## C. 第一个 Provider

**C1.** 第一个接入谁？

- OpenAI 官方
- OpenAI 兼容网关（One API / 国内聚合）
- Ollama 本地

**倾向建议**：OpenAI 兼容 HTTP（一个实现覆盖多数环境）。

**C2.** 配置来源？

- 环境变量
- 配置文件
- 两者都要

---

## D. Run 与观测

**D1.** Run 记录存哪里？

- 仅内存 / 日志
- 接口 `RunRecorder`，默认 noop，可接文件或 DB

**倾向建议**：接口 + 默认 log，不引入 DB。

**D2.** 是否需要 OpenTelemetry span 在 Phase 0？

- **倾向建议**：否，Phase 1 用 hook 接入。

---

## E. 模块与目录

**E1.** Phase 0 目录是否同意？

```
pkg/
├── protocol/schema/   # Message, Role
├── llm/
│   ├── model.go       # ChatModel, CallOption
│   ├── run.go         # Run, TokenUsage
│   ├── stream.go      # StreamReader
│   └── openai/        # 第一个 provider
```

**E2.** 是否需要 `cmd/llm-demo` 做手工验证？

- **倾向建议**：要，比纯单测更直观。

---

## F. 语言与文档

**F1.** 代码注释与导出文档：中文还是英文？

**F2.** 对外品牌名：LingVoice 编排内核是否有单独产品名（如 LingFlow）？

---

## 下一步（对齐后）

1. 确认 A–E 的选择
2. 实现 `pkg/protocol/schema` 扩展 + `pkg/llm` 骨架
3. OpenAI provider + demo + 测试
4. 再开 L1（Chain / ToolLoop）文档细化

---

## 你的反馈（请填写或口述）

| 编号 | 你的选择 / 意见 |
|------|----------------|
| A1 | |
| A2 | |
| B1 | |
| C1 | |
| D1 | |
| E1 | |
| 其他 | |
