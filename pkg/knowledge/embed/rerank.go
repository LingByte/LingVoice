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

// Reranker re-scores candidate documents for a query.
type Reranker interface {
	Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankResult, error)
}

// RerankResult is one reranked document index and score.
type RerankResult struct {
	Index int
	Score float64
}

// SiliconFlowRerankConfig configures SiliconFlow-compatible rerank APIs.
type SiliconFlowRerankConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

// SiliconFlowRerankClient calls /rerank on compatible endpoints.
type SiliconFlowRerankClient struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func NewSiliconFlowRerank(cfg SiliconFlowRerankConfig) (*SiliconFlowRerankClient, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("embed: BaseURL is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("embed: APIKey is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("embed: Model is required")
	}
	return &SiliconFlowRerankClient{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		client:  defaultHTTPClient(cfg.HTTPClient, 30*time.Second),
	}, nil
}

func (c *SiliconFlowRerankClient) Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankResult, error) {
	if c == nil {
		return nil, errors.New("embed: nil rerank client")
	}
	if query == "" {
		return nil, errors.New("embed: query is empty")
	}
	if len(documents) == 0 {
		return nil, errors.New("embed: documents is empty")
	}
	if topN <= 0 {
		topN = 5
	}
	endpoint := c.baseURL
	if !strings.HasSuffix(endpoint, "/rerank") {
		endpoint += "/rerank"
	}
	body := map[string]any{
		"model":     c.model,
		"query":     query,
		"documents": documents,
		"top_n":     topN,
	}
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
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rerank request failed: status=%d body=%s", resp.StatusCode, string(respBody))
	}
	var parsed1 struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
			Score          float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &parsed1); err == nil && len(parsed1.Results) > 0 {
		out := make([]RerankResult, 0, len(parsed1.Results))
		for _, r := range parsed1.Results {
			s := r.Score
			if s == 0 {
				s = r.RelevanceScore
			}
			out = append(out, RerankResult{Index: r.Index, Score: s})
		}
		return out, nil
	}
	var parsed2 struct {
		Data []struct {
			Index int     `json:"index"`
			Score float64 `json:"score"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &parsed2); err == nil && len(parsed2.Data) > 0 {
		out := make([]RerankResult, 0, len(parsed2.Data))
		for _, r := range parsed2.Data {
			out = append(out, RerankResult{Index: r.Index, Score: r.Score})
		}
		return out, nil
	}
	return nil, fmt.Errorf("embed: unrecognized rerank response: %s", string(respBody))
}

var _ Reranker = (*SiliconFlowRerankClient)(nil)
