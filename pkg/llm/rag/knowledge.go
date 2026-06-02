package rag

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/knowledge"
	"github.com/LingByte/LingVoice/pkg/knowledge/embed"
	"github.com/LingByte/LingVoice/pkg/knowledge/retrieve"
	"github.com/LingByte/LingVoice/pkg/search"
)

// KnowledgeConfig wires strategy-based indexing with vector + keyword retrieval.
type KnowledgeConfig struct {
	Handler       knowledge.KnowledgeHandler
	HandlerConfig knowledge.HandlerConfig
	Embed         embed.Config

	Chunker  knowledge.Chunker
	LLM      knowledge.TextCompleter
	LLMModel string
	Search   search.Engine

	RetrieveStrategy retrieve.Strategy
	KeywordFields    []string
	VectorWeight     float64
	Reranker         embed.Reranker
	Rerank           embed.RerankConfig
	RerankCandidates int

	Namespace string
	TopK      int
	MinScore  float64
	Chunk     *knowledge.ChunkOptions
	Preamble  string
}

// NewKnowledgeChain indexes documents with strategy routing, then builds a RAG chain.
func NewKnowledgeChain(ctx context.Context, docs []knowledge.DocumentInput, cfg KnowledgeConfig) (*Chain, error) {
	handler := cfg.Handler
	if handler == nil {
		hc := cfg.HandlerConfig
		if hc.Embedder == nil && cfg.Embed.Provider != "" {
			emb, err := embed.New(cfg.Embed)
			if err != nil {
				return nil, err
			}
			hc.Embedder = emb
		}
		if hc.Provider == "" {
			return nil, fmt.Errorf("rag: handler or handler config required")
		}
		var err error
		handler, err = knowledge.NewHandler(hc)
		if err != nil {
			return nil, err
		}
	}
	idx, err := knowledge.NewIndexerFromConfig(knowledge.IndexerConfig{
		Handler:  handler,
		Chunker:  cfg.Chunker,
		LLM:      cfg.LLM,
		LLMModel: cfg.LLMModel,
		Search:   cfg.Search,
	})
	if err != nil {
		return nil, err
	}
	if _, err := idx.IndexDocuments(ctx, docs, &knowledge.IndexOptions{
		Namespace: cfg.Namespace,
		Chunk:     cfg.Chunk,
	}); err != nil {
		return nil, err
	}

	strategy := cfg.RetrieveStrategy
	if strategy == "" {
		if cfg.Search != nil {
			strategy = retrieve.StrategyHybrid
		} else {
			strategy = retrieve.StrategyVector
		}
	}
	vec := &knowledge.Retriever{
		Handler:   handler,
		Namespace: cfg.Namespace,
		TopK:      cfg.TopK,
		MinScore:  cfg.MinScore,
	}
	sr, err := retrieve.New(retrieve.Config{
		Strategy:         strategy,
		Vector:           vec,
		Search:           cfg.Search,
		TopK:             cfg.TopK,
		MinScore:         cfg.MinScore,
		KeywordFields:    cfg.KeywordFields,
		VectorWeight:     cfg.VectorWeight,
		Reranker:         resolveReranker(cfg),
		RerankCandidates: cfg.RerankCandidates,
	})
	if err != nil {
		return nil, err
	}
	return New(Config{
		Retriever:      sr,
		TopK:           cfg.TopK,
		SystemPreamble: cfg.Preamble,
	})
}

func resolveReranker(cfg KnowledgeConfig) embed.Reranker {
	if cfg.Reranker != nil {
		return cfg.Reranker
	}
	if cfg.Rerank.Provider != "" || cfg.Rerank.SiliconFlow.BaseURL != "" || cfg.Rerank.Func.Fn != nil {
		r, err := embed.NewRerank(cfg.Rerank)
		if err == nil {
			return r
		}
	}
	return nil
}

// ChainFromService builds a RAG chain from an indexed knowledge.Service.
func ChainFromService(svc *knowledge.Service, preamble string) (*Chain, error) {
	if svc == nil || svc.Retriever == nil {
		return nil, fmt.Errorf("rag: nil knowledge service")
	}
	return New(Config{
		Retriever:      svc.Retriever,
		TopK:           svc.TopK,
		SystemPreamble: preamble,
	})
}
