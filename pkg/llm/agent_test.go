package llm

import (
	"context"
	"testing"
)

func TestSimpleTool_Create(t *testing.T) {
	tool := NewSimpleTool(
		"test-tool",
		"A test tool",
		map[string]interface{}{"input": "string"},
		func(ctx context.Context, input map[string]interface{}) (interface{}, error) {
			return "result", nil
		},
	)

	if tool.GetName() != "test-tool" {
		t.Errorf("expected 'test-tool', got %s", tool.GetName())
	}

	if tool.GetDescription() != "A test tool" {
		t.Errorf("expected 'A test tool', got %s", tool.GetDescription())
	}
}

func TestSimpleTool_Execute(t *testing.T) {
	tool := NewSimpleTool(
		"test-tool",
		"A test tool",
		map[string]interface{}{"input": "string"},
		func(ctx context.Context, input map[string]interface{}) (interface{}, error) {
			return "processed", nil
		},
	)

	result, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "processed" {
		t.Errorf("expected 'processed', got %v", result)
	}
}

func TestToolRegistry_Register(t *testing.T) {
	registry := NewToolRegistry()
	tool := NewSimpleTool(
		"test-tool",
		"A test tool",
		map[string]interface{}{},
		func(ctx context.Context, input map[string]interface{}) (interface{}, error) {
			return nil, nil
		},
	)

	err := registry.Register(tool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestToolRegistry_Register_EmptyName(t *testing.T) {
	registry := NewToolRegistry()
	tool := NewSimpleTool(
		"",
		"A test tool",
		map[string]interface{}{},
		func(ctx context.Context, input map[string]interface{}) (interface{}, error) {
			return nil, nil
		},
	)

	err := registry.Register(tool)
	if err == nil {
		t.Fatal("expected error for empty tool name")
	}
}

func TestToolRegistry_Get(t *testing.T) {
	registry := NewToolRegistry()
	tool := NewSimpleTool(
		"test-tool",
		"A test tool",
		map[string]interface{}{},
		func(ctx context.Context, input map[string]interface{}) (interface{}, error) {
			return nil, nil
		},
	)

	registry.Register(tool)

	retrieved, err := registry.Get("test-tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if retrieved.GetName() != "test-tool" {
		t.Errorf("expected 'test-tool', got %s", retrieved.GetName())
	}
}

func TestToolRegistry_Get_NotFound(t *testing.T) {
	registry := NewToolRegistry()

	_, err := registry.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent tool")
	}
}

func TestToolRegistry_List(t *testing.T) {
	registry := NewToolRegistry()

	for i := 0; i < 3; i++ {
		tool := NewSimpleTool(
			"tool"+string(rune(i)),
			"Tool "+string(rune(i)),
			map[string]interface{}{},
			func(ctx context.Context, input map[string]interface{}) (interface{}, error) {
				return nil, nil
			},
		)
		registry.Register(tool)
	}

	tools := registry.List()
	if len(tools) != 3 {
		t.Errorf("expected 3 tools, got %d", len(tools))
	}
}

func TestAgent_Create(t *testing.T) {
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	registry := NewToolRegistry()
	agent := NewAgent("test-agent", llm, registry, 5)

	if agent.name != "test-agent" {
		t.Errorf("expected 'test-agent', got %s", agent.name)
	}
}

func TestAgent_Run(t *testing.T) {
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	registry := NewToolRegistry()
	agent := NewAgent("test-agent", llm, registry, 5)

	result, err := agent.Run(context.Background(), "test input")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestAgentExecutor_Create(t *testing.T) {
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	registry := NewToolRegistry()
	agent := NewAgent("test-agent", llm, registry, 5)
	executor := NewAgentExecutor(agent)

	if executor.agent == nil {
		t.Fatal("expected non-nil agent")
	}
}

func TestAgentExecutor_Execute(t *testing.T) {
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	registry := NewToolRegistry()
	agent := NewAgent("test-agent", llm, registry, 5)
	executor := NewAgentExecutor(agent)

	result, err := executor.Execute(context.Background(), "test input")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestAgentStep_Execute(t *testing.T) {
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	registry := NewToolRegistry()
	agent := NewAgent("test-agent", llm, registry, 5)
	executor := NewAgentExecutor(agent)
	agentStep := NewAgentStep("agent-step", executor, "input", "output")

	result, err := agentStep.Execute(context.Background(), map[string]interface{}{
		"input": "test query",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, exists := result["output"]; !exists {
		t.Error("expected 'output' key in result")
	}
}

func TestAgentStep_GetInputKeys(t *testing.T) {
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	registry := NewToolRegistry()
	agent := NewAgent("test-agent", llm, registry, 5)
	executor := NewAgentExecutor(agent)
	agentStep := NewAgentStep("agent-step", executor, "input", "output")

	keys := agentStep.GetInputKeys()
	if len(keys) != 1 || keys[0] != "input" {
		t.Errorf("expected ['input'], got %v", keys)
	}
}

func TestAgentStep_GetOutputKeys(t *testing.T) {
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	registry := NewToolRegistry()
	agent := NewAgent("test-agent", llm, registry, 5)
	executor := NewAgentExecutor(agent)
	agentStep := NewAgentStep("agent-step", executor, "input", "output")

	keys := agentStep.GetOutputKeys()
	if len(keys) != 1 || keys[0] != "output" {
		t.Errorf("expected ['output'], got %v", keys)
	}
}
