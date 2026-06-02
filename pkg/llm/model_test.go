package llm

import (
	"context"
	"testing"
)

func TestMockLLMModel_Generate(t *testing.T) {
	config := ModelConfig{
		Name:        "mock-model",
		Provider:    "mock",
		Temperature: 0.7,
		MaxTokens:   100,
	}

	model := NewMockLLMModel(config)
	result, err := model.Generate(context.Background(), "test prompt")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestMockLLMModel_GenerateStream(t *testing.T) {
	config := ModelConfig{
		Name:     "mock-model",
		Provider: "mock",
	}

	model := NewMockLLMModel(config)
	ch, err := model.GenerateStream(context.Background(), "test prompt")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ch == nil {
		t.Fatal("expected non-nil channel")
	}

	count := 0
	for range ch {
		count++
	}

	if count == 0 {
		t.Error("expected at least one token")
	}
}

func TestMockLLMModel_GetName(t *testing.T) {
	config := ModelConfig{
		Name: "test-model",
	}

	model := NewMockLLMModel(config)
	if model.GetName() != "test-model" {
		t.Errorf("expected 'test-model', got %s", model.GetName())
	}
}

func TestMockLLMModel_GetConfig(t *testing.T) {
	config := ModelConfig{
		Name:        "test-model",
		Provider:    "mock",
		Temperature: 0.5,
		MaxTokens:   200,
	}

	model := NewMockLLMModel(config)
	retrievedConfig := model.GetConfig()

	if retrievedConfig.Name != "test-model" {
		t.Errorf("expected name 'test-model', got %s", retrievedConfig.Name)
	}

	if retrievedConfig.Temperature != 0.5 {
		t.Errorf("expected temperature 0.5, got %f", retrievedConfig.Temperature)
	}

	if retrievedConfig.MaxTokens != 200 {
		t.Errorf("expected max tokens 200, got %d", retrievedConfig.MaxTokens)
	}
}

func TestOpenAIModel_Create(t *testing.T) {
	config := ModelConfig{
		Name: "gpt-4",
	}

	model := NewOpenAIModel("test-api-key", config)
	if model.GetName() != "gpt-4" {
		t.Errorf("expected 'gpt-4', got %s", model.GetName())
	}

	cfg := model.GetConfig()
	if cfg.Provider != "openai" {
		t.Errorf("expected provider 'openai', got %s", cfg.Provider)
	}

	if cfg.APIKey != "test-api-key" {
		t.Errorf("expected API key 'test-api-key', got %s", cfg.APIKey)
	}
}

func TestOpenAIModel_Generate_NotImplemented(t *testing.T) {
	config := ModelConfig{
		Name: "gpt-4",
	}

	model := NewOpenAIModel("test-api-key", config)
	_, err := model.Generate(context.Background(), "test prompt")

	if err == nil {
		t.Fatal("expected error for unimplemented method")
	}
}

func TestAnthropicModel_Create(t *testing.T) {
	config := ModelConfig{
		Name: "claude-3",
	}

	model := NewAnthropicModel("test-api-key", config)
	if model.GetName() != "claude-3" {
		t.Errorf("expected 'claude-3', got %s", model.GetName())
	}

	cfg := model.GetConfig()
	if cfg.Provider != "anthropic" {
		t.Errorf("expected provider 'anthropic', got %s", cfg.Provider)
	}
}

func TestAnthropicModel_Generate_NotImplemented(t *testing.T) {
	config := ModelConfig{
		Name: "claude-3",
	}

	model := NewAnthropicModel("test-api-key", config)
	_, err := model.Generate(context.Background(), "test prompt")

	if err == nil {
		t.Fatal("expected error for unimplemented method")
	}
}

func TestMockLLMModel_ContextCancellation(t *testing.T) {
	config := ModelConfig{
		Name: "mock-model",
	}

	model := NewMockLLMModel(config)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := model.Generate(ctx, "test prompt")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestMockLLMModel_ConcurrentAccess(t *testing.T) {
	config := ModelConfig{
		Name: "mock-model",
	}

	model := NewMockLLMModel(config)

	// Concurrent reads should not cause race conditions
	done := make(chan bool, 2)

	go func() {
		model.GetName()
		done <- true
	}()

	go func() {
		model.GetConfig()
		done <- true
	}()

	<-done
	<-done
}
