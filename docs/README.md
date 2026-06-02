# LingVoice 编排文档

本目录记录 **自研 AI 语音编排系统** 的设计与演进。参考 LingEchoX、Eino、LiveKit Agents、LiveKit SIP 等项目的思路，**不直接集成其代码**，从零构建属于我们自己的编排能力。

## 建设原则

1. **自底向上**：先把 LLM 编排跑通，再叠 Tool、Graph、语音、媒体。
2. **接口先行**：每一层只依赖下层抽象，不跨层 import 实现细节。
3. **可替换**：Model / Tool / Runtime 均可插拔，不写死供应商。
4. **可观测**：每次 Run 有 trace，便于调试与后续语音场景对齐。

## 文档索引

| 文档 | 内容 | 状态 |
|------|------|------|
| [00-vision.md](./00-vision.md) | 目标、边界、与参考项目的关系 | 草案 |
| [01-layer-stack.md](./01-layer-stack.md) | 分层栈：当前做到哪一层 | 草案 |
| [02-llm-primitives.md](./02-llm-primitives.md) | **底层原语**：Message、Model、Run、Context | 草案 · 讨论重点 |
| [03-minimal-orchestration.md](./03-minimal-orchestration.md) | **最小编排**：Chain、一次调用、Tool 循环 | 草案 |
| [05-callback-metrics.md](./05-callback-metrics.md) | Callback + 异步 Metrics（Eino 风格） | 当前 |
| [06-tools.md](./06-tools.md) | Tool、ToolNode、ToolLoop（Eino 风格） | 当前 |
| [07-eino-parity.md](./07-eino-parity.md) | Eino 能力对照与演进路线 | 当前 |
| [discussion/01-open-questions.md](./discussion/01-open-questions.md) | 待商讨的设计决策 | 开放 |

## 当前阶段

**Phase 0 — LLM 编排基座**

- 定义原语与接口（文档 + `pkg/` 骨架）
- 实现一个 ChatModel 适配（如 OpenAI 兼容 API）
- 实现最小 Run：输入 messages → 输出 assistant message
- 暂不涉及：SIP、TTS、ASR、Campaign、UI

## 代码对应

```
pkg/protocol/
├── schema/            # Message、Tool、Stream、ConcatMessages
└── llm/               # ChatModel 接口、Options、FuncModel

# 待建
pkg/orchestrate/       # Phase 1：Chain / ToolLoop
cmd/                   # 见 cmd/README.md（6 个 demo）
```
