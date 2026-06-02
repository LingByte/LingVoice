package rag

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/knowledge"
)

// NewIndexedServiceChain builds a knowledge.Service, indexes documents, and returns a RAG chain.
func NewIndexedServiceChain(ctx context.Context, docs []knowledge.DocumentInput, cfg knowledge.ServiceConfig, indexOpts *knowledge.IndexOptions, preamble string) (*Chain, *knowledge.Service, error) {
	svc, err := knowledge.NewService(cfg)
	if err != nil {
		return nil, nil, err
	}
	if len(docs) > 0 {
		if _, err := svc.IndexDocuments(ctx, docs, indexOpts); err != nil {
			return nil, nil, err
		}
	}
	chain, err := ChainFromService(svc, preamble)
	if err != nil {
		return nil, nil, err
	}
	return chain, svc, nil
}

// MustNewIndexedServiceChain is like NewIndexedServiceChain but panics on error (demos only).
func MustNewIndexedServiceChain(ctx context.Context, docs []knowledge.DocumentInput, cfg knowledge.ServiceConfig, indexOpts *knowledge.IndexOptions, preamble string) (*Chain, *knowledge.Service) {
	chain, svc, err := NewIndexedServiceChain(ctx, docs, cfg, indexOpts, preamble)
	if err != nil {
		panic(fmt.Sprintf("rag: NewIndexedServiceChain: %v", err))
	}
	return chain, svc
}
