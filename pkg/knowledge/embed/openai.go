package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	maxEmbedInputChars  = 12000
	maxEmbedBatchInputs = 16
)

// openAIEmbedder calls OpenAI-compatible /embeddings endpoints.
type openAIEmbedder struct {
	provider       Provider
	baseURL        string
	apiKey         string
	model          string
	inputKey       string
	embeddingsPath string
	client         *http.Client
}

func NewOpenAI(cfg OpenAIConfig) (Embedder, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("embed: APIKey is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("embed: BaseURL is required")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "text-embedding-3-small"
	}
	return &openAIEmbedder{
		provider:       ProviderOpenAI,
		baseURL:        strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:         cfg.APIKey,
		model:          model,
		inputKey:       defaultInputKey(cfg.InputKey),
		embeddingsPath: strings.TrimSpace(cfg.EmbeddingsPath),
		client:         defaultHTTPClient(cfg.HTTPClient, 60*time.Second),
	}, nil
}

func (c *openAIEmbedder) Provider() Provider { return c.provider }

func (c *openAIEmbedder) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	endpoint := c.endpoint()
	return c.postBatches(ctx, endpoint, inputs, func(body []byte) ([][]float64, error) {
		var parsed struct {
			Data []struct {
				Embedding []float64 `json:"embedding"`
				Index     int       `json:"index"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, err
		}
		if len(parsed.Data) == 0 {
			return nil, errors.New("no embeddings returned")
		}
		out := make([][]float64, len(inputs))
		for _, d := range parsed.Data {
			if d.Index >= 0 && d.Index < len(out) {
				out[d.Index] = d.Embedding
			}
		}
		for i, v := range out {
			if len(v) == 0 {
				return nil, fmt.Errorf("missing embedding at index %d", i)
			}
		}
		return out, nil
	})
}

func (c *openAIEmbedder) endpoint() string {
	if c.embeddingsPath != "" {
		p := strings.TrimLeft(c.embeddingsPath, "/")
		return c.baseURL + "/" + p
	}
	if strings.HasSuffix(c.baseURL, "/embeddings") {
		return c.baseURL
	}
	return c.baseURL + "/embeddings"
}

func defaultInputKey(k string) string {
	if strings.TrimSpace(k) == "" {
		return "input"
	}
	return strings.TrimSpace(k)
}

func sanitizeInputs(inputs []string) []string {
	out := make([]string, 0, len(inputs))
	for _, in := range inputs {
		in = strings.TrimSpace(in)
		if in == "" {
			in = "-"
		}
		if len(in) > maxEmbedInputChars {
			in = in[:maxEmbedInputChars]
		}
		out = append(out, in)
	}
	return out
}

func (c *openAIEmbedder) postBatches(ctx context.Context, endpoint string, inputs []string, parse func([]byte) ([][]float64, error)) ([][]float64, error) {
	if c == nil {
		return nil, errors.New("embed: nil client")
	}
	if len(inputs) == 0 {
		return nil, errors.New("embed: inputs is empty")
	}
	sanitized := sanitizeInputs(inputs)
	var out [][]float64
	for start := 0; start < len(sanitized); start += maxEmbedBatchInputs {
		end := start + maxEmbedBatchInputs
		if end > len(sanitized) {
			end = len(sanitized)
		}
		batch := sanitized[start:end]
		body := map[string]any{"model": c.model, c.inputKey: batch}
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		resp, err := c.client.Do(req)
		if err != nil {
			return nil, err
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("embed request failed: status=%d body=%s", resp.StatusCode, truncate(respBody))
		}
		vecs, err := parse(respBody)
		if err != nil {
			return nil, fmt.Errorf("embed parse: %w body=%s", err, truncate(respBody))
		}
		out = append(out, vecs...)
	}
	if len(out) != len(sanitized) {
		return nil, fmt.Errorf("embedding count mismatch: got=%d want=%d", len(out), len(sanitized))
	}
	return out, nil
}

func truncate(b []byte) string {
	const max = 240
	s := strings.TrimSpace(string(b))
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
