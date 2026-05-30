package avflow

import (
	"context"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm"
	"github.com/LingByte/LingVoice/pkg/media"
)

func TestChatModelComponent_Create(t *testing.T) {
	model := llm.NewMockLLMModel(llm.ModelConfig{Name: "mock"})
	comp := NewChatModelComponent("chat", model)

	if comp.ID() != "chat" {
		t.Errorf("expected ID 'chat', got %s", comp.ID())
	}

	if comp.Type() != "ChatModel" {
		t.Errorf("expected type 'ChatModel', got %s", comp.Type())
	}

	inputs := comp.Inputs()
	if len(inputs) != 1 || inputs[0] != "text_in" {
		t.Errorf("expected input 'text_in', got %v", inputs)
	}

	outputs := comp.Outputs()
	if len(outputs) != 1 || outputs[0] != "text_out" {
		t.Errorf("expected output 'text_out', got %v", outputs)
	}
}

func TestChatModelComponent_Process(t *testing.T) {
	model := llm.NewMockLLMModel(llm.ModelConfig{Name: "mock"})
	comp := NewChatModelComponent("chat", model)

	inChan := make(chan *Packet, 1)
	outChan := make(chan *Packet, 1)

	inputs := map[string]<-chan *Packet{"text_in": inChan}
	outputs := map[string]chan<- *Packet{"text_out": outChan}

	// Send input
	inChan <- NewPacket(PacketTypeText, &media.TextPacket{
		Text:      "Hello",
		IsPartial: false,
	})
	close(inChan)

	// Process
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go comp.Process(ctx, inputs, outputs)

	// Receive output
	select {
	case pkt := <-outChan:
		if pkt.Type != PacketTypeText {
			t.Errorf("expected PacketTypeText, got %v", pkt.Type)
		}
		textPkt, ok := pkt.Data.(*media.TextPacket)
		if !ok {
			t.Fatal("expected TextPacket")
		}
		if textPkt.Text == "" {
			t.Error("expected non-empty response")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for output")
	}
}

func TestRAGComponent_Create(t *testing.T) {
	retriever := llm.NewSimpleRetriever()
	llmModel := llm.NewMockLLMModel(llm.ModelConfig{Name: "mock"})
	ragChain := llm.NewRAGChain(retriever, llmModel, 1)
	comp := NewRAGComponent("rag", ragChain)

	if comp.ID() != "rag" {
		t.Errorf("expected ID 'rag', got %s", comp.ID())
	}

	if comp.Type() != "RAG" {
		t.Errorf("expected type 'RAG', got %s", comp.Type())
	}
}

func TestRAGComponent_Process(t *testing.T) {
	retriever := llm.NewSimpleRetriever()
	retriever.Add(context.Background(), llm.Document{
		ID:      "doc1",
		Content: "Go is a programming language",
	})

	llmModel := llm.NewMockLLMModel(llm.ModelConfig{Name: "mock"})
	ragChain := llm.NewRAGChain(retriever, llmModel, 1)
	comp := NewRAGComponent("rag", ragChain)

	inChan := make(chan *Packet, 1)
	outChan := make(chan *Packet, 1)

	inputs := map[string]<-chan *Packet{"query_in": inChan}
	outputs := map[string]chan<- *Packet{"answer_out": outChan}

	inChan <- NewPacket(PacketTypeText, &media.TextPacket{
		Text: "What is Go?",
	})
	close(inChan)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go comp.Process(ctx, inputs, outputs)

	select {
	case pkt := <-outChan:
		if pkt.Type != PacketTypeText {
			t.Errorf("expected PacketTypeText, got %v", pkt.Type)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for output")
	}
}

func TestPromptComponent_Create(t *testing.T) {
	comp := NewPromptComponent("prompt", "Question: %s\nAnswer:")

	if comp.ID() != "prompt" {
		t.Errorf("expected ID 'prompt', got %s", comp.ID())
	}

	if comp.Type() != "Prompt" {
		t.Errorf("expected type 'Prompt', got %s", comp.Type())
	}
}

func TestPromptComponent_Process(t *testing.T) {
	comp := NewPromptComponent("prompt", "Q: %s\nA:")

	inChan := make(chan *Packet, 1)
	outChan := make(chan *Packet, 1)

	inputs := map[string]<-chan *Packet{"input": inChan}
	outputs := map[string]chan<- *Packet{"prompt_out": outChan}

	inChan <- NewPacket(PacketTypeText, &media.TextPacket{
		Text: "What is 2+2?",
	})
	close(inChan)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go comp.Process(ctx, inputs, outputs)

	select {
	case pkt := <-outChan:
		if pkt.Type != PacketTypeText {
			t.Errorf("expected PacketTypeText, got %v", pkt.Type)
		}
		textPkt := pkt.Data.(*media.TextPacket)
		if textPkt.Text != "Q: What is 2+2?\nA:" {
			t.Errorf("expected formatted prompt, got %s", textPkt.Text)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for output")
	}
}

func TestMemoryComponent_Create(t *testing.T) {
	memory := llm.NewBufferMemory(10)
	comp := NewMemoryComponent("memory", memory, "user")

	if comp.ID() != "memory" {
		t.Errorf("expected ID 'memory', got %s", comp.ID())
	}

	if comp.Type() != "Memory" {
		t.Errorf("expected type 'Memory', got %s", comp.Type())
	}
}

func TestMemoryComponent_Process(t *testing.T) {
	memory := llm.NewBufferMemory(10)
	comp := NewMemoryComponent("memory", memory, "user")

	inChan := make(chan *Packet, 1)
	outChan := make(chan *Packet, 1)

	inputs := map[string]<-chan *Packet{"message_in": inChan}
	outputs := map[string]chan<- *Packet{"context_out": outChan}

	inChan <- NewPacket(PacketTypeText, &media.TextPacket{
		Text: "Hello",
	})
	close(inChan)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go comp.Process(ctx, inputs, outputs)

	select {
	case pkt := <-outChan:
		if pkt.Type != PacketTypeText {
			t.Errorf("expected PacketTypeText, got %v", pkt.Type)
		}
		textPkt := pkt.Data.(*media.TextPacket)
		if textPkt.Text == "" {
			t.Error("expected non-empty context")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for output")
	}
}

func TestChainComponent_Create(t *testing.T) {
	chain := llm.NewChain("test-chain")
	comp := NewChainComponent("chain", chain)

	if comp.ID() != "chain" {
		t.Errorf("expected ID 'chain', got %s", comp.ID())
	}

	if comp.Type() != "Chain" {
		t.Errorf("expected type 'Chain', got %s", comp.Type())
	}
}

func TestChainComponent_Process(t *testing.T) {
	chain := llm.NewChain("test-chain")
	step := llm.NewSimpleStep(
		"step1",
		[]string{"text"},
		[]string{"result"},
		func(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
			text := input["text"].(string)
			return map[string]interface{}{"result": text + "-processed"}, nil
		},
	)
	chain.AddStep(step)

	comp := NewChainComponent("chain", chain)

	inChan := make(chan *Packet, 1)
	outChan := make(chan *Packet, 1)

	inputs := map[string]<-chan *Packet{"input": inChan}
	outputs := map[string]chan<- *Packet{"output": outChan}

	inChan <- NewPacket(PacketTypeText, &media.TextPacket{
		Text: "test",
	})
	close(inChan)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go comp.Process(ctx, inputs, outputs)

	select {
	case pkt := <-outChan:
		if pkt.Type != PacketTypeGeneric {
			t.Errorf("expected PacketTypeGeneric, got %v", pkt.Type)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for output")
	}
}

func TestAgentComponent_Create(t *testing.T) {
	llmModel := llm.NewMockLLMModel(llm.ModelConfig{Name: "mock"})
	registry := llm.NewToolRegistry()
	agent := llm.NewAgent("test-agent", llmModel, registry, 5)
	executor := llm.NewAgentExecutor(agent)
	comp := NewAgentComponent("agent", executor)

	if comp.ID() != "agent" {
		t.Errorf("expected ID 'agent', got %s", comp.ID())
	}

	if comp.Type() != "Agent" {
		t.Errorf("expected type 'Agent', got %s", comp.Type())
	}
}

func TestAgentComponent_Process(t *testing.T) {
	llmModel := llm.NewMockLLMModel(llm.ModelConfig{Name: "mock"})
	registry := llm.NewToolRegistry()
	agent := llm.NewAgent("test-agent", llmModel, registry, 5)
	executor := llm.NewAgentExecutor(agent)
	comp := NewAgentComponent("agent", executor)

	inChan := make(chan *Packet, 1)
	outChan := make(chan *Packet, 1)

	inputs := map[string]<-chan *Packet{"query_in": inChan}
	outputs := map[string]chan<- *Packet{"result_out": outChan}

	inChan <- NewPacket(PacketTypeText, &media.TextPacket{
		Text: "What is 2+2?",
	})
	close(inChan)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go comp.Process(ctx, inputs, outputs)

	select {
	case pkt := <-outChan:
		if pkt.Type != PacketTypeText {
			t.Errorf("expected PacketTypeText, got %v", pkt.Type)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for output")
	}
}

func TestSubgraphComponent_Create(t *testing.T) {
	subgraph := NewGraph("sub")
	comp := NewSubgraphComponent("subgraph", subgraph)

	if comp.ID() != "subgraph" {
		t.Errorf("expected ID 'subgraph', got %s", comp.ID())
	}

	if comp.Type() != "Subgraph" {
		t.Errorf("expected type 'Subgraph', got %s", comp.Type())
	}
}

func TestSubgraphComponent_Process(t *testing.T) {
	subgraph := NewGraph("sub")
	comp := NewSubgraphComponent("subgraph", subgraph)

	inChan := make(chan *Packet, 1)
	outChan := make(chan *Packet, 1)

	inputs := map[string]<-chan *Packet{"input": inChan}
	outputs := map[string]chan<- *Packet{"output": outChan}

	pkt := NewPacket(PacketTypeText, &media.TextPacket{Text: "test"})
	inChan <- pkt
	close(inChan)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go comp.Process(ctx, inputs, outputs)

	select {
	case outPkt := <-outChan:
		if outPkt.Type != PacketTypeText {
			t.Errorf("expected PacketTypeText, got %v", outPkt.Type)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for output")
	}
}

func TestLLMComponentsIntegration(t *testing.T) {
	// Create a complete LLM pipeline
	memory := llm.NewBufferMemory(10)
	retriever := llm.NewSimpleRetriever()
	retriever.Add(context.Background(), llm.Document{
		ID:      "doc1",
		Content: "Go is a programming language",
	})

	llmModel := llm.NewMockLLMModel(llm.ModelConfig{Name: "mock"})
	ragChain := llm.NewRAGChain(retriever, llmModel, 1)

	// Build graph
	g := NewGraph("llm-pipeline")

	memComp := NewMemoryComponent("memory", memory, "user")
	ragComp := NewRAGComponent("rag", ragChain)
	chatComp := NewChatModelComponent("chat", llmModel)

	g.AddComponent(memComp)
	g.AddComponent(ragComp)
	g.AddComponent(chatComp)

	g.Connect("memory", "context_out", "rag", "query_in")
	g.Connect("rag", "answer_out", "chat", "text_in")

	if len(g.components) != 3 {
		t.Errorf("expected 3 components, got %d", len(g.components))
	}

	if len(g.edges) != 2 {
		t.Errorf("expected 2 edges, got %d", len(g.edges))
	}
}
