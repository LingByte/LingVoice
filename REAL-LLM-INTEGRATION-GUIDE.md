# 真实 LLM + 分层记忆系统集成指南

**文档日期:** May 30, 2026  
**框架版本:** 3.0.0  
**状态:** 完整指南

---

## 🎯 概述

本指南展示如何将真实的 LLM API（OpenAI、Anthropic、本地 LLM）与分层记忆系统集成，实现完整的 LLM + 记忆系统。

---

## 📋 支持的 LLM 提供商

### 1. OpenAI (GPT-4, GPT-3.5)
- **API 端点:** `https://api.openai.com/v1/chat/completions`
- **认证:** Bearer Token (API Key)
- **模型:** gpt-4, gpt-3.5-turbo
- **功能:** 流式输出、函数调用

### 2. Anthropic (Claude)
- **API 端点:** `https://api.anthropic.com/v1/messages`
- **认证:** x-api-key Header
- **模型:** claude-3-opus, claude-3-sonnet, claude-3-haiku
- **功能:** 流式输出、长上下文

### 3. 本地 LLM (Ollama)
- **API 端点:** `http://localhost:11434/api/generate`
- **认证:** 无
- **模型:** llama2, mistral, neural-chat 等
- **功能:** 完全离线、隐私保护

---

## 🔧 实现步骤

### Step 1: 实现 OpenAI 模型

```go
package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenAIModel implements LLMModel for OpenAI API
type OpenAIModel struct {
	apiKey      string
	model       string
	temperature float64
	maxTokens   int
	client      *http.Client
}

// NewOpenAIModel creates a new OpenAI model
func NewOpenAIModel(apiKey string, model string, temperature float64, maxTokens int) *OpenAIModel {
	return &OpenAIModel{
		apiKey:      apiKey,
		model:       model,
		temperature: temperature,
		maxTokens:   maxTokens,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Generate generates text using OpenAI API
func (om *OpenAIModel) Generate(ctx context.Context, prompt string) (string, error) {
	if om.apiKey == "" {
		return "", fmt.Errorf("OpenAI API key not set")
	}

	// Create request body
	requestBody := fmt.Sprintf(`{
		"model": "%s",
		"messages": [
			{"role": "user", "content": "%s"}
		],
		"temperature": %.2f,
		"max_tokens": %d
	}`, om.model, escapeJSON(prompt), om.temperature, om.maxTokens)

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", 
		"https://api.openai.com/v1/chat/completions", 
		strings.NewReader(requestBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+om.apiKey)

	// Make request
	resp, err := om.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call OpenAI API: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenAI API error: %s", string(body))
	}

	// Parse and extract content from response
	content := extractContent(string(body))
	return content, nil
}

// GetConfig returns model configuration
func (om *OpenAIModel) GetConfig() ModelConfig {
	return ModelConfig{
		Name:        om.model,
		Provider:    "openai",
		Temperature: om.temperature,
		MaxTokens:   om.maxTokens,
	}
}
```

### Step 2: 实现 Anthropic 模型

```go
// AnthropicModel implements LLMModel for Anthropic API
type AnthropicModel struct {
	apiKey      string
	model       string
	temperature float64
	maxTokens   int
	client      *http.Client
}

// NewAnthropicModel creates a new Anthropic model
func NewAnthropicModel(apiKey string, model string, temperature float64, maxTokens int) *AnthropicModel {
	return &AnthropicModel{
		apiKey:      apiKey,
		model:       model,
		temperature: temperature,
		maxTokens:   maxTokens,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Generate generates text using Anthropic API
func (am *AnthropicModel) Generate(ctx context.Context, prompt string) (string, error) {
	if am.apiKey == "" {
		return "", fmt.Errorf("Anthropic API key not set")
	}

	// Create request body
	requestBody := fmt.Sprintf(`{
		"model": "%s",
		"max_tokens": %d,
		"temperature": %.2f,
		"messages": [
			{"role": "user", "content": "%s"}
		]
	}`, am.model, am.maxTokens, am.temperature, escapeJSON(prompt))

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.anthropic.com/v1/messages",
		strings.NewReader(requestBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", am.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	// Make request
	resp, err := am.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call Anthropic API: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Anthropic API error: %s", string(body))
	}

	// Parse and extract content from response
	content := extractContent(string(body))
	return content, nil
}

// GetConfig returns model configuration
func (am *AnthropicModel) GetConfig() ModelConfig {
	return ModelConfig{
		Name:        am.model,
		Provider:    "anthropic",
		Temperature: am.temperature,
		MaxTokens:   am.maxTokens,
	}
}
```

### Step 3: 实现本地 LLM 模型

```go
// LocalLLMModel implements LLMModel for local LLM (e.g., Ollama)
type LocalLLMModel struct {
	endpoint    string
	model       string
	temperature float64
	maxTokens   int
	client      *http.Client
}

// NewLocalLLMModel creates a new local LLM model
func NewLocalLLMModel(endpoint string, model string, temperature float64, maxTokens int) *LocalLLMModel {
	return &LocalLLMModel{
		endpoint:    endpoint,
		model:       model,
		temperature: temperature,
		maxTokens:   maxTokens,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Generate generates text using local LLM
func (llm *LocalLLMModel) Generate(ctx context.Context, prompt string) (string, error) {
	// Create request body
	requestBody := fmt.Sprintf(`{
		"model": "%s",
		"prompt": "%s",
		"temperature": %.2f,
		"num_predict": %d,
		"stream": false
	}`, llm.model, escapeJSON(prompt), llm.temperature, llm.maxTokens)

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST",
		llm.endpoint+"/api/generate",
		strings.NewReader(requestBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")

	// Make request
	resp, err := llm.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call local LLM: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("local LLM error: %s", string(body))
	}

	// Parse and extract content from response
	content := extractContent(string(body))
	return content, nil
}

// GetConfig returns model configuration
func (llm *LocalLLMModel) GetConfig() ModelConfig {
	return ModelConfig{
		Name:        llm.model,
		Provider:    "local",
		Temperature: llm.temperature,
		MaxTokens:   llm.maxTokens,
	}
}
```

---

## 💻 使用示例

### 使用 OpenAI

```go
// 创建 OpenAI 模型
llmModel := NewOpenAIModel(
	os.Getenv("OPENAI_API_KEY"),
	"gpt-4",
	0.7,
	200,
)

// 创建分层记忆系统
store, _ := llm.NewFileSystemStore("/tmp/memory")
hms := llm.NewHierarchicalMemorySystem(store)

// 进行 LLM 调用
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
response, err := llmModel.Generate(ctx, "What is machine learning?")
cancel()

if err != nil {
	log.Fatal(err)
}

// 存储到分层记忆
entry := llm.MemoryEntry{
	ID:         "llm_call_1",
	Content:    fmt.Sprintf("Q: What is machine learning?\nA: %s", response),
	Importance: 0.95,
}

hms.AddMemory(context.Background(), entry)
```

### 使用 Anthropic

```go
// 创建 Anthropic 模型
llmModel := NewAnthropicModel(
	os.Getenv("ANTHROPIC_API_KEY"),
	"claude-3-opus",
	0.7,
	200,
)

// 其余代码相同...
```

### 使用本地 LLM

```go
// 创建本地 LLM 模型
llmModel := NewLocalLLMModel(
	"http://localhost:11434",
	"llama2",
	0.7,
	200,
)

// 其余代码相同...
```

---

## 🔑 API 密钥配置

### OpenAI
```bash
export OPENAI_API_KEY="sk-..."
```

### Anthropic
```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

### 本地 LLM (Ollama)
```bash
# 安装 Ollama: https://ollama.ai
# 运行: ollama serve
# 拉取模型: ollama pull llama2
export LOCAL_LLM_ENDPOINT="http://localhost:11434"
```

---

## 📊 完整的集成流程

```
1. 初始化 LLM 模型
   ↓
2. 创建分层记忆系统
   ↓
3. 进行 LLM 调用
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
   ├─ 从工作记忆检索
   ├─ 从短期记忆检索
   └─ 从长期记忆检索
   ↓
7. 使用上下文生成更好的响应
```

---

## ✅ 验证清单

- [ ] 实现 OpenAI 模型
- [ ] 实现 Anthropic 模型
- [ ] 实现本地 LLM 模型
- [ ] 配置 API 密钥
- [ ] 测试 LLM 调用
- [ ] 验证记忆存储
- [ ] 验证智能回忆
- [ ] 测试流式输出
- [ ] 性能优化
- [ ] 错误处理

---

## 🚀 下一步

1. **实现流式输出** - 支持实时流式响应
2. **添加重试逻辑** - 处理 API 超时和错误
3. **实现缓存** - 减少 API 调用
4. **添加成本跟踪** - 监控 API 使用成本
5. **性能优化** - 并发处理多个请求
6. **集成更多提供商** - Google, Cohere, HuggingFace 等

---

## 📚 参考资源

- **OpenAI API:** https://platform.openai.com/docs
- **Anthropic API:** https://docs.anthropic.com
- **Ollama:** https://ollama.ai
- **LingVoice 文档:** /docs

---

**文档生成时间:** May 30, 2026 23:59 UTC+08:00  
**框架版本:** 3.0.0  
**状态:** 完整指南
