package knowledge

import (
	"context"
	"fmt"
)

// DeleteDocuments removes all chunks for the given parent document IDs from vector store and search index.
// When chunkIDs are known (from IndexResult.ChunkIDs), pass them via DeleteByChunkIDs instead.
func (idx *Indexer) DeleteDocuments(ctx context.Context, docIDs []string, opts *IndexOptions) error {
	if idx == nil || idx.Handler == nil {
		return fmt.Errorf("knowledge: nil indexer")
	}
	if len(docIDs) == 0 {
		return nil
	}
	ns := namespaceFrom(opts)
	var ids []string
	for _, docID := range docIDs {
		if docID == "" {
			continue
		}
		found, err := idx.listChunkIDs(ctx, docID, ns)
		if err != nil {
			return fmt.Errorf("knowledge: list chunks for %q: %w", docID, err)
		}
		ids = append(ids, found...)
	}
	return idx.DeleteByChunkIDs(ctx, ids, opts)
}

// DeleteByChunkIDs removes specific chunk record IDs.
func (idx *Indexer) DeleteByChunkIDs(ctx context.Context, chunkIDs []string, opts *IndexOptions) error {
	if idx == nil || idx.Handler == nil {
		return fmt.Errorf("knowledge: nil indexer")
	}
	if len(chunkIDs) == 0 {
		return nil
	}
	ns := namespaceFrom(opts)
	if err := idx.Handler.Delete(ctx, chunkIDs, &DeleteOptions{Namespace: ns}); err != nil {
		return err
	}
	if idx.Search != nil {
		for _, id := range chunkIDs {
			if err := idx.Search.Delete(ctx, id); err != nil {
				return fmt.Errorf("knowledge: search delete %q: %w", id, err)
			}
		}
	}
	return nil
}

func (idx *Indexer) listChunkIDs(ctx context.Context, parentID, namespace string) ([]string, error) {
	ls, err := idx.Handler.List(ctx, &ListOptions{
		Namespace: namespace,
		Limit:     1000,
		Filters: []Filter{{
			Field:    "parent_id",
			Operator: FilterOpEqual,
			Value:    []any{parentID},
		}},
	})
	if err != nil {
		return nil, err
	}
	if ls == nil || len(ls.Records) == 0 {
		// Fallback: scan without filter for handlers that ignore metadata filters.
		ls, err = idx.Handler.List(ctx, &ListOptions{Namespace: namespace, Limit: 1000})
		if err != nil {
			return nil, err
		}
	}
	var ids []string
	for _, rec := range ls.Records {
		if rec.ID == "" {
			continue
		}
		if pid, _ := rec.Metadata["parent_id"].(string); pid == parentID || hasPrefixChunk(parentID, rec.ID) {
			ids = append(ids, rec.ID)
		}
	}
	return ids, nil
}

func hasPrefixChunk(parentID, chunkID string) bool {
	prefix := parentID + "#"
	return len(chunkID) > len(prefix) && chunkID[:len(prefix)] == prefix
}
