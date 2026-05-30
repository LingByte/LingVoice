package knowledge

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// Retriever adapts KnowledgeHandler to vector retrieval for RAG pipelines.
type Retriever struct {
	Handler   KnowledgeHandler
	Namespace string
	TopK      int
	MinScore  float64
}

// Retrieve runs a semantic query against the knowledge store.
func (r *Retriever) Retrieve(ctx context.Context, query string, topK int) ([]*schema.Document, error) {
	if r == nil || r.Handler == nil {
		return nil, fmt.Errorf("knowledge: nil retriever")
	}
	if topK <= 0 {
		topK = r.TopK
	}
	if topK <= 0 {
		topK = 3
	}
	opts := &QueryOptions{
		Namespace: r.Namespace,
		TopK:      topK,
		MinScore:  r.MinScore,
	}
	results, err := r.Handler.Query(ctx, query, opts)
	if err != nil {
		return nil, err
	}
	out := make([]*schema.Document, 0, len(results))
	for _, qr := range results {
		rec := qr.Record
		meta := map[string]string{}
		if rec.Title != "" {
			meta["title"] = rec.Title
		}
		if rec.Source != "" {
			meta["source"] = rec.Source
		}
		for k, v := range rec.Metadata {
			if s, ok := v.(string); ok {
				meta[k] = s
			}
		}
		out = append(out, &schema.Document{
			ID:       rec.ID,
			Content:  rec.Content,
			Score:    qr.Score,
			Metadata: meta,
		})
	}
	return out, nil
}
