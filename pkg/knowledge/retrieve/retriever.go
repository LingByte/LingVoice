package retrieve

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/LingByte/LingVoice/pkg/knowledge/embed"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
	"github.com/LingByte/LingVoice/pkg/search"
)

// VectorRetriever runs dense vector queries against a knowledge store.
type VectorRetriever interface {
	Retrieve(ctx context.Context, query string, topK int) ([]*schema.Document, error)
}

// Config configures strategy-based retrieval over vector store + search engine.
type Config struct {
	Strategy Strategy
	Vector   VectorRetriever
	Search   search.Engine
	TopK     int
	MinScore float64
	// KeywordFields passed to pkg/search when StrategyKeyword or StrategyHybrid.
	KeywordFields []string
	// VectorWeight is the vector score weight in hybrid mode (0–1, default 0.65).
	VectorWeight float64
	// Reranker optionally re-scores candidate documents after the base strategy.
	Reranker embed.Reranker
	// RerankCandidates is how many docs to fetch before reranking (default TopK*3).
	RerankCandidates int
}

// StrategyRetriever implements retriever.Retriever with vector/keyword/hybrid strategies.
type StrategyRetriever struct {
	cfg Config
}

// New builds a strategy retriever.
func New(cfg Config) (*StrategyRetriever, error) {
	if cfg.Strategy == "" {
		cfg.Strategy = StrategyVector
	}
	if cfg.TopK <= 0 {
		cfg.TopK = 3
	}
	if cfg.VectorWeight <= 0 {
		cfg.VectorWeight = 0.65
	}
	if len(cfg.KeywordFields) == 0 {
		cfg.KeywordFields = []string{"title", "content", "body"}
	}
	switch cfg.Strategy {
	case StrategyVector:
		if cfg.Vector == nil {
			return nil, fmt.Errorf("retrieve: vector retriever required")
		}
	case StrategyKeyword:
		if cfg.Search == nil {
			return nil, fmt.Errorf("retrieve: search engine required")
		}
	case StrategyHybrid:
		if cfg.Vector == nil || cfg.Search == nil {
			return nil, fmt.Errorf("retrieve: hybrid requires vector retriever and search engine")
		}
	default:
		return nil, fmt.Errorf("retrieve: unsupported strategy %q", cfg.Strategy)
	}
	return &StrategyRetriever{cfg: cfg}, nil
}

// Retrieve runs the configured strategy.
func (r *StrategyRetriever) Retrieve(ctx context.Context, query string, topK int) ([]*schema.Document, error) {
	if r == nil {
		return nil, fmt.Errorf("retrieve: nil retriever")
	}
	if topK <= 0 {
		topK = r.cfg.TopK
	}
	fetchK := topK
	if r.cfg.Reranker != nil {
		fetchK = r.cfg.RerankCandidates
		if fetchK <= 0 {
			fetchK = topK * 3
		}
		if fetchK < topK {
			fetchK = topK
		}
	}
	var docs []*schema.Document
	var err error
	switch r.cfg.Strategy {
	case StrategyVector:
		docs, err = r.cfg.Vector.Retrieve(ctx, query, fetchK)
	case StrategyKeyword:
		docs, err = r.keywordSearch(ctx, query, fetchK)
	case StrategyHybrid:
		docs, err = r.hybridSearch(ctx, query, fetchK)
	default:
		return nil, fmt.Errorf("retrieve: unsupported strategy")
	}
	if err != nil {
		return nil, err
	}
	return applyRerank(ctx, r.cfg.Reranker, query, docs, topK, fetchK)
}

func (r *StrategyRetriever) keywordSearch(ctx context.Context, query string, topK int) ([]*schema.Document, error) {
	res, err := r.cfg.Search.Search(ctx, search.SearchRequest{
		Keyword:      query,
		SearchFields: r.cfg.KeywordFields,
		Size:         topK,
	})
	if err != nil {
		return nil, err
	}
	return hitsToDocuments(res.Hits, r.cfg.MinScore), nil
}

func (r *StrategyRetriever) hybridSearch(ctx context.Context, query string, topK int) ([]*schema.Document, error) {
	vecDocs, err := r.cfg.Vector.Retrieve(ctx, query, topK*2)
	if err != nil {
		return nil, err
	}
	kwDocs, err := r.keywordSearch(ctx, query, topK*2)
	if err != nil {
		return nil, err
	}
	wv := r.cfg.VectorWeight
	if wv > 1 {
		wv = 1
	}
	wk := 1 - wv

	type scored struct {
		doc   *schema.Document
		score float64
	}
	merged := map[string]scored{}
	for _, d := range vecDocs {
		if d == nil {
			continue
		}
		id := d.ID
		if id == "" {
			id = d.Content
		}
		s := merged[id]
		s.doc = d
		s.score += d.Score * wv
		merged[id] = s
	}
	for _, d := range kwDocs {
		if d == nil {
			continue
		}
		id := d.ID
		if id == "" {
			id = d.Content
		}
		s := merged[id]
		if s.doc == nil {
			s.doc = d
		}
		s.score += d.Score * wk
		merged[id] = s
	}
	var ranked []scored
	for _, s := range merged {
		if s.doc == nil || s.score < r.cfg.MinScore {
			continue
		}
		copy := *s.doc
		copy.Score = s.score
		ranked = append(ranked, scored{doc: &copy, score: s.score})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if len(ranked) > topK {
		ranked = ranked[:topK]
	}
	out := make([]*schema.Document, len(ranked))
	for i, s := range ranked {
		out[i] = s.doc
	}
	return out, nil
}

func hitsToDocuments(hits []search.Hit, minScore float64) []*schema.Document {
	out := make([]*schema.Document, 0, len(hits))
	for _, h := range hits {
		if h.Score < minScore {
			continue
		}
		content := fieldString(h.Fields, "content")
		if content == "" {
			content = fieldString(h.Fields, "body")
		}
		meta := map[string]string{}
		for _, k := range []string{"title", "source", "parent_id", "chunk_strategy"} {
			if v := fieldString(h.Fields, k); v != "" {
				meta[k] = v
			}
		}
		out = append(out, &schema.Document{
			ID:       h.ID,
			Content:  content,
			Score:    h.Score,
			Metadata: meta,
		})
	}
	return out
}

func fieldString(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	v, ok := fields[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []string:
		if len(t) > 0 {
			return t[0]
		}
	}
	return fmt.Sprint(v)
}
