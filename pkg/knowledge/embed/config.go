package embed

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// OpenAIConfig configures OpenAI-compatible /embeddings HTTP APIs.
type OpenAIConfig struct {
	BaseURL        string
	APIKey         string
	Model          string
	EmbeddingsPath string
	InputKey       string
	HTTPClient     *http.Client
}

// NVIDIAConfig configures NVIDIA NIM / compatible embedding endpoints.
type NVIDIAConfig struct {
	BaseURL        string
	APIKey         string
	Model          string
	InputKey       string
	EmbeddingsPath string
	HTTPClient     *http.Client
}

// FuncConfig adapts a function to Embedder (tests and custom backends).
type FuncConfig struct {
	Provider Provider
	Fn       func(ctx context.Context, inputs []string) ([][]float64, error)
}

// Config selects an embedding strategy via explicit parameters (no env lookup).
type Config struct {
	Provider Provider
	OpenAI   OpenAIConfig
	NVIDIA   NVIDIAConfig
	Func     FuncConfig
	// Client adapts an existing float32 client as OpenAI provider.
	Client Float32Client
}

// New builds an Embedder for the configured provider.
func New(cfg Config) (Embedder, error) {
	if cfg.Client != nil {
		return FromFloat32Client(ProviderOpenAI, cfg.Client), nil
	}
	switch cfg.Provider {
	case ProviderOpenAI, "":
		if cfg.OpenAI.APIKey == "" && cfg.OpenAI.BaseURL == "" {
			return nil, fmt.Errorf("embed: openai config required")
		}
		return NewOpenAI(cfg.OpenAI)
	case ProviderNVIDIA:
		return NewNVIDIA(cfg.NVIDIA)
	case ProviderFunc:
		if cfg.Func.Fn == nil {
			return nil, fmt.Errorf("embed: func embedder required")
		}
		p := cfg.Func.Provider
		if p == "" {
			p = ProviderFunc
		}
		return &funcEmbedder{provider: p, fn: cfg.Func.Fn}, nil
	default:
		return nil, fmt.Errorf("embed: unsupported provider %q", cfg.Provider)
	}
}

type funcEmbedder struct {
	provider Provider
	fn       func(ctx context.Context, inputs []string) ([][]float64, error)
}

func (f *funcEmbedder) Provider() Provider { return f.provider }
func (f *funcEmbedder) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	return f.fn(ctx, inputs)
}

func defaultHTTPClient(c *http.Client, timeout time.Duration) *http.Client {
	if c != nil {
		return c
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{Timeout: timeout}
}
