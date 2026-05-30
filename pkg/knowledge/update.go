package knowledge

import (
	"context"
	"fmt"
)

// UpdateDocuments deletes existing chunks for each document ID and re-indexes content.
func (idx *Indexer) UpdateDocuments(ctx context.Context, docs []DocumentInput, opts *IndexOptions) ([]IndexResult, error) {
	if idx == nil || idx.Handler == nil {
		return nil, fmt.Errorf("knowledge: nil indexer")
	}
	if len(docs) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(docs))
	for _, doc := range docs {
		if doc.ID != "" {
			ids = append(ids, doc.ID)
		}
	}
	if err := idx.DeleteDocuments(ctx, ids, opts); err != nil {
		return nil, err
	}
	return idx.IndexDocuments(ctx, docs, opts)
}
