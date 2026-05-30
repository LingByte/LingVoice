package openai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/internal/httputil"
)

const defaultEmbeddingModel = "text-embedding-3-small"

// EmbeddingConfig configures OpenAI-compatible embeddings API.
type EmbeddingConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
}

// EmbeddingModel calls /embeddings on OpenAI-compatible endpoints.
type EmbeddingModel struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// NewEmbeddingModel creates an embedding client.
func NewEmbeddingModel(cfg EmbeddingConfig) (*EmbeddingModel, error) {
	if cfg.APIKey == "" {
		return nil, errMissingAPIKey
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = defaultBaseURL
	}
	model := cfg.Model
	if model == "" {
		model = defaultEmbeddingModel
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &EmbeddingModel{
		apiKey:  cfg.APIKey,
		baseURL: base,
		model:   model,
		client:  client,
	}, nil
}

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

func (m *EmbeddingModel) headers() map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + m.apiKey,
	}
}

// Embed returns vectors for input texts.
func (m *EmbeddingModel) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if m == nil {
		return nil, fmt.Errorf("openai: nil embedding model")
	}
	if len(texts) == 0 {
		return nil, nil
	}
	req := embeddingRequest{Model: m.model, Input: texts}
	var resp embeddingResponse
	if err := httputil.DoJSON(ctx, m.client, "POST", m.baseURL+"/embeddings", m.headers(), req, &resp); err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for _, d := range resp.Data {
		if d.Index >= 0 && d.Index < len(out) {
			out[d.Index] = d.Embedding
		}
	}
	return out, nil
}

// Dimensions returns a nominal dimension hint (actual dims come from API).
func (m *EmbeddingModel) Dimensions() int {
	return 1536
}
