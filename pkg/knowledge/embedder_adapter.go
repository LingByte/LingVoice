package knowledge

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/knowledge/embed"
	"github.com/LingByte/LingVoice/pkg/llm/retriever"
)

// NewFloat32EmbedAdapter adapts a float32 embedding API client to Embedder.
func NewFloat32EmbedAdapter(client embed.Float32Client) Embedder {
	return embed.FromFloat32Client(embed.ProviderOpenAI, client)
}

// ToRetrieverEmbedder bridges knowledge/embed to pkg/llm/retriever.Embedder.
func ToRetrieverEmbedder(e Embedder, dim int) *embed.RetrieverBridge {
	return embed.ToRetrieverEmbedder(e, dim)
}

// RetrieverEmbedder adapts retriever.Embedder to knowledge.Embedder.
type RetrieverEmbedder struct {
	Inner retriever.Embedder
}

func (e *RetrieverEmbedder) Provider() embed.Provider { return embed.ProviderFunc }

func (e *RetrieverEmbedder) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	if e == nil || e.Inner == nil {
		return nil, fmt.Errorf("knowledge: nil embedder")
	}
	vecs, err := e.Inner.Embed(ctx, inputs)
	if err != nil {
		return nil, err
	}
	out := make([][]float64, len(vecs))
	for i, v := range vecs {
		out[i] = make([]float64, len(v))
		for j, x := range v {
			out[i][j] = float64(x)
		}
	}
	return out, nil
}
