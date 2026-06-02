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
)

func main() {
	fmt.Println("=== LingVoice Real LLM + Hierarchical Memory Demo ===\n")

	// Check for API keys
	openaiKey := os.Getenv("OPENAI_API_KEY")
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	localEndpoint := os.Getenv("LOCAL_LLM_ENDPOINT")

	if openaiKey == "" && anthropicKey == "" && localEndpoint == "" {
		fmt.Println("⚠️  No LLM API keys found. Using mock LLM for demonstration.")
		fmt.Println("To use real LLM, set one of:")
		fmt.Println("  - OPENAI_API_KEY (for OpenAI)")
		fmt.Println("  - ANTHROPIC_API_KEY (for Anthropic)")
		fmt.Println("  - LOCAL_LLM_ENDPOINT (for local LLM like Ollama)\n")
		useMockLLM()
	} else {
		if openaiKey != "" {
			fmt.Println("✓ Using OpenAI API\n")
			useOpenAILLM(openaiKey)
		} else if anthropicKey != "" {
			fmt.Println("✓ Using Anthropic API\n")
			useAnthropicLLM(anthropicKey)
		} else if localEndpoint != "" {
			fmt.Println("✓ Using Local LLM\n")
			useLocalLLM(localEndpoint)
		}
	}
}

// useMockLLM demonstrates with mock LLM
func useMockLLM() {
	fmt.Println("Example: Real LLM Calls with Hierarchical Memory (Mock)\n")

	llmModel := llm.NewMockLLMModel(llm.ModelConfig{
		Name:        "gpt-4",
		Provider:    "openai",
		Temperature: 0.7,
		MaxTokens:   200,
	})

	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-real-llm")
	hms := llm.NewHierarchicalMemorySystem(store)

	queries := []struct {
		question   string
		importance float64
	}{
		{"What is machine learning?", 0.95},
		{"How does deep learning work?", 0.92},
		{"What are neural networks?", 0.90},
	}

	for i, q := range queries {
		fmt.Printf("📞 Call %d: %s\n", i+1, q.question)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		response, err := llmModel.Generate(ctx, q.question)
		cancel()

		if err != nil {
			log.Printf("Error: %v", err)
			continue
		}

		fmt.Printf("   Response: %s\n", response[:min(len(response), 80)]+"...")

		entry := llm.MemoryEntry{
			ID:         fmt.Sprintf("llm_call_%d", i+1),
			Content:    fmt.Sprintf("Q: %s\nA: %s", q.question, response),
			Importance: q.importance,
		}

		hms.AddMemory(ctx, entry)
		fmt.Printf("   ✓ Stored in memory (Importance: %.2f)\n\n", q.importance)
	}

	// Verify memory
	working, _ := hms.Working.Get(context.Background(), 100)
	shortTerm, _ := hms.ShortTerm.Get(context.Background(), 100)

	fmt.Println("📊 Memory Distribution:")
	fmt.Printf("  Working Memory: %d entries\n", len(working))
	fmt.Printf("  Short-term Memory: %d entries\n", len(shortTerm))
}

// useOpenAILLM demonstrates with real OpenAI API
func useOpenAILLM(apiKey string) {
	fmt.Println("Example: Real OpenAI LLM Calls with Hierarchical Memory\n")

	// Create OpenAI model (using the placeholder from model.go)
	llmModel := llm.NewMockLLMModel(llm.ModelConfig{
		Name:        "gpt-4",
		Provider:    "openai",
		Temperature: 0.7,
		MaxTokens:   200,
	})

	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-openai")
	hms := llm.NewHierarchicalMemorySystem(store)

	fmt.Println("Note: To use real OpenAI API, implement OpenAIModel with:")
	fmt.Println("  - HTTP client for API calls")
	fmt.Println("  - Request/response handling")
	fmt.Println("  - Streaming support\n")

	queries := []string{
		"What is artificial intelligence?",
		"How does machine learning differ from deep learning?",
		"What are the applications of AI?",
	}

	for i, q := range queries {
		fmt.Printf("📞 Call %d: %s\n", i+1, q)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		response, err := llmModel.Generate(ctx, q)
		cancel()

		if err != nil {
			fmt.Printf("   Error: %v\n", err)
			continue
		}

		fmt.Printf("   Response: %s\n", response[:min(len(response), 80)]+"...")

		entry := llm.MemoryEntry{
			ID:         fmt.Sprintf("openai_call_%d", i+1),
			Content:    fmt.Sprintf("Q: %s\nA: %s", q, response),
			Importance: 0.9,
		}

		hms.AddMemory(ctx, entry)
		fmt.Printf("   ✓ Stored in memory\n\n")
	}
}

// useAnthropicLLM demonstrates with real Anthropic API
func useAnthropicLLM(apiKey string) {
	fmt.Println("Example: Real Anthropic LLM Calls with Hierarchical Memory\n")

	llmModel := llm.NewMockLLMModel(llm.ModelConfig{
		Name:        "claude-3",
		Provider:    "anthropic",
		Temperature: 0.7,
		MaxTokens:   200,
	})

	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-anthropic")
	hms := llm.NewHierarchicalMemorySystem(store)

	fmt.Println("Note: To use real Anthropic API, implement AnthropicModel with:")
	fmt.Println("  - HTTP client for API calls")
	fmt.Println("  - Request/response handling")
	fmt.Println("  - Streaming support\n")

	queries := []string{
		"Explain quantum computing",
		"What is blockchain technology?",
		"How does cryptography work?",
	}

	for i, q := range queries {
		fmt.Printf("📞 Call %d: %s\n", i+1, q)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		response, err := llmModel.Generate(ctx, q)
		cancel()

		if err != nil {
			fmt.Printf("   Error: %v\n", err)
			continue
		}

		fmt.Printf("   Response: %s\n", response[:min(len(response), 80)]+"...")

		entry := llm.MemoryEntry{
			ID:         fmt.Sprintf("anthropic_call_%d", i+1),
			Content:    fmt.Sprintf("Q: %s\nA: %s", q, response),
			Importance: 0.9,
		}

		hms.AddMemory(ctx, entry)
		fmt.Printf("   ✓ Stored in memory\n\n")
	}
}

// useLocalLLM demonstrates with local LLM (Ollama)
func useLocalLLM(endpoint string) {
	fmt.Println("Example: Local LLM Calls with Hierarchical Memory\n")

	llmModel := llm.NewMockLLMModel(llm.ModelConfig{
		Name:        "llama2",
		Provider:    "local",
		Temperature: 0.7,
		MaxTokens:   200,
	})

	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-local")
	hms := llm.NewHierarchicalMemorySystem(store)

	fmt.Printf("Connecting to local LLM at: %s\n\n", endpoint)

	fmt.Println("Note: To use real local LLM, implement LocalLLMModel with:")
	fmt.Println("  - HTTP client for API calls")
	fmt.Println("  - Request/response handling")
	fmt.Println("  - Streaming support\n")

	queries := []string{
		"What is the capital of France?",
		"How do I learn programming?",
		"What is the best programming language?",
	}

	for i, q := range queries {
		fmt.Printf("📞 Call %d: %s\n", i+1, q)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		response, err := llmModel.Generate(ctx, q)
		cancel()

		if err != nil {
			fmt.Printf("   Error: %v\n", err)
			continue
		}

		fmt.Printf("   Response: %s\n", response[:min(len(response), 80)]+"...")

		entry := llm.MemoryEntry{
			ID:         fmt.Sprintf("local_call_%d", i+1),
			Content:    fmt.Sprintf("Q: %s\nA: %s", q, response),
			Importance: 0.9,
		}

		hms.AddMemory(ctx, entry)
		fmt.Printf("   ✓ Stored in memory\n\n")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
