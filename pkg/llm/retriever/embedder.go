package retriever

import (
	"context"
	"fmt"
	"math"
)

// Embedder produces dense vectors for text (Eino embedding subset).
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
}

// EmbedFunc adapts a function to Embedder.
type EmbedFunc func(ctx context.Context, texts []string) ([][]float32, error)

// FuncEmbedder implements Embedder from callbacks (tests and lightweight wrappers).
type FuncEmbedder struct {
	Dim   int
	Fn    EmbedFunc
}

func (f *FuncEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if f == nil || f.Fn == nil {
		return nil, fmt.Errorf("retriever: nil embed func")
	}
	return f.Fn(ctx, texts)
}

func (f *FuncEmbedder) Dimensions() int {
	if f == nil || f.Dim <= 0 {
		return 128
	}
	return f.Dim
}

// CosineSimilarity returns cosine similarity in [0,1] for equal-length vectors.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
