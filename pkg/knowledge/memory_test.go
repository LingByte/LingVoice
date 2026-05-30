package knowledge

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/knowledge/embed"
)

type hashEmbedder struct{}

func (hashEmbedder) Provider() embed.Provider { return embed.ProviderFunc }

func (hashEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, t := range texts {
		out[i] = hashVec(t, 16)
	}
	return out, nil
}

func hashVec(text string, dim int) []float64 {
	vec := make([]float64, dim)
	for i, r := range text {
		vec[i%dim] += float64(int(r)%97) / 97
	}
	return vec
}

func TestMemoryHandler_VectorQuery(t *testing.T) {
	h, err := NewMemoryHandler(hashEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const ns = "demo"
	if err := h.CreateNamespace(ctx, ns); err != nil {
		t.Fatal(err)
	}
	if err := h.Upsert(ctx, []Record{
		{ID: "a", Content: "pregel mailbox streaming"},
		{ID: "b", Content: "voice asr tts layer"},
	}, &UpsertOptions{Namespace: ns}); err != nil {
		t.Fatal(err)
	}
	res, err := h.Query(ctx, "pregel mailbox", &QueryOptions{Namespace: ns, TopK: 1})
	if err != nil || len(res) != 1 || res[0].Record.ID != "a" {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestNewHandlerMemoryProvider(t *testing.T) {
	h, err := NewHandler(HandlerConfig{
		Provider: ProviderMemory,
		Embedder: hashEmbedder{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.Provider() != ProviderMemory {
		t.Fatalf("provider=%q", h.Provider())
	}
}
