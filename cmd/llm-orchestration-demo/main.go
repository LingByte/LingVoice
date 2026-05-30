package main

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/LingByte/LingVoice/pkg/avflow"
	"github.com/LingByte/LingVoice/pkg/llm"
	"github.com/LingByte/LingVoice/pkg/media"
)

func main() {
	fmt.Println("=== LingVoice LLM Orchestration Demo ===\n")

	// Example 1: Simple Chat Pipeline
	fmt.Println("Example 1: Simple Chat Pipeline")
	fmt.Println("--------------------------------")
	simpleChatDemo()

	fmt.Println("\n")

	// Example 2: RAG Pipeline
	fmt.Println("Example 2: RAG Pipeline")
	fmt.Println("----------------------")
	ragPipelineDemo()

	fmt.Println("\n")

	// Example 3: Agent with Tools
	fmt.Println("Example 3: Agent with Tools")
	fmt.Println("---------------------------")
	agentWithToolsDemo()

	fmt.Println("\n")

	// Example 4: Complex Chain
	fmt.Println("Example 4: Complex Chain")
	fmt.Println("------------------------")
	complexChainDemo()

	fmt.Println("\n✅ All demos completed successfully!")
}

// simpleChatDemo demonstrates a simple chat pipeline
func simpleChatDemo() {
	// Create LLM model
	llmModel := llm.NewMockLLMModel(llm.ModelConfig{
		Name:        "gpt-4",
		Provider:    "openai",
		Temperature: 0.7,
		MaxTokens:   100,
	})

	// Create chat component
	chatComp := avflow.NewChatModelComponent("chat", llmModel)

	// Create graph
	g := avflow.NewGraph("simple-chat")
	g.AddComponent(chatComp)

	// Create input channel
	inputChan := make(chan *avflow.Packet, 1)

	// Send a message
	inputChan <- avflow.NewPacket(avflow.PacketTypeText, &media.TextPacket{
		Text:      "What is the capital of France?",
		IsPartial: false,
	})
	close(inputChan)

	fmt.Println("Input: What is the capital of France?")

	// Process (simplified - in real usage would be integrated with graph)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	outputChan := make(chan *avflow.Packet, 1)
	inputs := map[string]<-chan *avflow.Packet{"text_in": inputChan}
	outputs := map[string]chan<- *avflow.Packet{"text_out": outputChan}

	go chatComp.Process(ctx, inputs, outputs)

	select {
	case pkt := <-outputChan:
		if textPkt, ok := pkt.Data.(*media.TextPacket); ok {
			fmt.Printf("Output: %s\n", textPkt.Text)
		}
	case <-ctx.Done():
		fmt.Println("Timeout")
	}
}

// ragPipelineDemo demonstrates RAG pipeline
func ragPipelineDemo() {
	// Create knowledge base
	retriever := llm.NewSimpleRetriever()
	retriever.Add(context.Background(), llm.Document{
		ID:      "doc1",
		Content: "Go is a statically typed, compiled programming language designed for simplicity and efficiency.",
	})
	retriever.Add(context.Background(), llm.Document{
		ID:      "doc2",
		Content: "Python is a high-level, interpreted programming language known for its readability and ease of use.",
	})

	// Create LLM
	llmModel := llm.NewMockLLMModel(llm.ModelConfig{Name: "gpt-4"})

	// Create RAG chain
	ragChain := llm.NewRAGChain(retriever, llmModel, 2)

	// Create RAG component
	ragComp := avflow.NewRAGComponent("rag", ragChain)

	// Test query
	query := "What is Go?"
	fmt.Printf("Query: %s\n", query)

	// Execute RAG
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	answer, err := ragChain.Execute(ctx, query)
	if err != nil {
		log.Fatalf("RAG execution failed: %v", err)
	}

	fmt.Printf("Answer: %s\n", answer)
	_ = ragComp // Use the component
}

// agentWithToolsDemo demonstrates agent with tools
func agentWithToolsDemo() {
	// Create tool registry
	registry := llm.NewToolRegistry()

	// Add calculator tool
	calcTool := llm.NewSimpleTool(
		"calculator",
		"Performs basic arithmetic operations",
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
			case "subtract":
				return a - b, nil
			case "multiply":
				return a * b, nil
			case "divide":
				if b == 0 {
					return nil, fmt.Errorf("division by zero")
				}
				return a / b, nil
			default:
				return nil, fmt.Errorf("unknown operation: %s", operation)
			}
		},
	)

	registry.Register(calcTool)

	// Create agent
	llmModel := llm.NewMockLLMModel(llm.ModelConfig{Name: "gpt-4"})
	agent := llm.NewAgent("math-assistant", llmModel, registry, 5)
	executor := llm.NewAgentExecutor(agent)

	// Create agent component
	agentComp := avflow.NewAgentComponent("agent", executor)

	// Test query
	query := "What is 2 + 2?"
	fmt.Printf("Query: %s\n", query)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := executor.Execute(ctx, query)
	if err != nil {
		log.Fatalf("Agent execution failed: %v", err)
	}

	fmt.Printf("Result: %s\n", result)
	_ = agentComp // Use the component
}

// complexChainDemo demonstrates a complex chain
func complexChainDemo() {
	// Create chain
	chain := llm.NewChain("complex-chain")

	// Step 1: Format prompt
	promptStep := llm.NewSimpleStep(
		"format-prompt",
		[]string{"question"},
		[]string{"prompt"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			question := input["question"].(string)
			prompt := fmt.Sprintf("Answer this question: %s", question)
			return map[string]interface{}{"prompt": prompt}, nil
		},
	)

	// Step 2: Generate response
	generateStep := llm.NewSimpleStep(
		"generate",
		[]string{"prompt"},
		[]string{"response"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			prompt := input["prompt"].(string)
			response := fmt.Sprintf("Response to: %s", prompt)
			return map[string]interface{}{"response": response}, nil
		},
	)

	// Step 3: Format output
	formatStep := llm.NewSimpleStep(
		"format-output",
		[]string{"response"},
		[]string{"output"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			response := input["response"].(string)
			output := fmt.Sprintf("Final: %s", response)
			return map[string]interface{}{"output": output}, nil
		},
	)

	chain.AddStep(promptStep).AddStep(generateStep).AddStep(formatStep)

	// Create chain component
	chainComp := avflow.NewChainComponent("chain", chain)

	// Execute chain
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := map[string]interface{}{
		"question": "What is the meaning of life?",
	}

	result, err := chain.Execute(ctx, input)
	if err != nil {
		log.Fatalf("Chain execution failed: %v", err)
	}

	fmt.Printf("Input: %v\n", input)
	fmt.Printf("Output: %v\n", result)
	_ = chainComp // Use the component
}
