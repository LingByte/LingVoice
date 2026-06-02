package knowledge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/search"
)

// DocumentInput is a source document to chunk and index.
type DocumentInput struct {
	ID       string
	Source   string
	Title    string
	Content  string
	Tags     []string
	Metadata map[string]any
	// Strategy overrides IndexOptions.Chunk.Strategy for this document.
	Strategy ChunkStrategy
}

// IndexOptions configures document ingestion.
type IndexOptions struct {
	Namespace    string
	Chunk        *ChunkOptions
	BatchSize    int
	SearchDocType string
}

// IndexResult summarizes one indexed document.
type IndexResult struct {
	DocumentID string
	ChunkCount int
	ChunkIDs   []string
	Strategy   string
	DocType    string
}

// Indexer chunks documents by strategy and upserts vectors into a knowledge store.
type Indexer struct {
	Handler KnowledgeHandler
	Chunker Chunker
	Search  search.Engine
}

// NewIndexer builds an indexer. When chunker is nil, DefaultRoutingChunker(nil) is used.
func NewIndexer(handler KnowledgeHandler, chunker Chunker) (*Indexer, error) {
	if handler == nil {
		return nil, fmt.Errorf("knowledge: nil handler")
	}
	if chunker == nil {
		chunker = DefaultRoutingChunker(nil)
	}
	return &Indexer{Handler: handler, Chunker: chunker}, nil
}

// ChunkDocument splits one document using the configured chunking strategy.
func (idx *Indexer) ChunkDocument(ctx context.Context, doc DocumentInput, opts *IndexOptions) ([]Chunk, error) {
	if idx == nil || idx.Chunker == nil {
		return nil, fmt.Errorf("knowledge: nil indexer")
	}
	text := strings.TrimSpace(doc.Content)
	if text == "" {
		return nil, ErrEmptyText
	}
	chunkOpts := cloneChunkOptions(opts)
	if doc.Strategy != "" {
		if chunkOpts == nil {
			chunkOpts = &ChunkOptions{}
		}
		chunkOpts.Strategy = doc.Strategy
	}
	if chunkOpts != nil && doc.Title != "" && chunkOpts.DocumentTitle == "" {
		chunkOpts.DocumentTitle = doc.Title
	}
	return idx.Chunker.Chunk(ctx, text, chunkOpts)
}

// IndexDocuments chunks each input document and upserts all segments.
func (idx *Indexer) IndexDocuments(ctx context.Context, docs []DocumentInput, opts *IndexOptions) ([]IndexResult, error) {
	if idx == nil || idx.Handler == nil {
		return nil, fmt.Errorf("knowledge: nil indexer")
	}
	if len(docs) == 0 {
		return nil, nil
	}

	var records []Record
	results := make([]IndexResult, 0, len(docs))
	now := time.Now().UTC()

	for _, doc := range docs {
		if strings.TrimSpace(doc.ID) == "" {
			return nil, fmt.Errorf("knowledge: document id required")
		}
		chunks, err := idx.ChunkDocument(ctx, doc, opts)
		if err != nil {
			return nil, fmt.Errorf("knowledge: chunk %q: %w", doc.ID, err)
		}
		res := IndexResult{DocumentID: doc.ID, ChunkCount: len(chunks)}
		if len(chunks) > 0 && chunks[0].Metadata != nil {
			if s, ok := chunks[0].Metadata["chunk_strategy"].(string); ok {
				res.Strategy = s
			}
			if s, ok := chunks[0].Metadata["document_type"].(string); ok {
				res.DocType = s
			}
		}
		chunkIDs := make([]string, 0, len(chunks))
		for _, ch := range chunks {
			meta := cloneMetadata(doc.Metadata)
			for k, v := range ch.Metadata {
				meta[k] = v
			}
			meta["parent_id"] = doc.ID
			meta["chunk_index"] = ch.Index
			title := strings.TrimSpace(ch.Title)
			if title == "" {
				title = doc.Title
			}
			chunkID := fmt.Sprintf("%s#%d", doc.ID, ch.Index)
			chunkIDs = append(chunkIDs, chunkID)
			records = append(records, Record{
				ID:        chunkID,
				Source:    doc.Source,
				Title:     title,
				Content:   ch.Text,
				Tags:      doc.Tags,
				Metadata:  meta,
				CreatedAt: now,
				UpdatedAt: now,
			})
		}
		res.ChunkIDs = chunkIDs
		results = append(results, res)
	}

	upsert := &UpsertOptions{Namespace: namespaceFrom(opts)}
	if opts != nil && opts.BatchSize > 0 {
		upsert.BatchSize = opts.BatchSize
	}
	if err := idx.Handler.Upsert(ctx, records, upsert); err != nil {
		return nil, err
	}
	if idx.Search != nil {
		docType := "knowledge_chunk"
		if opts != nil && opts.SearchDocType != "" {
			docType = opts.SearchDocType
		}
		if err := IndexSearchRecords(ctx, idx.Search, records, docType); err != nil {
			return nil, fmt.Errorf("knowledge: search index: %w", err)
		}
	}
	return results, nil
}

func cloneChunkOptions(opts *IndexOptions) *ChunkOptions {
	if opts == nil || opts.Chunk == nil {
		return nil
	}
	copy := *opts.Chunk
	if opts.Chunk.PreChunkClean != nil {
		clean := *opts.Chunk.PreChunkClean
		copy.PreChunkClean = &clean
	}
	return &copy
}

func cloneMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func namespaceFrom(opts *IndexOptions) string {
	if opts == nil {
		return ""
	}
	return opts.Namespace
}
