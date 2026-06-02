package knowledge

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/search"
)

// IndexSearchRecords indexes knowledge records into pkg/search for keyword/hybrid retrieval.
func IndexSearchRecords(ctx context.Context, engine search.Engine, records []Record, docType string) error {
	if engine == nil || len(records) == 0 {
		return nil
	}
	if docType == "" {
		docType = "knowledge_chunk"
	}
	docs := make([]search.Doc, 0, len(records))
	for _, rec := range records {
		fields := map[string]any{
			"title":   rec.Title,
			"content": rec.Content,
			"body":    rec.Content,
			"source":  rec.Source,
			"tags":    rec.Tags,
		}
		for k, v := range rec.Metadata {
			fields[k] = v
		}
		docs = append(docs, search.Doc{ID: rec.ID, Type: docType, Fields: fields})
	}
	return engine.IndexBatch(ctx, docs)
}
