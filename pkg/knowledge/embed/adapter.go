package embed

import (
	"context"
	"fmt"
)

// FromFloat32Client adapts a float32 embedding client to Embedder.
func FromFloat32Client(provider Provider, client Float32Client) Embedder {
	if provider == "" {
		provider = ProviderOpenAI
	}
	return &float32Adapter{provider: provider, client: client}
}

type float32Adapter struct {
	provider Provider
	client   Float32Client
}

func (a *float32Adapter) Provider() Provider { return a.provider }

func (a *float32Adapter) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	if a == nil || a.client == nil {
		return nil, fmt.Errorf("embed: nil float32 client")
	}
	vecs, err := a.client.Embed(ctx, inputs)
	if err != nil {
		return nil, err
	}
	out := make([][]float64, len(vecs))
	for i, v := range vecs {
		out[i] = float32To64(v)
	}
	return out, nil
}

func float32To64(in []float32) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}

func float64To32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}

// ToRetrieverEmbedder adapts knowledge/embed.Embedder to pkg/llm/retriever.Embedder.
func ToRetrieverEmbedder(e Embedder, dim int) *RetrieverBridge {
	return &RetrieverBridge{Inner: e, Dim: dim}
}

// RetrieverBridge implements retriever.Embedder from embed.Embedder.
type RetrieverBridge struct {
	Inner Embedder
	Dim   int
}

func (b *RetrieverBridge) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if b == nil || b.Inner == nil {
		return nil, fmt.Errorf("embed: nil bridge")
	}
	vecs, err := b.Inner.Embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	out := make([][]float32, len(vecs))
	for i, v := range vecs {
		out[i] = float64To32(v)
	}
	return out, nil
}

func (b *RetrieverBridge) Dimensions() int {
	if b != nil && b.Dim > 0 {
		return b.Dim
	}
	return 0
}
