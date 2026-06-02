# OpenAI + 分层记忆系统集成指南

**文档日期:** May 31, 2026  
**框架版本:** 3.0.0  
**状态:** 完整指南

---

## 🎯 概述

`cmd/llm-memory-integration-demo` 已改为使用真实的 OpenAI API，与分层记忆系统完全集成。

---

## 🔑 配置 OpenAI API

### 1. 获取 API 密钥

访问 [OpenAI Platform](https://platform.openai.com/api-keys) 获取你的 API 密钥。

### 2. 设置环境变量

```bash
export OPENAI_API_KEY="sk-..."
```

### 3. 验证配置

```bash
echo $OPENAI_API_KEY
```

---

## 🚀 运行演示

### 使用真实 OpenAI API

```bash
cd /Users/cetide/Desktop/LingVoice
export OPENAI_API_KEY="sk-..."
go run ./cmd/llm-memory-integration-demo/main.go
```

### 不设置 API 密钥（使用 Mock LLM）

```bash
cd /Users/cetide/Desktop/LingVoice
go run ./cmd/llm-memory-integration-demo/main.go
```

---

## 📋 演示包含的内容

### Example 1: 真实 LLM 调用 + 分层记忆

```
📞 Call 1: What is Go programming language?
   Response: [真实 OpenAI 响应]
   ✓ Stored in memory (Importance: 0.95)

📞 Call 2: How does Go handle concurrency?
   Response: [真实 OpenAI 响应]
   ✓ Stored in memory (Importance: 0.92)

...

📊 Memory Distribution After 5 LLM Calls:
  Working Memory: 5 entries
  Short-term Memory: 5 entries
  Long-term Memory: 0 entries found
```

### Example 2: 知识积累

通过多次 LLM 调用积累知识：
- Go basics (0.95)
- Go concurrency (0.92)
- Go error handling (0.90)
- Go testing (0.88)
- Go performance (0.85)

### Example 3: 上下文感知响应

使用记忆中的上下文生成更准确的响应：
- 初始化上下文 (3 条记录)
- 后续查询时回忆相关上下文
- 生成更准确的响应

### Example 4: RAG + 记忆

RAG 系统与分层记忆的集成：
- 检索相关文档
- 生成答案
- 存储到分层记忆

### Example 5: Agent + 记忆

Agent 系统与分层记忆的集成：
- Agent 做出决策
- 使用工具执行
- 存储决策到记忆

---

## 🔧 实现细节

### OpenAI API 调用

```go
func callOpenAIAPI(ctx context.Context, apiKey string, prompt string) (string, error) {
	requestBody := map[string]interface{}{
		"model":       "gpt-4",
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"temperature": 0.7,
		"max_tokens":  200,
	}

	body, _ := json.Marshal(requestBody)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.openai.com/v1/chat/completions",
		strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call OpenAI API: %w", err)
	}
	defer resp.Body.Close()

	// 解析响应...
	return content, nil
}
```

### 分层记忆存储

```go
entry := llm.MemoryEntry{
	ID:         fmt.Sprintf("llm_call_%d", i+1),
	Content:    fmt.Sprintf("Q: %s\nA: %s", q.question, response),
	Importance: q.importance,
	Metadata: map[string]interface{}{
		"call_index": i + 1,
		"timestamp":  time.Now(),
		"model":      "gpt-4",
		"tokens":     len(response) / 4,
	},
}

hms.AddMemory(ctx, entry)
```

---

## 📊 完整的流程

```
1. 检查 OPENAI_API_KEY
   ↓
2. 如果设置，使用真实 OpenAI API
   如果未设置，使用 Mock LLM
   ↓
3. 创建分层记忆系统
   ↓
4. 进行 LLM 调用
   ↓
5. 评估重要性
   ↓
6. 自动分层存储
   ├─ Importance > 0.9 → Long-term
   ├─ Importance > 0.7 → Short-term
   ├─ Importance > 0.5 → Working
   └─ All → Sensory
   ↓
7. 后续查询时智能回忆
   ├─ 从工作记忆检索
   ├─ 从短期记忆检索
   └─ 从长期记忆检索
   ↓
8. 使用上下文生成更好的响应
```

---

## 💰 成本估算

### OpenAI API 定价（截至 2026 年 5 月）

**GPT-4:**
- 输入: $0.03 / 1K tokens
- 输出: $0.06 / 1K tokens

**GPT-3.5-turbo:**
- 输入: $0.0005 / 1K tokens
- 输出: $0.0015 / 1K tokens

### 示例成本

5 次 LLM 调用，每次 200 tokens：
- 总 tokens: 1,000
- 成本 (GPT-4): ~$0.03
- 成本 (GPT-3.5): ~$0.001

---

## ⚠️ 注意事项

### 1. API 密钥安全

**永远不要：**
- 在代码中硬编码 API 密钥
- 提交 API 密钥到 Git
- 在日志中打印 API 密钥

**应该：**
- 使用环境变量
- 使用密钥管理服务
- 定期轮换密钥

### 2. 速率限制

OpenAI API 有速率限制：
- 免费试用: 3 RPM (requests per minute)
- 付费账户: 根据计划而定

### 3. 超时处理

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
response, err := callOpenAIAPI(ctx, apiKey, prompt)
```

### 4. 错误处理

```go
if err != nil {
	fmt.Printf("   ⚠️  Error: %v\n", err)
	continue
}
```

---

## 🔄 切换模型

### 使用 GPT-3.5-turbo（更便宜）

```go
requestBody := map[string]interface{}{
	"model":       "gpt-3.5-turbo",  // 改为 gpt-3.5-turbo
	"messages":    []map[string]string{{"role": "user", "content": prompt}},
	"temperature": 0.7,
	"max_tokens":  200,
}
```

### 使用其他模型

```go
// gpt-4-turbo
"model": "gpt-4-turbo-preview"

// gpt-4o (最新)
"model": "gpt-4o"

// gpt-4o-mini (最快)
"model": "gpt-4o-mini"
```

---

## 📚 参考资源

- **OpenAI API 文档:** https://platform.openai.com/docs
- **API 参考:** https://platform.openai.com/docs/api-reference
- **模型列表:** https://platform.openai.com/docs/models
- **定价:** https://openai.com/pricing

---

## ✅ 验证清单

- [ ] 获取 OpenAI API 密钥
- [ ] 设置 OPENAI_API_KEY 环境变量
- [ ] 运行演示应用
- [ ] 验证 LLM 调用成功
- [ ] 检查分层记忆存储
- [ ] 测试智能回忆
- [ ] 监控 API 使用成本
- [ ] 实现错误处理和重试

---

## 🚀 下一步

1. **实现流式输出** - 支持实时流式响应
2. **添加重试逻辑** - 处理 API 超时和错误
3. **实现缓存** - 减少 API 调用
4. **添加成本跟踪** - 监控 API 使用成本
5. **性能优化** - 并发处理多个请求
6. **集成更多提供商** - Anthropic, Google, Cohere 等

---

**文档生成时间:** May 31, 2026 00:05 UTC+08:00  
**框架版本:** 3.0.0  
**状态:** 完整指南
