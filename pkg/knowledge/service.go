package knowledge

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/knowledge/retrieve"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// Service bundles indexing, retrieval, and RAG helpers behind one facade.
type Service struct {
	Handler   KnowledgeHandler
	Indexer   *Indexer
	Retriever *retrieve.StrategyRetriever
	Namespace string
	TopK      int
	MinScore  float64
}

// ServiceConfig wires handler, indexer, and retrieval strategy explicitly.
type ServiceConfig struct {
	Handler       KnowledgeHandler
	HandlerConfig HandlerConfig
	Indexer       IndexerConfig
	Retrieve      retrieve.Config
	Namespace     string
	TopK          int
	MinScore      float64
}

// NewService builds a knowledge service from explicit configuration.
func NewService(cfg ServiceConfig) (*Service, error) {
	handler := cfg.Handler
	if handler == nil {
		var err error
		handler, err = NewHandler(cfg.HandlerConfig)
		if err != nil {
			return nil, err
		}
	}
	idxCfg := cfg.Indexer
	idxCfg.Handler = handler
	idx, err := NewIndexerFromConfig(idxCfg)
	if err != nil {
		return nil, err
	}
	topK := cfg.TopK
	if topK <= 0 {
		topK = cfg.Retrieve.TopK
	}
	if topK <= 0 {
		topK = 3
	}
	minScore := cfg.MinScore
	if minScore == 0 && cfg.Retrieve.MinScore > 0 {
		minScore = cfg.Retrieve.MinScore
	}
	vec := &Retriever{
		Handler:   handler,
		Namespace: cfg.Namespace,
		TopK:      topK,
		MinScore:  minScore,
	}
	retCfg := cfg.Retrieve
	if retCfg.Strategy == "" {
		if idx.Search != nil {
			retCfg.Strategy = retrieve.StrategyHybrid
		} else {
			retCfg.Strategy = retrieve.StrategyVector
		}
	}
	retCfg.Vector = vec
	if retCfg.TopK <= 0 {
		retCfg.TopK = topK
	}
	if retCfg.MinScore == 0 {
		retCfg.MinScore = minScore
	}
	sr, err := retrieve.New(retCfg)
	if err != nil {
		return nil, err
	}
	return &Service{
		Handler:   handler,
		Indexer:   idx,
		Retriever: sr,
		Namespace: cfg.Namespace,
		TopK:      topK,
		MinScore:  minScore,
	}, nil
}

// IndexDocuments chunks and indexes documents.
func (s *Service) IndexDocuments(ctx context.Context, docs []DocumentInput, opts *IndexOptions) ([]IndexResult, error) {
	if s == nil || s.Indexer == nil {
		return nil, fmt.Errorf("knowledge: nil service")
	}
	return s.Indexer.IndexDocuments(ctx, docs, opts)
}

// DeleteDocuments removes indexed parent documents.
func (s *Service) DeleteDocuments(ctx context.Context, docIDs []string, opts *IndexOptions) error {
	if s == nil || s.Indexer == nil {
		return fmt.Errorf("knowledge: nil service")
	}
	return s.Indexer.DeleteDocuments(ctx, docIDs, opts)
}

// UpdateDocuments re-indexes documents in place.
func (s *Service) UpdateDocuments(ctx context.Context, docs []DocumentInput, opts *IndexOptions) ([]IndexResult, error) {
	if s == nil || s.Indexer == nil {
		return nil, fmt.Errorf("knowledge: nil service")
	}
	return s.Indexer.UpdateDocuments(ctx, docs, opts)
}

// Retrieve runs the configured retrieval strategy.
func (s *Service) Retrieve(ctx context.Context, query string, topK int) ([]*schema.Document, error) {
	if s == nil || s.Retriever == nil {
		return nil, fmt.Errorf("knowledge: nil service")
	}
	return s.Retriever.Retrieve(ctx, query, topK)
}
