package embed

import (
	"context"
	"fmt"
)

// RerankProvider selects a rerank backend.
type RerankProvider string

const (
	RerankProviderSiliconFlow RerankProvider = "siliconflow"
	RerankProviderFunc        RerankProvider = "func"
)

// RerankFuncConfig adapts a function to Reranker (tests and custom backends).
type RerankFuncConfig struct {
	Fn func(ctx context.Context, query string, documents []string, topN int) ([]RerankResult, error)
}

// RerankConfig builds a Reranker from explicit parameters (no env lookup).
type RerankConfig struct {
	Provider    RerankProvider
	SiliconFlow SiliconFlowRerankConfig
	Func        RerankFuncConfig
}

type funcReranker struct {
	fn func(ctx context.Context, query string, documents []string, topN int) ([]RerankResult, error)
}

func (f *funcReranker) Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankResult, error) {
	return f.fn(ctx, query, documents, topN)
}

// NewRerank returns a rerank client for the configured provider.
func NewRerank(cfg RerankConfig) (Reranker, error) {
	switch cfg.Provider {
	case RerankProviderFunc:
		if cfg.Func.Fn == nil {
			return nil, fmt.Errorf("embed: func reranker required")
		}
		return &funcReranker{fn: cfg.Func.Fn}, nil
	case RerankProviderSiliconFlow, "":
		return NewSiliconFlowRerank(cfg.SiliconFlow)
	default:
		return nil, fmt.Errorf("embed: unsupported rerank provider %q", cfg.Provider)
	}
}
