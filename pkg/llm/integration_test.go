package llm

import (
	"context"
	"testing"
)

func TestCompleteChainWithRAG(t *testing.T) {
	// Setup RAG
	retriever := NewSimpleRetriever()
	retriever.Add(context.Background(), Document{
		ID:      "doc1",
		Content: "Go is a programming language",
	})

	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	ragChain := NewRAGChain(retriever, llm, 1)

	// Build chain
	chain := NewChain("rag-chain")

	ragStep := NewRAGStep("rag", ragChain, "query", "answer")
	chain.AddStep(ragStep)

	// Execute
	result, err := chain.Execute(context.Background(), map[string]interface{}{
		"query": "What is Go?",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, exists := result["answer"]; !exists {
		t.Error("expected 'answer' in result")
	}
}

func TestCompleteChainWithMemory(t *testing.T) {
	// Setup memory
	memory := NewBufferMemory(10)

	// Build chain
	chain := NewChain("memory-chain")

	memoryStep := NewMemoryStep("memory", memory, "user", "message", "context")
	chain.AddStep(memoryStep)

	// Execute
	result, err := chain.Execute(context.Background(), map[string]interface{}{
		"message": "Hello, AI!",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, exists := result["context"]; !exists {
		t.Error("expected 'context' in result")
	}

	if len(memory.messages) != 1 {
		t.Errorf("expected 1 message in memory, got %d", len(memory.messages))
	}
}

func TestCompleteChainWithAgent(t *testing.T) {
	// Setup agent
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	registry := NewToolRegistry()

	// Add a simple tool
	tool := NewSimpleTool(
		"calculator",
		"A simple calculator",
		map[string]interface{}{"operation": "string"},
		func(ctx context.Context, input map[string]interface{}) (interface{}, error) {
			return "42", nil
		},
	)
	registry.Register(tool)

	agent := NewAgent("test-agent", llm, registry, 5)
	executor := NewAgentExecutor(agent)

	// Build chain
	chain := NewChain("agent-chain")
	agentStep := NewAgentStep("agent", executor, "input", "output")
	chain.AddStep(agentStep)

	// Execute
	result, err := chain.Execute(context.Background(), map[string]interface{}{
		"input": "What is 2+2?",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, exists := result["output"]; !exists {
		t.Error("expected 'output' in result")
	}
}

func TestComplexChainWithConditional(t *testing.T) {
	chain := NewChain("complex-chain")

	// First step: process input
	step1 := NewSimpleStep(
		"step1",
		[]string{"input"},
		[]string{"processed"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			val := input["input"].(string)
			return map[string]interface{}{"processed": val + "-processed"}, nil
		},
	)

	// Conditional step
	trueStep := NewSimpleStep(
		"true-branch",
		[]string{"processed"},
		[]string{"result"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{"result": "true-path"}, nil
		},
	)

	falseStep := NewSimpleStep(
		"false-branch",
		[]string{"processed"},
		[]string{"result"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{"result": "false-path"}, nil
		},
	)

	condStep := NewConditionalStep(
		"conditional",
		func(ctx context.Context, input map[string]interface{}) (bool, error) {
			return true, nil
		},
		trueStep,
		falseStep,
	)

	chain.AddStep(step1).AddStep(condStep)

	result, err := chain.Execute(context.Background(), map[string]interface{}{
		"input": "test",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result["result"] != "true-path" {
		t.Errorf("expected 'true-path', got %v", result["result"])
	}
}

func TestChainWithRAGAndMemory(t *testing.T) {
	// Setup RAG
	retriever := NewSimpleRetriever()
	retriever.Add(context.Background(), Document{
		ID:      "doc1",
		Content: "Python is a programming language",
	})

	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	ragChain := NewRAGChain(retriever, llm, 1)

	// Setup memory
	memory := NewBufferMemory(10)

	// Build chain
	chain := NewChain("rag-memory-chain")

	// Add user message to memory
	memoryStep1 := NewMemoryStep("memory-user", memory, "user", "question", "context1")
	chain.AddStep(memoryStep1)

	// Retrieve answer with RAG
	ragStep := NewRAGStep("rag", ragChain, "question", "answer")
	chain.AddStep(ragStep)

	// Add assistant response to memory
	memoryStep2 := NewMemoryStep("memory-assistant", memory, "assistant", "answer", "context2")
	chain.AddStep(memoryStep2)

	// Execute
	result, err := chain.Execute(context.Background(), map[string]interface{}{
		"question": "What is Python?",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, exists := result["answer"]; !exists {
		t.Error("expected 'answer' in result")
	}

	if len(memory.messages) != 2 {
		t.Errorf("expected 2 messages in memory, got %d", len(memory.messages))
	}
}

func TestAgentWithTools(t *testing.T) {
	// Setup tools
	registry := NewToolRegistry()

	tool1 := NewSimpleTool(
		"add",
		"Add two numbers",
		map[string]interface{}{"a": "number", "b": "number"},
		func(ctx context.Context, input map[string]interface{}) (interface{}, error) {
			a := input["a"].(float64)
			b := input["b"].(float64)
			return a + b, nil
		},
	)

	tool2 := NewSimpleTool(
		"multiply",
		"Multiply two numbers",
		map[string]interface{}{"a": "number", "b": "number"},
		func(ctx context.Context, input map[string]interface{}) (interface{}, error) {
			a := input["a"].(float64)
			b := input["b"].(float64)
			return a * b, nil
		},
	)

	registry.Register(tool1)
	registry.Register(tool2)

	// Setup agent
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	agent := NewAgent("calculator-agent", llm, registry, 5)

	// Verify tools are registered
	tools := registry.List()
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}

	// Execute agent
	result, err := agent.Run(context.Background(), "What is 2+3?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestChainErrorHandling(t *testing.T) {
	chain := NewChain("error-chain")

	step := NewSimpleStep(
		"failing-step",
		[]string{"input"},
		[]string{"output"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			return nil, nil // Missing required output
		},
	)

	chain.AddStep(step)

	_, err := chain.Execute(context.Background(), map[string]interface{}{
		"input": "test",
	})

	// Should succeed even if output is missing
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
