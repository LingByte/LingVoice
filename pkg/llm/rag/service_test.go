package rag_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/knowledge"
	"github.com/LingByte/LingVoice/pkg/knowledge/retrieve"
	"github.com/LingByte/LingVoice/pkg/llm/rag"
	"github.com/LingByte/LingVoice/pkg/knowledge/embed"
)

type hashEmbedder struct{}

func (hashEmbedder) Provider() embed.Provider { return embed.ProviderFunc }

func (hashEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, t := range texts {
		vec := make([]float64, 8)
		for j, r := range t {
			vec[j%8] += float64(int(r)%97) / 97
		}
		out[i] = vec
	}
	return out, nil
}

func TestNewIndexedServiceChain(t *testing.T) {
	handler, err := knowledge.NewMemoryHandler(hashEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	chain, svc, err := rag.NewIndexedServiceChain(context.Background(),
		[]knowledge.DocumentInput{
			{ID: "x", Content: "mailbox channel", Strategy: knowledge.ChunkStrategyStructured},
		},
		knowledge.ServiceConfig{
			Handler: handler,
			Retrieve: retrieve.Config{
				Strategy: retrieve.StrategyVector,
				TopK:     1,
			},
			Namespace: "demo",
		},
		&knowledge.IndexOptions{Namespace: "demo", Chunk: &knowledge.ChunkOptions{MaxChars: 80}},
		"ctx",
	)
	if err != nil || chain == nil || svc == nil {
		t.Fatalf("chain=%v svc=%v err=%v", chain, svc, err)
	}
}
