package llm

import (
	"context"
	"testing"
)

func TestSimpleRetriever_Add(t *testing.T) {
	retriever := NewSimpleRetriever()
	doc := Document{
		ID:      "doc1",
		Content: "test content",
	}

	err := retriever.Add(context.Background(), doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(retriever.documents) != 1 {
		t.Errorf("expected 1 document, got %d", len(retriever.documents))
	}
}

func TestSimpleRetriever_Add_EmptyID(t *testing.T) {
	retriever := NewSimpleRetriever()
	doc := Document{
		ID:      "",
		Content: "test content",
	}

	err := retriever.Add(context.Background(), doc)
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestSimpleRetriever_Retrieve(t *testing.T) {
	retriever := NewSimpleRetriever()
	retriever.Add(context.Background(), Document{
		ID:      "doc1",
		Content: "hello world",
	})
	retriever.Add(context.Background(), Document{
		ID:      "doc2",
		Content: "foo bar",
	})

	results, err := retriever.Retrieve(context.Background(), "hello", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}

	if results[0].ID != "doc1" {
		t.Errorf("expected doc1, got %s", results[0].ID)
	}
}

func TestSimpleRetriever_Retrieve_TopK(t *testing.T) {
	retriever := NewSimpleRetriever()
	for i := 0; i < 5; i++ {
		retriever.Add(context.Background(), Document{
			ID:      "doc" + string(rune(i)),
			Content: "test content",
		})
	}

	results, err := retriever.Retrieve(context.Background(), "test", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) > 2 {
		t.Errorf("expected at most 2 results, got %d", len(results))
	}
}

func TestSimpleRetriever_Delete(t *testing.T) {
	retriever := NewSimpleRetriever()
	retriever.Add(context.Background(), Document{
		ID:      "doc1",
		Content: "test",
	})

	err := retriever.Delete(context.Background(), "doc1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(retriever.documents) != 0 {
		t.Errorf("expected 0 documents, got %d", len(retriever.documents))
	}
}

func TestSimpleRetriever_Clear(t *testing.T) {
	retriever := NewSimpleRetriever()
	for i := 0; i < 3; i++ {
		retriever.Add(context.Background(), Document{
			ID:      "doc" + string(rune(i)),
			Content: "test",
		})
	}

	err := retriever.Clear(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(retriever.documents) != 0 {
		t.Errorf("expected 0 documents, got %d", len(retriever.documents))
	}
}

func TestRAGChain_Execute(t *testing.T) {
	retriever := NewSimpleRetriever()
	retriever.Add(context.Background(), Document{
		ID:      "doc1",
		Content: "The capital of France is Paris",
	})

	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	ragChain := NewRAGChain(retriever, llm, 1)

	result, err := ragChain.Execute(context.Background(), "What is the capital of France?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestRAGStep_Execute(t *testing.T) {
	retriever := NewSimpleRetriever()
	retriever.Add(context.Background(), Document{
		ID:      "doc1",
		Content: "test document",
	})

	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	ragChain := NewRAGChain(retriever, llm, 1)
	ragStep := NewRAGStep("rag-step", ragChain, "query", "answer")

	result, err := ragStep.Execute(context.Background(), map[string]interface{}{
		"query": "test query",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, exists := result["answer"]; !exists {
		t.Error("expected 'answer' key in result")
	}
}

func TestRAGStep_Execute_MissingQuery(t *testing.T) {
	retriever := NewSimpleRetriever()
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	ragChain := NewRAGChain(retriever, llm, 1)
	ragStep := NewRAGStep("rag-step", ragChain, "query", "answer")

	_, err := ragStep.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

func TestRAGStep_GetInputKeys(t *testing.T) {
	retriever := NewSimpleRetriever()
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	ragChain := NewRAGChain(retriever, llm, 1)
	ragStep := NewRAGStep("rag-step", ragChain, "query", "answer")

	keys := ragStep.GetInputKeys()
	if len(keys) != 1 || keys[0] != "query" {
		t.Errorf("expected ['query'], got %v", keys)
	}
}

func TestRAGStep_GetOutputKeys(t *testing.T) {
	retriever := NewSimpleRetriever()
	llm := NewMockLLMModel(ModelConfig{Name: "mock"})
	ragChain := NewRAGChain(retriever, llm, 1)
	ragStep := NewRAGStep("rag-step", ragChain, "query", "answer")

	keys := ragStep.GetOutputKeys()
	if len(keys) != 1 || keys[0] != "answer" {
		t.Errorf("expected ['answer'], got %v", keys)
	}
}
