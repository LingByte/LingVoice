package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"

	"github.com/LingByte/LingVoice/pkg/llm"
	"github.com/LingByte/LingVoice/pkg/media"
)

// ChatModelComponent wraps an LLM model for chat-based interactions
type ChatModelComponent struct {
	BaseComponent
	model llm.LLMModel
	mu    sync.RWMutex
}

// NewChatModelComponent creates a new chat model component
func NewChatModelComponent(id string, model llm.LLMModel) *ChatModelComponent {
	return &ChatModelComponent{
		BaseComponent: NewBaseComponent(id, "ChatModel", []string{"text_in"}, []string{"text_out"}),
		model:         model,
	}
}

// Process processes text input and generates responses
func (cmc *ChatModelComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["text_in"]
	out := outputs["text_out"]

	for {
		select {
		case <-ctx.Done():
			return nil
		case pkt, ok := <-in:
			if !ok {
				return nil
			}

			if pkt.Type != PacketTypeText {
				continue
			}

			textPkt, ok := pkt.Data.(*media.TextPacket)
			if !ok {
				continue
			}

			// Skip partial results
			if textPkt.IsPartial {
				continue
			}

			cmc.mu.RLock()
			model := cmc.model
			cmc.mu.RUnlock()

			// Generate response
			response, err := model.Generate(ctx, textPkt.Text)
			if err != nil {
				return fmt.Errorf("model generation failed: %w", err)
			}

			// Send response
			select {
			case <-ctx.Done():
				return nil
			case out <- NewPacket(PacketTypeText, &media.TextPacket{
				Text:           response,
				IsLLMGenerated: true,
				IsEnd:          true,
			}):
			}
		}
	}
}

// RAGComponent implements retrieval-augmented generation
type RAGComponent struct {
	BaseComponent
	ragChain *llm.RAGChain
	mu       sync.RWMutex
}

// NewRAGComponent creates a new RAG component
func NewRAGComponent(id string, ragChain *llm.RAGChain) *RAGComponent {
	return &RAGComponent{
		BaseComponent: NewBaseComponent(id, "RAG", []string{"query_in"}, []string{"answer_out"}),
		ragChain:      ragChain,
	}
}

// Process processes queries and generates RAG-based answers
func (rc *RAGComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["query_in"]
	out := outputs["answer_out"]

	for {
		select {
		case <-ctx.Done():
			return nil
		case pkt, ok := <-in:
			if !ok {
				return nil
			}

			if pkt.Type != PacketTypeText {
				continue
			}

			textPkt, ok := pkt.Data.(*media.TextPacket)
			if !ok {
				continue
			}

			rc.mu.RLock()
			ragChain := rc.ragChain
			rc.mu.RUnlock()

			// Execute RAG
			answer, err := ragChain.Execute(ctx, textPkt.Text)
			if err != nil {
				return fmt.Errorf("rag execution failed: %w", err)
			}

			// Send answer
			select {
			case <-ctx.Done():
				return nil
			case out <- NewPacket(PacketTypeText, &media.TextPacket{
				Text:  answer,
				IsEnd: true,
			}):
			}
		}
	}
}

// PromptComponent formats prompts with templates
type PromptComponent struct {
	BaseComponent
	template string
	mu       sync.RWMutex
}

// NewPromptComponent creates a new prompt component
func NewPromptComponent(id string, template string) *PromptComponent {
	return &PromptComponent{
		BaseComponent: NewBaseComponent(id, "Prompt", []string{"input"}, []string{"prompt_out"}),
		template:      template,
	}
}

// Process formats input into a prompt using the template
func (pc *PromptComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["input"]
	out := outputs["prompt_out"]

	for {
		select {
		case <-ctx.Done():
			return nil
		case pkt, ok := <-in:
			if !ok {
				return nil
			}

			if pkt.Type != PacketTypeText {
				continue
			}

			textPkt, ok := pkt.Data.(*media.TextPacket)
			if !ok {
				continue
			}

			pc.mu.RLock()
			template := pc.template
			pc.mu.RUnlock()

			// Format prompt
			prompt := fmt.Sprintf(template, textPkt.Text)

			// Send formatted prompt
			select {
			case <-ctx.Done():
				return nil
			case out <- NewPacket(PacketTypeText, &media.TextPacket{
				Text:  prompt,
				IsEnd: true,
			}):
			}
		}
	}
}

// MemoryComponent manages conversation history
type MemoryComponent struct {
	BaseComponent
	memory llm.Memory
	role   string
	mu     sync.RWMutex
}

// NewMemoryComponent creates a new memory component
func NewMemoryComponent(id string, memory llm.Memory, role string) *MemoryComponent {
	return &MemoryComponent{
		BaseComponent: NewBaseComponent(id, "Memory", []string{"message_in"}, []string{"context_out"}),
		memory:        memory,
		role:          role,
	}
}

// Process stores messages and outputs conversation context
func (mc *MemoryComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["message_in"]
	out := outputs["context_out"]

	for {
		select {
		case <-ctx.Done():
			return nil
		case pkt, ok := <-in:
			if !ok {
				return nil
			}

			if pkt.Type != PacketTypeText {
				continue
			}

			textPkt, ok := pkt.Data.(*media.TextPacket)
			if !ok {
				continue
			}

			mc.mu.RLock()
			memory := mc.memory
			role := mc.role
			mc.mu.RUnlock()

			// Add message to memory
			msg := llm.Message{
				Role:    role,
				Content: textPkt.Text,
			}

			if err := memory.AddMessage(ctx, msg); err != nil {
				return fmt.Errorf("failed to add message to memory: %w", err)
			}

			// Get context
			context := memory.GetContext()

			// Send context
			select {
			case <-ctx.Done():
				return nil
			case out <- NewPacket(PacketTypeText, &media.TextPacket{
				Text:  context,
				IsEnd: true,
			}):
			}
		}
	}
}

// ChainComponent executes a chain of steps
type ChainComponent struct {
	BaseComponent
	chain *llm.Chain
	mu    sync.RWMutex
}

// NewChainComponent creates a new chain component
func NewChainComponent(id string, chain *llm.Chain) *ChainComponent {
	return &ChainComponent{
		BaseComponent: NewBaseComponent(id, "Chain", []string{"input"}, []string{"output"}),
		chain:         chain,
	}
}

// Process executes the chain
func (cc *ChainComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["input"]
	out := outputs["output"]

	for {
		select {
		case <-ctx.Done():
			return nil
		case pkt, ok := <-in:
			if !ok {
				return nil
			}

			// Extract input data
			inputData := make(map[string]interface{})
			if pkt.Type == PacketTypeText {
				if textPkt, ok := pkt.Data.(*media.TextPacket); ok {
					inputData["text"] = textPkt.Text
				}
			} else if pkt.Type == PacketTypeGeneric {
				if genericData, ok := pkt.Data.(map[string]interface{}); ok {
					inputData = genericData
				}
			}

			cc.mu.RLock()
			chain := cc.chain
			cc.mu.RUnlock()

			// Execute chain
			result, err := chain.Execute(ctx, inputData)
			if err != nil {
				return fmt.Errorf("chain execution failed: %w", err)
			}

			// Send result
			select {
			case <-ctx.Done():
				return nil
			case out <- NewPacket(PacketTypeGeneric, result):
			}
		}
	}
}

// AgentComponent executes an agent with tools
type AgentComponent struct {
	BaseComponent
	executor *llm.AgentExecutor
	mu       sync.RWMutex
}

// NewAgentComponent creates a new agent component
func NewAgentComponent(id string, executor *llm.AgentExecutor) *AgentComponent {
	return &AgentComponent{
		BaseComponent: NewBaseComponent(id, "Agent", []string{"query_in"}, []string{"result_out"}),
		executor:      executor,
	}
}

// Process executes the agent
func (ac *AgentComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["query_in"]
	out := outputs["result_out"]

	for {
		select {
		case <-ctx.Done():
			return nil
		case pkt, ok := <-in:
			if !ok {
				return nil
			}

			if pkt.Type != PacketTypeText {
				continue
			}

			textPkt, ok := pkt.Data.(*media.TextPacket)
			if !ok {
				continue
			}

			ac.mu.RLock()
			executor := ac.executor
			ac.mu.RUnlock()

			// Execute agent
			result, err := executor.Execute(ctx, textPkt.Text)
			if err != nil {
				return fmt.Errorf("agent execution failed: %w", err)
			}

			// Send result
			select {
			case <-ctx.Done():
				return nil
			case out <- NewPacket(PacketTypeText, &media.TextPacket{
				Text:  result,
				IsEnd: true,
			}):
			}
		}
	}
}

// SubgraphComponent executes a subgraph
type SubgraphComponent struct {
	BaseComponent
	subgraph *Graph
	mu       sync.RWMutex
}

// NewSubgraphComponent creates a new subgraph component
func NewSubgraphComponent(id string, subgraph *Graph) *SubgraphComponent {
	return &SubgraphComponent{
		BaseComponent: NewBaseComponent(id, "Subgraph", []string{"input"}, []string{"output"}),
		subgraph:      subgraph,
	}
}

// Process executes the subgraph
func (sc *SubgraphComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["input"]
	out := outputs["output"]

	for {
		select {
		case <-ctx.Done():
			return nil
		case pkt, ok := <-in:
			if !ok {
				return nil
			}

			sc.mu.RLock()
			subgraph := sc.subgraph
			sc.mu.RUnlock()

			// Execute subgraph
			if err := subgraph.Run(ctx); err != nil {
				return fmt.Errorf("subgraph execution failed: %w", err)
			}

			// Forward packet
			select {
			case <-ctx.Done():
				return nil
			case out <- pkt:
			}
		}
	}
}
