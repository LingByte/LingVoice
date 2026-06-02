package rag

import (
	"context"
	"fmt"
	"strings"

	"github.com/LingByte/LingVoice/pkg/llm/retriever"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// Config configures retrieval-augmented generation (Eino RAG subset).
type Config struct {
	Retriever retriever.Retriever
	TopK      int
	SystemPreamble string
}

// Chain retrieves context and builds augmented messages.
type Chain struct {
	retriever retriever.Retriever
	topK      int
	preamble  string
}

// New creates a RAG chain.
func New(cfg Config) (*Chain, error) {
	if cfg.Retriever == nil {
		return nil, fmt.Errorf("rag: nil retriever")
	}
	topK := cfg.TopK
	if topK <= 0 {
		topK = 3
	}
	preamble := cfg.SystemPreamble
	if preamble == "" {
		preamble = "Use the following context to answer the user question."
	}
	return &Chain{retriever: cfg.Retriever, topK: topK, preamble: preamble}, nil
}

// BuildMessages retrieves docs and returns system+user messages.
func (c *Chain) BuildMessages(ctx context.Context, query string) ([]*schema.Message, error) {
	if c == nil || c.retriever == nil {
		return nil, fmt.Errorf("rag: nil chain")
	}
	docs, err := c.retriever.Retrieve(ctx, query, c.topK)
	if err != nil {
		return nil, err
	}
	contextBlock := schema.DocumentsPlainText(docs)
	sys := c.preamble
	if contextBlock != "" {
		sys = strings.TrimSpace(sys + "\n\nContext:\n" + contextBlock)
	}
	return []*schema.Message{
		schema.SystemMessage(sys),
		schema.UserMessage(query),
	}, nil
}

// NewVectorChain builds a RAG chain backed by vector retrieval.
func NewVectorChain(ctx context.Context, embedder Embedder, docs []*schema.Document, cfg Config) (*Chain, error) {
	vr, err := retriever.NewVectorRetriever(embedder, &retriever.InMemoryVectorStore{}, cfg.TopK)
	if err != nil {
		return nil, err
	}
	if err := vr.Index(ctx, docs); err != nil {
		return nil, err
	}
	cfg.Retriever = vr
	return New(cfg)
}

// Embedder is the embedding interface used by vector RAG chains.
type Embedder = retriever.Embedder

// Retrieve returns raw documents for compose nodes.
func (c *Chain) Retrieve(ctx context.Context, query string) ([]*schema.Document, error) {
	if c == nil || c.retriever == nil {
		return nil, fmt.Errorf("rag: nil chain")
	}
	return c.retriever.Retrieve(ctx, query, c.topK)
}
