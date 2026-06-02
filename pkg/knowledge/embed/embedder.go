package embed

import "context"

// Provider identifies an embedding backend implementation.
type Provider string

const (
	ProviderOpenAI Provider = "openai"
	ProviderNVIDIA Provider = "nvidia"
	ProviderFunc   Provider = "func"
)

// Embedder produces dense vectors for retrieval indexing and query.
type Embedder interface {
	Provider() Provider
	Embed(ctx context.Context, inputs []string) ([][]float64, error)
}

// Float32Client is implemented by OpenAI-compatible embedding APIs returning float32 vectors.
type Float32Client interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}
