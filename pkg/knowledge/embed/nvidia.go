package embed

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// nvidiaEmbedder uses NVIDIA NIM-compatible embedding HTTP APIs.
type nvidiaEmbedder struct {
	inner *openAIEmbedder
}

// NewNVIDIA builds an NVIDIA embedding client (same wire format as OpenAI embeddings).
func NewNVIDIA(cfg NVIDIAConfig) (Embedder, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("embed: BaseURL is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("embed: APIKey is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("embed: Model is required")
	}
	inner, err := NewOpenAI(OpenAIConfig{
		BaseURL:        cfg.BaseURL,
		APIKey:         cfg.APIKey,
		Model:          cfg.Model,
		InputKey:       cfg.InputKey,
		EmbeddingsPath: cfg.EmbeddingsPath,
		HTTPClient:     cfg.HTTPClient,
	})
	if err != nil {
		return nil, err
	}
	o := inner.(*openAIEmbedder)
	o.provider = ProviderNVIDIA
	if cfg.HTTPClient == nil {
		o.client = defaultHTTPClient(nil, 30*time.Second)
	}
	return &nvidiaEmbedder{inner: o}, nil
}

func (c *nvidiaEmbedder) Provider() Provider { return ProviderNVIDIA }
func (c *nvidiaEmbedder) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	if c == nil || c.inner == nil {
		return nil, fmt.Errorf("embed: nil nvidia client")
	}
	return c.inner.Embed(ctx, inputs)
}

// NVIDIAEmbedClient is a backward-compatible alias type for explicit struct construction.
type NVIDIAEmbedClient = nvidiaEmbedder
