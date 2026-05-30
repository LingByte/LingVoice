package knowledge

import (
	"context"
	"testing"
	"time"
)

func TestNewQdrantHandler_RequiresBaseURL(t *testing.T) {
	if _, err := NewQdrantHandler(QdrantConfig{}, nil); err != ErrBaseURL {
		t.Fatalf("err=%v", err)
	}
}

func TestNewMilvusHandler_RequiresAddress(t *testing.T) {
	if _, err := NewMilvusHandler(MilvusConfig{}, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewHandler_ProviderSwitch(t *testing.T) {
	qh, err := NewHandler(HandlerConfig{
		Provider: ProviderQdrant,
		Qdrant:   QdrantConfig{BaseURL: "http://127.0.0.1:6333", Timeout: time.Second},
	})
	if err != nil || qh.Provider() != ProviderQdrant {
		t.Fatalf("qdrant err=%v provider=%q", err, qh.Provider())
	}
	if _, err := NewHandler(HandlerConfig{Provider: "unknown"}); err == nil {
		t.Fatal("expected unsupported provider error")
	}
}

func TestNewIndexerFromConfig_WithLLM(t *testing.T) {
	store := &captureHandler{}
	idx, err := NewIndexerFromConfig(IndexerConfig{
		Handler: store,
		LLM:     stubCompleter{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if idx == nil || idx.Chunker == nil {
		t.Fatal("expected indexer")
	}
}

type stubCompleter struct{}

func (stubCompleter) Complete(context.Context, string, string) (string, error) {
	return `{"chunks":[{"title":"A","text":"chunk"}]}`, nil
}

func TestFloat32EmbedAdapter(t *testing.T) {
	adapter := NewFloat32EmbedAdapter(stubEmbedClient{})
	vecs, err := adapter.Embed(t.Context(), []string{"hello"})
	if err != nil || len(vecs) != 1 || len(vecs[0]) != 2 {
		t.Fatalf("vecs=%v err=%v", vecs, err)
	}
}

type stubEmbedClient struct{}

func (stubEmbedClient) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0.1, 0.2}
	}
	return out, nil
}
