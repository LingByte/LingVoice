package llm

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"
)

// Document represents a document in the knowledge base
type Document struct {
	ID       string
	Content  string
	Metadata map[string]interface{}
	Score    float64
}

// Retriever defines the interface for document retrieval
type Retriever interface {
	// Retrieve retrieves documents matching the query
	Retrieve(ctx context.Context, query string, topK int) ([]Document, error)

	// Add adds a document to the knowledge base
	Add(ctx context.Context, doc Document) error

	// Delete deletes a document from the knowledge base
	Delete(ctx context.Context, docID string) error

	// Clear clears all documents
	Clear(ctx context.Context) error
}

// SimpleRetriever is a basic in-memory retriever
type SimpleRetriever struct {
	documents map[string]Document
	mu        sync.RWMutex
}

// NewSimpleRetriever creates a new simple retriever
func NewSimpleRetriever() *SimpleRetriever {
	return &SimpleRetriever{
		documents: make(map[string]Document),
	}
}

// Retrieve retrieves documents matching the query
func (sr *SimpleRetriever) Retrieve(ctx context.Context, query string, topK int) ([]Document, error) {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	var results []Document
	for _, doc := range sr.documents {
		// Simple keyword matching
		if contains(doc.Content, query) {
			results = append(results, doc)
		}
	}

	// Limit results
	if len(results) > topK {
		results = results[:topK]
	}

	return results, nil
}

// Add adds a document to the knowledge base
func (sr *SimpleRetriever) Add(ctx context.Context, doc Document) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if doc.ID == "" {
		return fmt.Errorf("document ID cannot be empty")
	}

	sr.documents[doc.ID] = doc
	return nil
}

// Delete deletes a document from the knowledge base
func (sr *SimpleRetriever) Delete(ctx context.Context, docID string) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	delete(sr.documents, docID)
	return nil
}

// Clear clears all documents
func (sr *SimpleRetriever) Clear(ctx context.Context) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	sr.documents = make(map[string]Document)
	return nil
}

// RAGChain represents a retrieval-augmented generation chain
type RAGChain struct {
	retriever Retriever
	llm       LLMModel
	topK      int
}

// NewRAGChain creates a new RAG chain
func NewRAGChain(retriever Retriever, llm LLMModel, topK int) *RAGChain {
	return &RAGChain{
		retriever: retriever,
		llm:       llm,
		topK:      topK,
	}
}

// Execute executes the RAG chain
func (rc *RAGChain) Execute(ctx context.Context, query string) (string, error) {
	// Retrieve relevant documents
	docs, err := rc.retriever.Retrieve(ctx, query, rc.topK)
	if err != nil {
		return "", fmt.Errorf("retrieval failed: %w", err)
	}

	// Build context from retrieved documents
	context := ""
	for _, doc := range docs {
		context += doc.Content + "\n"
	}

	// Build prompt with context
	prompt := fmt.Sprintf("Context:\n%s\n\nQuestion: %s\n\nAnswer:", context, query)

	// Generate response
	response, err := rc.llm.Generate(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("generation failed: %w", err)
	}

	return response, nil
}

// contains checks if a string contains a substring (case-insensitive)
func contains(text, substring string) bool {
	for i := 0; i <= len(text)-len(substring); i++ {
		if text[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}

// RAGStep is a chain step for RAG
type RAGStep struct {
	name      string
	ragChain  *RAGChain
	queryKey  string
	outputKey string
}

// NewRAGStep creates a new RAG step
func NewRAGStep(name string, ragChain *RAGChain, queryKey, outputKey string) *RAGStep {
	return &RAGStep{
		name:      name,
		ragChain:  ragChain,
		queryKey:  queryKey,
		outputKey: outputKey,
	}
}

// Execute executes the RAG step
func (rs *RAGStep) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	query, ok := input[rs.queryKey].(string)
	if !ok {
		return nil, fmt.Errorf("query key %s not found or not a string", rs.queryKey)
	}

	result, err := rs.ragChain.Execute(ctx, query)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		rs.outputKey: result,
	}, nil
}

// GetInputKeys returns required input keys
func (rs *RAGStep) GetInputKeys() []string {
	return []string{rs.queryKey}
}

// GetOutputKeys returns produced output keys
func (rs *RAGStep) GetOutputKeys() []string {
	return []string{rs.outputKey}
}

// GetName returns the step name
func (rs *RAGStep) GetName() string {
	return rs.name
}
