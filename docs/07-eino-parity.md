# 07 · Eino 能力对照与 LLM 编排

参考 [eino-main](../eino-main/)，LingVoice **专注 LLM 编排层**（L0–L2 + A2A/RAG 扩展），语音（L3+）暂不推进。

## 复刻结论（2026-05-30）

| 维度 | 状态 |
|------|------|
| **L0–L2 编排** | ✅ Chain / Graph / Pregel / HITL / Runnable 四模式 |
| **Pregel 超步 token 流式** | ✅ `streamPregelGraph` + `MergeMessageStreams` |
| **Runnable sync→stream** | ✅ `MessageRunnable` / ChatModel DAG / Pregel 流式 |
| **RAG 向量检索** | ✅ `Embedder` + `VectorRetriever` + OpenAI `/embeddings` + `pkg/knowledge` Qdrant/Milvus |
| **A2A** | ✅ Google A2A v0.3 JSON-RPC 2.0 + REST + SSE；push webhook 投递 |
| **L3 语音** | ❌ 待建设 |

## 关键包

| 能力 | 包 / 文件 |
|------|-----------|
| Pregel 流式 | `compose/graph_stream_pregel.go` |
| Runnable 四模式 | `compose/runnable_adapt.go`, `graph_stream_dag.go` |
| 知识库 | `knowledge/config.go`；`ProviderMemory` 本地向量；`embed`/`retrieve` 策略 + rerank；`Service` + `LoadDocumentsFromDir` |
| 文档清洗 | `pkg/utils` CleanText |
| A2A JSON-RPC | `a2a/jsonrpc.go`, `tasks/*`, `pushNotificationConfig/*`, push retry + dead-letter + **redrive** |
| A2A 认证 | `a2a/auth.go`, `a2a/tls.go` Bearer/API Key + mTLS |

## Demo

| 命令 | 场景 |
|------|------|
| `supervisor-demo` / `plan-execute-demo` | ADK prebuilts |
| `host-compose-demo` | Compose multi-agent |
| `rag-demo` | keyword RAG |
| `rag-vector-demo` | 向量 embedding RAG |
| `knowledge-search-demo` | Bleve 关键词检索 + 策略分片（无向量库） |
| `knowledge-qdrant-demo` | Qdrant + 可选 Bleve hybrid 检索 |
| `knowledge-milvus-demo` | Milvus + 可选 Bleve hybrid 检索 |
| `knowledge-compose-demo` | knowledge Service + compose RAG 图 |
| `knowledge-service-demo` | Service 索引/检索/更新/删除 |
| `knowledge-hybrid-demo` | 内存向量 + Bleve hybrid + func rerank |
| `pregel-demo` | Pregel BSP（Invoke）；ChatModel 图可用 StreamMessages 流式 |

完整索引：[cmd/README.md](../cmd/README.md)

## 测试

```bash
go test ./pkg/llm/... -cover
```

## 暂不推进

- L3 语音 ASR/TTS
- 生产级向量库见 `knowledge-qdrant-demo`；`pkg/knowledge` 核心仅接受显式 Config，不读环境变量

## 相关文档

- [01-layer-stack.md](./01-layer-stack.md)
- [04-protocol-layer.md](./04-protocol-layer.md)
