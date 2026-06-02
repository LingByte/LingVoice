package retriever

import (
	"context"
	"fmt"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// VectorEntry stores an indexed document embedding.
type VectorEntry struct {
	Doc    *schema.Document
	Vector []float32
}

// InMemoryVectorStore holds document vectors for similarity search.
type InMemoryVectorStore struct {
	mu      sync.RWMutex
	entries []VectorEntry
}

// Upsert replaces or appends vectors for documents.
func (s *InMemoryVectorStore) Upsert(entries ...VectorEntry) {
	if s == nil || len(entries) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		if e.Doc == nil || len(e.Vector) == 0 {
			continue
		}
		replaced := false
		for i := range s.entries {
			if s.entries[i].Doc != nil && s.entries[i].Doc.ID == e.Doc.ID {
				s.entries[i] = e
				replaced = true
				break
			}
		}
		if !replaced {
			s.entries = append(s.entries, e)
		}
	}
}

// Search returns topK entries by cosine similarity to queryVec.
func (s *InMemoryVectorStore) Search(queryVec []float32, topK int) []*schema.Document {
	if s == nil || len(queryVec) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 3
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	type scored struct {
		doc   *schema.Document
		score float64
	}
	var ranked []scored
	for _, e := range s.entries {
		if e.Doc == nil {
			continue
		}
		score := CosineSimilarity(queryVec, e.Vector)
		if score <= 0 {
			continue
		}
		copy := *e.Doc
		copy.Score = score
		ranked = append(ranked, scored{doc: &copy, score: score})
	}
	for i := 0; i < len(ranked); i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}
	if len(ranked) > topK {
		ranked = ranked[:topK]
	}
	out := make([]*schema.Document, len(ranked))
	for i, r := range ranked {
		out[i] = r.doc
	}
	return out
}

// VectorRetriever embeds queries and searches a vector store.
type VectorRetriever struct {
	Embedder Embedder
	Store    *InMemoryVectorStore
	TopK     int
}

// NewVectorRetriever builds a vector retriever.
func NewVectorRetriever(embedder Embedder, store *InMemoryVectorStore, topK int) (*VectorRetriever, error) {
	if embedder == nil {
		return nil, fmt.Errorf("retriever: nil embedder")
	}
	if store == nil {
		store = &InMemoryVectorStore{}
	}
	if topK <= 0 {
		topK = 3
	}
	return &VectorRetriever{Embedder: embedder, Store: store, TopK: topK}, nil
}

// Index embeds and stores documents.
func (v *VectorRetriever) Index(ctx context.Context, docs []*schema.Document) error {
	if v == nil || v.Embedder == nil || v.Store == nil {
		return fmt.Errorf("retriever: nil vector retriever")
	}
	texts := make([]string, len(docs))
	for i, d := range docs {
		if d != nil {
			texts[i] = d.Content
		}
	}
	vecs, err := v.Embedder.Embed(ctx, texts)
	if err != nil {
		return err
	}
	var entries []VectorEntry
	for i, d := range docs {
		if d == nil || i >= len(vecs) {
			continue
		}
		entries = append(entries, VectorEntry{Doc: d, Vector: vecs[i]})
	}
	v.Store.Upsert(entries...)
	return nil
}

// Retrieve embeds the query and searches the vector store.
func (v *VectorRetriever) Retrieve(ctx context.Context, query string, topK int) ([]*schema.Document, error) {
	if v == nil || v.Embedder == nil || v.Store == nil {
		return nil, fmt.Errorf("retriever: nil vector retriever")
	}
	if topK <= 0 {
		topK = v.TopK
	}
	vecs, err := v.Embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, nil
	}
	return v.Store.Search(vecs[0], topK), nil
}
