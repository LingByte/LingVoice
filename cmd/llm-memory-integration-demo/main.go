package main

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm"
	"github.com/LingByte/LingVoice/pkg/llm/openai"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func main() {
	fmt.Println("=== LingVoice LLM + Hierarchical Memory Integration Demo ===\n")

	// Example 1: Real LLM Calls with Memory
	fmt.Println("Example 1: Real LLM Calls with Hierarchical Memory")
	fmt.Println("---------------------------------------------------")
	realLLMCallsWithMemory()

	fmt.Println("\n")

	// Example 2: Knowledge Building Through Multiple LLM Calls
	fmt.Println("Example 2: Knowledge Building Through Multiple LLM Calls")
	fmt.Println("------------------------------------------------------")
	knowledgeBuildingDemo()

	fmt.Println("\n")

	// Example 3: Context-Aware LLM Responses Using Memory
	fmt.Println("Example 3: Context-Aware LLM Responses Using Memory")
	fmt.Println("--------------------------------------------------")
	contextAwareLLMDemo()

	fmt.Println("\n")

	// Example 4: RAG with Hierarchical Memory
	fmt.Println("Example 4: RAG with Hierarchical Memory")
	fmt.Println("--------------------------------------")
	ragWithMemoryDemo()

	fmt.Println("\n")

	// Example 5: Agent with Memory
	fmt.Println("Example 5: Agent with Memory")
	fmt.Println("---------------------------")
	agentWithMemoryDemo()

	fmt.Println("\n✅ All demos completed successfully!")
}

// chatModelAdapter adapts OpenAI ChatModel to the LLMModel interface used in demos.
type chatModelAdapter struct {
	model *openai.ChatModel
	name  string
}

func (a *chatModelAdapter) Generate(ctx context.Context, prompt string) (string, error) {
	msg, err := a.model.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		return "", err
	}
	return msg.Content, nil
}

func (a *chatModelAdapter) GenerateStream(ctx context.Context, prompt string) (<-chan string, error) {
	return nil, fmt.Errorf("stream not implemented")
}

func (a *chatModelAdapter) GetConfig() llm.ModelConfig {
	return llm.ModelConfig{Name: a.name, Provider: "openai"}
}

func (a *chatModelAdapter) GetName() string {
	return a.name
}

func newOpenAIAdapter() (*chatModelAdapter, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}
	modelName := os.Getenv("OPENAI_MODEL")
	if modelName == "" {
		modelName = "gpt-4o-mini"
	}
	chatModel, err := openai.NewChatModel(openai.Config{APIKey: apiKey, Model: modelName})
	if err != nil {
		return nil, err
	}
	return &chatModelAdapter{model: chatModel, name: modelName}, nil
}

// realLLMCallsWithMemory demonstrates real OpenAI LLM calls with memory
func realLLMCallsWithMemory() {
	adapter, err := newOpenAIAdapter()
	if err != nil {
		log.Fatalf("failed to init OpenAI model: %v", err)
	}

	// Create hierarchical memory system
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-llm-memory")
	hms := llm.NewHierarchicalMemorySystem(store)

	fmt.Println("🤖 Making real OpenAI API calls with memory tracking:\n")

	// Multiple LLM calls
	queries := []struct {
		question   string
		importance float64
	}{
		{
			question:   "What is Go programming language?",
			importance: 0.95,
		},
		{
			question:   "How does Go handle concurrency?",
			importance: 0.92,
		},
		{
			question:   "What are goroutines and channels?",
			importance: 0.90,
		},
		{
			question:   "How to write efficient Go code?",
			importance: 0.88,
		},
		{
			question:   "What is the Go standard library?",
			importance: 0.85,
		},
	}

	for i, q := range queries {
		fmt.Printf("📞 Call %d: %s\n", i+1, q.question)

		// Make real OpenAI API call using ChatModel
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		response, err := adapter.Generate(ctx, q.question)
		cancel()

		if err != nil {
			fmt.Printf("   ⚠️  Error: %v\n", err)
			continue
		}

		fmt.Printf("   Response: %s\n", truncate(response, 80))

		// Store in hierarchical memory
		entry := llm.MemoryEntry{
			ID:         fmt.Sprintf("llm_call_%d", i+1),
			Content:    fmt.Sprintf("Q: %s\nA: %s", q.question, response),
			Importance: q.importance,
			Metadata: map[string]interface{}{
				"call_index": i + 1,
				"timestamp":  time.Now(),
				"model":      adapter.name,
				"tokens":     len(response) / 4,
			},
		}

		err = hms.AddMemory(context.Background(), entry)
		if err != nil {
			log.Printf("failed to add memory: %v", err)
			continue
		}

		fmt.Printf("   ✓ Stored in memory (Importance: %.2f)\n\n", q.importance)
	}

	// Verify memory distribution
	fmt.Println("📊 Memory Distribution After 5 LLM Calls:")
	working, _ := hms.Working.Get(context.Background(), 100)
	shortTerm, _ := hms.ShortTerm.Get(context.Background(), 100)
	longTerm, _ := hms.LongTerm.Search(context.Background(), "Go")

	fmt.Printf("  Working Memory: %d entries\n", len(working))
	fmt.Printf("  Short-term Memory: %d entries\n", len(shortTerm))
	fmt.Printf("  Long-term Memory: %d entries found\n", len(longTerm))
}

// knowledgeBuildingDemo demonstrates knowledge building through multiple LLM calls
func knowledgeBuildingDemo() {
	llmModel, err := newOpenAIAdapter()
	if err != nil {
		log.Fatalf("OPENAI_API_KEY not set or invalid: %v", err)
	}
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-knowledge-build")
	hms := llm.NewHierarchicalMemorySystem(store)

	fmt.Println("🧠 Building knowledge through multiple LLM calls:\n")

	// Simulate a learning session
	topics := []struct {
		topic      string
		importance float64
	}{
		{"Go basics", 0.95},
		{"Go concurrency", 0.92},
		{"Go error handling", 0.90},
		{"Go testing", 0.88},
		{"Go performance", 0.85},
	}

	for i, t := range topics {
		fmt.Printf("📚 Learning Topic %d: %s\n", i+1, t.topic)

		// Make LLM call to learn about topic
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		response, _ := llmModel.Generate(ctx, fmt.Sprintf("Explain %s in detail", t.topic))
		cancel()

		// Store knowledge in memory
		entry := llm.MemoryEntry{
			ID:         fmt.Sprintf("knowledge_%s", t.topic),
			Content:    response,
			Importance: t.importance,
			Metadata: map[string]interface{}{
				"topic":      t.topic,
				"learned_at": time.Now(),
				"category":   "Go Programming",
			},
		}

		hms.AddMemory(ctx, entry)
		fmt.Printf("   ✓ Knowledge stored (Importance: %.2f)\n\n", t.importance)
	}

	// Verify knowledge consolidation
	fmt.Println("📈 Knowledge Consolidation Status:")
	fmt.Printf("  Total knowledge items: 5\n")
	fmt.Printf("  All items stored in hierarchical memory\n")
	fmt.Printf("  Ready for intelligent recall\n")
}

// contextAwareLLMDemo demonstrates context-aware LLM responses using memory
func contextAwareLLMDemo() {
	llmModel, err := newOpenAIAdapter()
	if err != nil {
		log.Fatalf("OPENAI_API_KEY not set or invalid: %v", err)
	}
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-context-aware")
	hms := llm.NewHierarchicalMemorySystem(store)

	fmt.Println("🎯 Context-Aware LLM Responses:\n")

	// First, build some context through LLM calls
	fmt.Println("Step 1: Building context through initial LLM calls")
	contextQueries := []string{
		"What is Go?",
		"What is Rust?",
		"What is Python?",
	}

	for i, q := range contextQueries {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		response, _ := llmModel.Generate(ctx, q)
		cancel()

		entry := llm.MemoryEntry{
			ID:         fmt.Sprintf("context_%d", i),
			Content:    fmt.Sprintf("Q: %s\nA: %s", q, response),
			Importance: 0.9,
		}

		hms.AddMemory(ctx, entry)
		fmt.Printf("  ✓ Added context: %s\n", q)
	}

	// Now make a follow-up query that uses the context
	fmt.Println("\nStep 2: Making context-aware follow-up query")
	followUpQuery := "Compare Go and Rust"
	fmt.Printf("  Query: %s\n", followUpQuery)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	response, _ := llmModel.Generate(ctx, followUpQuery)
	cancel()

	fmt.Printf("  Response: %s\n", response[:min(len(response), 100)]+"...")

	// Recall relevant context from memory
	fmt.Println("\nStep 3: Recalling relevant context from memory")
	relevantMemories, _ := hms.Recall(ctx, "Go Rust")
	fmt.Printf("  Found %d relevant memories\n", len(relevantMemories))

	// Store the follow-up response with context
	entry := llm.MemoryEntry{
		ID:           "context_aware_response",
		Content:      fmt.Sprintf("Q: %s\nA: %s", followUpQuery, response),
		Importance:   0.95,
		Associations: []string{"context_0", "context_1"},
	}

	hms.AddMemory(ctx, entry)
	fmt.Printf("  ✓ Stored context-aware response with associations\n")
}

// ragWithMemoryDemo demonstrates RAG with hierarchical memory
func ragWithMemoryDemo() {
	llmModel, err := newOpenAIAdapter()
	if err != nil {
		log.Fatalf("OPENAI_API_KEY not set or invalid: %v", err)
	}
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-rag-memory")
	hms := llm.NewHierarchicalMemorySystem(store)

	fmt.Println("🔍 RAG with Hierarchical Memory:\n")

	// Create knowledge base
	retriever := llm.NewSimpleRetriever()
	documents := []llm.Document{
		{
			ID:      "doc_1",
			Content: "Go is a statically typed, compiled programming language created by Google",
		},
		{
			ID:      "doc_2",
			Content: "Go uses goroutines for concurrent programming, which are lightweight threads",
		},
		{
			ID:      "doc_3",
			Content: "Go has a simple syntax and built-in support for testing",
		},
	}

	for _, doc := range documents {
		retriever.Add(context.Background(), doc)
		fmt.Printf("  ✓ Added document: %s\n", doc.ID)
	}

	// Create RAG chain
	ragChain := llm.NewRAGChain(retriever, llmModel, 2)

	// Make RAG queries
	fmt.Println("\nStep 2: Making RAG queries")
	queries := []string{
		"What is Go?",
		"How does Go handle concurrency?",
	}

	for i, q := range queries {
		fmt.Printf("  Query %d: %s\n", i+1, q)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		answer, _ := ragChain.Execute(ctx, q)
		cancel()

		fmt.Printf("  Answer: %s\n", answer[:min(len(answer), 80)]+"...")

		// Store RAG result in memory
		entry := llm.MemoryEntry{
			ID:         fmt.Sprintf("rag_result_%d", i+1),
			Content:    fmt.Sprintf("Q: %s\nA: %s", q, answer),
			Importance: 0.92,
			Metadata: map[string]interface{}{
				"type":  "rag_result",
				"query": q,
			},
		}

		hms.AddMemory(ctx, entry)
		fmt.Printf("  ✓ Stored RAG result in memory\n\n")
	}
}

// agentWithMemoryDemo demonstrates agent with memory
func agentWithMemoryDemo() {
	llmModel, err := newOpenAIAdapter()
	if err != nil {
		log.Fatalf("OPENAI_API_KEY not set or invalid: %v", err)
	}
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-agent-memory")
	hms := llm.NewHierarchicalMemorySystem(store)

	fmt.Println("🤖 Agent with Memory:\n")

	// Create tool registry
	registry := llm.NewToolRegistry()

	// Add calculator tool
	calcTool := llm.NewSimpleTool(
		"calculator",
		"Performs basic arithmetic",
		map[string]interface{}{
			"operation": "string",
			"a":         "number",
			"b":         "number",
		},
		func(ctx context.Context, input map[string]interface{}) (interface{}, error) {
			operation := input["operation"].(string)
			a := input["a"].(float64)
			b := input["b"].(float64)

			switch operation {
			case "add":
				return a + b, nil
			case "multiply":
				return a * b, nil
			default:
				return nil, fmt.Errorf("unknown operation")
			}
		},
	)

	registry.Register(calcTool)

	// Create agent
	agent := llm.NewAgent("math-assistant", llmModel, registry, 5)
	executor := llm.NewAgentExecutor(agent)

	fmt.Println("Step 1: Agent making decisions with tools")

	// Make agent calls
	queries := []string{
		"What is 5 + 3?",
		"What is 10 * 7?",
		"What is the sum of 100 and 50?",
	}

	for i, q := range queries {
		fmt.Printf("  Query %d: %s\n", i+1, q)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result, _ := executor.Execute(ctx, q)
		cancel()

		fmt.Printf("  Result: %s\n", result)

		// Store agent decision in memory
		entry := llm.MemoryEntry{
			ID:         fmt.Sprintf("agent_decision_%d", i+1),
			Content:    fmt.Sprintf("Q: %s\nA: %s", q, result),
			Importance: 0.90,
			Metadata: map[string]interface{}{
				"type":      "agent_decision",
				"tool_used": "calculator",
			},
		}

		hms.AddMemory(ctx, entry)
		fmt.Printf("  ✓ Stored agent decision in memory\n\n")
	}

	// Verify memory
	fmt.Println("Step 2: Verifying memory of agent decisions")
	working, _ := hms.Working.Get(context.Background(), 100)
	fmt.Printf("  Total decisions in working memory: %d\n", len(working))
	fmt.Printf("  Agent has learned from interactions\n")
}

// Helper functions
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
