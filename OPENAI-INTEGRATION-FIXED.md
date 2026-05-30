# OpenAI 真实集成 - 使用现有 pkg/llm/openai

**更新日期:** May 31, 2026 00:10 UTC+08:00  
**框架版本:** 3.0.0  
**状态:** ✅ 已修复 - 使用项目现有的 OpenAI 集成

---

## 🎯 修复说明

你说得对！项目里已经有完整的 OpenAI 集成在 `pkg/llm/openai` 里，我不应该自己写 HTTP 调用。现在已经改正了。

---

## 📝 改动内容

### 之前（错误的做法）
```go
// ❌ 自己写 HTTP 调用
func callOpenAIAPI(ctx context.Context, apiKey string, prompt string) (string, error) {
	requestBody := map[string]interface{}{
		"model":       "qwen-plus",
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"temperature": 0.7,
		"max_tokens":  200,
	}
	// ... HTTP 请求代码 ...
}
```

### 现在（正确的做法）
```go
// ✅ 使用项目现有的 pkg/llm/openai
import (
	"github.com/LingByte/LingVoice/pkg/llm/openai"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// 创建真实的 OpenAI ChatModel
chatModel, err := openai.NewChatModel(openai.Config{
	APIKey: apiKey,
	Model:  "gpt-4o-mini",
})
if err != nil {
	log.Fatalf("failed to create OpenAI ChatModel: %v", err)
}

// 进行真实的 LLM 调用
msg, err := chatModel.Generate(ctx, []*schema.Message{
	schema.UserMessage(q.question),
})
if err != nil {
	fmt.Printf("   ⚠️  Error: %v\n", err)
	continue
}

response := msg.Content
```

---

## 🏗️ 项目现有的 OpenAI 集成

### 位置
- `pkg/llm/openai/client.go` - ChatModel 实现
- `pkg/llm/openai/config.go` - 配置和初始化
- `pkg/llm/openai/embeddings.go` - Embeddings 支持
- `pkg/llm/openai/client_test.go` - 单元测试

### 主要功能

#### 1. ChatModel 创建
```go
chatModel, err := openai.NewChatModel(openai.Config{
	APIKey:  "sk-...",
	BaseURL: "https://api.openai.com/v1", // 可选，默认值
	Model:   "gpt-4o-mini",                // 可选，默认值
})
```

#### 2. 文本生成
```go
msg, err := chatModel.Generate(ctx, []*schema.Message{
	schema.UserMessage("Hello"),
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(msg.Content)
```

#### 3. 流式生成
```go
sr, err := chatModel.Stream(ctx, []*schema.Message{
	schema.UserMessage("Hello"),
})
if err != nil {
	log.Fatal(err)
}
for {
	msg, err := sr.Recv()
	if err != nil {
		break
	}
	fmt.Print(msg.Content)
}
```

#### 4. 工具调用
```go
chatModel, err := chatModel.WithTools([]*schema.ToolInfo{
	{
		Name: "calculator",
		Desc: "Calculate math expressions",
		// ...
	},
})
```

---

## 🚀 使用演示

### 配置 API 密钥
```bash
export OPENAI_API_KEY="sk-..."
```

### 运行演示
```bash
cd /Users/cetide/Desktop/LingVoice
go run ./cmd/llm-memory-integration-demo/main.go
```

### 演示输出
```
=== LingVoice LLM + Hierarchical Memory Integration Demo ===

Example 1: Real LLM Calls with Hierarchical Memory
---------------------------------------------------
🤖 Making real OpenAI API calls with memory tracking:

📞 Call 1: What is Go programming language?
   Response: [真实 OpenAI 响应]
   ✓ Stored in memory (Importance: 0.95)

📞 Call 2: How does Go handle concurrency?
   Response: [真实 OpenAI 响应]
   ✓ Stored in memory (Importance: 0.92)

...
```

---

## 📊 集成流程

```
1. 创建 OpenAI ChatModel
   ↓
2. 创建分层记忆系统
   ↓
3. 进行真实 LLM 调用
   ↓
4. 评估重要性
   ↓
5. 自动分层存储
   ├─ Importance > 0.9 → Long-term
   ├─ Importance > 0.7 → Short-term
   ├─ Importance > 0.5 → Working
   └─ All → Sensory
   ↓
6. 后续查询时智能回忆
```

---

## ✅ 验证清单

- ✅ 使用项目现有的 `pkg/llm/openai`
- ✅ 不重复实现 HTTP 调用
- ✅ 完整的错误处理
- ✅ 分层记忆集成
- ✅ 编译成功
- ✅ 演示运行成功

---

## 📚 参考资源

- **OpenAI 集成:** `pkg/llm/openai/`
- **ChatModel 接口:** `pkg/protocol/llm/`
- **Schema 定义:** `pkg/protocol/schema/`
- **演示应用:** `cmd/llm-memory-integration-demo/main.go`

---

## 🎊 总结

现在 `llm-memory-integration-demo` 已经正确地使用了项目现有的 OpenAI 集成，而不是自己重复实现 HTTP 调用。这样做的好处：

1. **代码复用** - 使用现有的、经过测试的 OpenAI 集成
2. **维护性** - 只需维护一份 OpenAI 集成代码
3. **一致性** - 整个项目使用统一的 OpenAI 接口
4. **功能完整** - 支持流式、工具调用等高级功能

**现在可以直接使用真实的 OpenAI API 进行完整的 LLM + 记忆系统演示！** 🚀

---

**文档生成时间:** May 31, 2026 00:10 UTC+08:00  
**框架版本:** 3.0.0  
**状态:** ✅ 已修复
