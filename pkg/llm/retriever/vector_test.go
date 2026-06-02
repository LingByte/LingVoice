package retriever_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/retriever"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestVectorRetriever(t *testing.T) {
	embedder := &retriever.FuncEmbedder{
		Dim: 4,
		Fn: func(_ context.Context, texts []string) ([][]float32, error) {
			out := make([][]float32, len(texts))
			for i, t := range texts {
				v := make([]float32, 4)
				for j, c := range t {
					v[j%4] += float32(c)
				}
				out[i] = v
			}
			return out, nil
		},
	}
	vr, err := retriever.NewVectorRetriever(embedder, &retriever.InMemoryVectorStore{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	docs := []*schema.Document{
		{ID: "a", Content: "pregel mailbox streaming"},
		{ID: "b", Content: "a2a mtls authentication"},
	}
	if err := vr.Index(context.Background(), docs); err != nil {
		t.Fatal(err)
	}
	got, err := vr.Retrieve(context.Background(), "pregel streaming", 2)
	if err != nil || len(got) == 0 || got[0].ID != "a" {
		t.Fatalf("err=%v got=%v", err, got)
	}
}

func TestCosineSimilarity(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	if retriever.CosineSimilarity(a, b) != 1 {
		t.Fatal("expected 1")
	}
}
