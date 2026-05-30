package llm

import (
	"context"
	"testing"
)

func TestBufferMemory_AddMessage(t *testing.T) {
	memory := NewBufferMemory(10)
	msg := Message{
		Role:    "user",
		Content: "test message",
	}

	err := memory.AddMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(memory.messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(memory.messages))
	}
}

func TestBufferMemory_AddMessage_TimestampSet(t *testing.T) {
	memory := NewBufferMemory(10)
	msg := Message{
		Role:    "user",
		Content: "test message",
	}

	memory.AddMessage(context.Background(), msg)

	if memory.messages[0].Timestamp.IsZero() {
		t.Error("expected timestamp to be set")
	}
}

func TestBufferMemory_GetMessages(t *testing.T) {
	memory := NewBufferMemory(10)

	for i := 0; i < 3; i++ {
		memory.AddMessage(context.Background(), Message{
			Role:    "user",
			Content: "message " + string(rune(i)),
		})
	}

	messages, err := memory.GetMessages(context.Background(), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(messages))
	}
}

func TestBufferMemory_GetMessages_AllMessages(t *testing.T) {
	memory := NewBufferMemory(10)

	for i := 0; i < 3; i++ {
		memory.AddMessage(context.Background(), Message{
			Role:    "user",
			Content: "message " + string(rune(i)),
		})
	}

	messages, err := memory.GetMessages(context.Background(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(messages))
	}
}

func TestBufferMemory_Clear(t *testing.T) {
	memory := NewBufferMemory(10)

	for i := 0; i < 3; i++ {
		memory.AddMessage(context.Background(), Message{
			Role:    "user",
			Content: "message " + string(rune(i)),
		})
	}

	err := memory.Clear(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(memory.messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(memory.messages))
	}
}

func TestBufferMemory_GetContext(t *testing.T) {
	memory := NewBufferMemory(10)
	memory.AddMessage(context.Background(), Message{
		Role:    "user",
		Content: "hello",
	})
	memory.AddMessage(context.Background(), Message{
		Role:    "assistant",
		Content: "hi",
	})

	context := memory.GetContext()
	if context == "" {
		t.Error("expected non-empty context")
	}

	if !contains(context, "user") {
		t.Error("expected 'user' in context")
	}

	if !contains(context, "hello") {
		t.Error("expected 'hello' in context")
	}
}

func TestBufferMemory_MaxSize(t *testing.T) {
	memory := NewBufferMemory(3)

	for i := 0; i < 5; i++ {
		memory.AddMessage(context.Background(), Message{
			Role:    "user",
			Content: "message " + string(rune(i)),
		})
	}

	if len(memory.messages) > 3 {
		t.Errorf("expected at most 3 messages, got %d", len(memory.messages))
	}
}

func TestSummaryMemory_AddMessage(t *testing.T) {
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	memory := NewSummaryMemory(10, llm)
	msg := Message{
		Role:    "user",
		Content: "test message",
	}

	err := memory.AddMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(memory.messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(memory.messages))
	}
}

func TestSummaryMemory_GetMessages(t *testing.T) {
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	memory := NewSummaryMemory(10, llm)

	for i := 0; i < 3; i++ {
		memory.AddMessage(context.Background(), Message{
			Role:    "user",
			Content: "message " + string(rune(i)),
		})
	}

	messages, err := memory.GetMessages(context.Background(), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(messages))
	}
}

func TestSummaryMemory_Clear(t *testing.T) {
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	memory := NewSummaryMemory(10, llm)

	for i := 0; i < 3; i++ {
		memory.AddMessage(context.Background(), Message{
			Role:    "user",
			Content: "message " + string(rune(i)),
		})
	}

	err := memory.Clear(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(memory.messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(memory.messages))
	}
}

func TestMemoryStep_Execute(t *testing.T) {
	memory := NewBufferMemory(10)
	memoryStep := NewMemoryStep("memory-step", memory, "user", "content", "context")

	result, err := memoryStep.Execute(context.Background(), map[string]interface{}{
		"content": "test message",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, exists := result["context"]; !exists {
		t.Error("expected 'context' key in result")
	}

	if len(memory.messages) != 1 {
		t.Errorf("expected 1 message in memory, got %d", len(memory.messages))
	}
}

func TestMemoryStep_Execute_MissingContent(t *testing.T) {
	memory := NewBufferMemory(10)
	memoryStep := NewMemoryStep("memory-step", memory, "user", "content", "context")

	_, err := memoryStep.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing content")
	}
}

func TestMemoryStep_GetInputKeys(t *testing.T) {
	memory := NewBufferMemory(10)
	memoryStep := NewMemoryStep("memory-step", memory, "user", "content", "context")

	keys := memoryStep.GetInputKeys()
	if len(keys) != 1 || keys[0] != "content" {
		t.Errorf("expected ['content'], got %v", keys)
	}
}

func TestMemoryStep_GetOutputKeys(t *testing.T) {
	memory := NewBufferMemory(10)
	memoryStep := NewMemoryStep("memory-step", memory, "user", "content", "context")

	keys := memoryStep.GetOutputKeys()
	if len(keys) != 1 || keys[0] != "context" {
		t.Errorf("expected ['context'], got %v", keys)
	}
}
