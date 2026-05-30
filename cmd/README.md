# cmd 示例索引

所有需要调用 LLM 的 demo 均从环境变量读取 API Key（**不要**写死在代码里）。

| 命令 | 说明 | 需要 API Key |
|------|------|--------------|
| [llm-demo](./llm-demo/) | 基础 Generate / Stream + metrics | ✅ |
| [chain-demo](./chain-demo/) | Chain：System + ChatModel | ✅ |
| [chain-builder-demo](./chain-builder-demo/) | **ChainBuilder → CompileGraph**（本地） | ❌ |
| [workflow-demo](./workflow-demo/) | **Workflow AddBranch** + FieldMapping（本地） | ❌ |
| [pregel-demo](./pregel-demo/) | **Pregel fan-out/join** 超步图（本地） | ❌ |
| [pregel-checkpoint-demo](./pregel-checkpoint-demo/) | **Pregel interrupt + checkpoint 恢复**（本地） | ❌ |
| [multi-branch-demo](./multi-branch-demo/) | **ChainMultiBranch** fan-out（本地） | ❌ |
| [workflow-react-demo](./workflow-react-demo/) | **Workflow AddReActNode**（本地 mock） | ❌ |
| [generic-chain-demo](./generic-chain-demo/) | **GenericChain[I,O]** 类型化链（本地） | ❌ |
| [runner-demo](./runner-demo/) | **Runner StreamFrames** + checkpoint（本地） | ❌ |
| [react-demo](./react-demo/) | ReAct Agent（Graph 引擎）+ verbose trace + HITL Resume | ✅ |
| [orchestrate-demo](./orchestrate-demo/) | **SessionOrchestrator** 多轮 + template + checkpoint | ✅ |
| [pipeline-demo](./pipeline-demo/) | Template → Branch → ReAct Pipeline（本地，`-stream` frame trace） | ❌ |
| [session-hitl-demo](./session-hitl-demo/) | **SessionOrchestrator** 多轮 + 可审批 Tool HITL（本地） | ❌ |
| [graph-demo](./graph-demo/) | **Compiled Graph** ReAct + 节点 trace + tool metrics | ✅ |
| [parallel-demo](./parallel-demo/) | Chain 并行 Step（本地） | ❌ |
| [checkpoint-demo](./checkpoint-demo/) | Graph Checkpoint + HITL 中断/恢复（本地） | ❌ |
| [host-demo](./host-demo/) | Multi-agent Host 路由（本地） | ❌ |
| [host-compose-demo](./host-compose-demo/) | Compose graph host + ProcessState（本地） | ❌ |
| [supervisor-demo](./supervisor-demo/) | ADK supervisor 路由 worker（本地） | ❌ |
| [plan-execute-demo](./plan-execute-demo/) | ADK plan-execute 逐步执行（本地） | ❌ |
| [rag-demo](./rag-demo/) | keyword RAG + compose 图（本地） | ❌ |
| [rag-vector-demo](./rag-vector-demo/) | 向量 embedding RAG（本地 mock embedder） | ❌ |
| [knowledge-demo](./knowledge-demo/) | 策略分片预览（dry-run） | ❌ |
| [knowledge-search-demo](./knowledge-search-demo/) | Bleve 关键词检索 + 策略分片（无向量库） | ❌ |
| [knowledge-qdrant-demo](./knowledge-qdrant-demo/) | Qdrant + 可选 Bleve hybrid | ✅ embed |
| [knowledge-milvus-demo](./knowledge-milvus-demo/) | Milvus + 可选 Bleve hybrid | ✅ embed |
| [knowledge-compose-demo](./knowledge-compose-demo/) | knowledge Service + compose RAG 图（本地 Bleve） | ❌ |
| [knowledge-service-demo](./knowledge-service-demo/) | Service 索引/检索/更新/删除全生命周期（本地 Bleve） | ❌ |
| [knowledge-hybrid-demo](./knowledge-hybrid-demo/) | 内存向量 + Bleve hybrid + 可选 func rerank（无 API Key） | ❌ |
| [a2a-demo](./a2a-demo/) | A2A Bearer auth + SSE streaming（`-extended` 含 tasks/push） | ❌ |
| [branch-demo](./branch-demo/) | ChainBranch 条件分支（本地） | ❌ |
| [hitl-demo](./hitl-demo/) | ReAct + 可审批 Tool + checkpoint（本地 mock） | ❌ |
| [tool-demo](./tool-demo/) | 本地 ToolNode（InferTool） | ❌ |
| [prompt-demo](./prompt-demo/) | FString 模板渲染 | ❌ |

## 环境变量

```bash
export OPENAI_API_KEY=sk-...
# 或 DashScope 兼容模式
export DASHSCOPE_API_KEY=sk-...
export ANTHROPIC_API_KEY=sk-ant-...
```

## 快速开始

```bash
# 1. 基础对话
go run ./cmd/llm-demo -prompt "用一句话介绍 LLM 编排"

# 2. DashScope 流式
DASHSCOPE_API_KEY=... go run ./cmd/llm-demo \
  -model qwen-plus \
  -base-url https://dashscope.aliyuncs.com/compatible-mode/v1 \
  -stream -prompt "介绍 LLM 编排"

# 3. 本地 Tool（无需 Key）
go run ./cmd/tool-demo

# 4. Prompt 模板（无需 Key）
go run ./cmd/prompt-demo -name LingVoice

# 5. Chain 编排
go run ./cmd/chain-demo -prompt "什么是 Chain？"

# 5b. ChainBuilder → CompileGraph（本地，Eino Append 风格）
go run ./cmd/chain-builder-demo

# 5c. ChainBranch 条件分支（本地）
go run ./cmd/branch-demo

# 5d. Workflow 分支 + 字段映射（本地）
go run ./cmd/workflow-demo

# 5e. Pregel fan-out/join 超步图（本地）
go run ./cmd/pregel-demo

# 5e2. Pregel interrupt + checkpoint 恢复（本地）
go run ./cmd/pregel-checkpoint-demo

# 5e3. ChainMultiBranch fan-out（本地）
go run ./cmd/multi-branch-demo

# 5e4. Workflow + ReAct 节点（本地 mock）
go run ./cmd/workflow-react-demo

# 5e5. GenericChain 类型化链（本地）
go run ./cmd/generic-chain-demo

# 5f. Runner StreamFrames + checkpoint（本地 mock）
go run ./cmd/runner-demo

# 6. ReAct 计算器 Agent（Graph 引擎，推荐 -verbose）
go run ./cmd/react-demo -verbose -force-tools \
  -model qwen-plus \
  -base-url https://dashscope.aliyuncs.com/compatible-mode/v1 \
  -prompt "计算 (17+25)*3，每一步都必须调用工具"

# 6b. 多轮 SessionOrchestrator（template + checkpoint）
go run ./cmd/orchestrate-demo \
  -model qwen-plus \
  -base-url https://dashscope.aliyuncs.com/compatible-mode/v1 \
  -prompt "计算 (3+4)*2，必须用工具" \
  -prompt2 "刚才结果是多少？"

# 6c. Pipeline：Template → Branch → ReAct（本地）
go run ./cmd/pipeline-demo
go run ./cmd/pipeline-demo -stream   # frame-level trace

# 6d. SessionOrchestrator + HITL 多轮（本地 mock）
go run ./cmd/session-hitl-demo

# 7. Graph 模式（带节点 trace + tool metrics）
go run ./cmd/graph-demo \
  -model qwen-plus \
  -base-url https://dashscope.aliyuncs.com/compatible-mode/v1 \
  -prompt "用 add 和 multiply 计算 (8+7)*6"

# 8. 并行 Step（本地，无需 Key）
go run ./cmd/parallel-demo

# 9. Checkpoint + HITL（本地，无需 Key）
go run ./cmd/checkpoint-demo

# 10. Multi-agent Host 路由（本地）
go run ./cmd/host-demo

# 10b. Compose graph host + ProcessState（本地）
go run ./cmd/host-compose-demo

# 10c. ADK supervisor（本地）
go run ./cmd/supervisor-demo

# 10d. ADK plan-execute（本地）
go run ./cmd/plan-execute-demo

# 10e. RAG keyword + compose（本地）
go run ./cmd/rag-demo

# 10e2. RAG 向量 embedding（本地 mock embedder）
go run ./cmd/rag-vector-demo

# 10e3. knowledge 策略分片预览（本地）
go run ./cmd/knowledge-demo

# 10e4. knowledge Bleve 关键词 RAG（本地）
go run ./cmd/knowledge-search-demo -index /tmp/lv-search

# 10e5. knowledge compose 图 + Bleve（本地）
go run ./cmd/knowledge-compose-demo -index /tmp/lv-compose

# 10f. A2A Bearer + SSE streaming（本地）
go run ./cmd/a2a-demo

# 11. ReAct + HITL 可审批 Tool（本地 mock model）
go run ./cmd/hitl-demo
```

## 共享包

`cmd/internal/demo` 提供统一的 `BuildModel`、`WrapMetrics`、打印 helpers，供各 demo 复用。
