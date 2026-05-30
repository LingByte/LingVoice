package retrieve

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/knowledge/embed"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func applyRerank(ctx context.Context, reranker embed.Reranker, query string, docs []*schema.Document, topK, candidates int) ([]*schema.Document, error) {
	if reranker == nil {
		return docs, nil
	}
	if len(docs) == 0 {
		return docs, nil
	}
	if topK <= 0 {
		topK = len(docs)
	}
	if candidates <= 0 {
		candidates = topK * 3
	}
	if candidates < topK {
		candidates = topK
	}
	if len(docs) > candidates {
		docs = docs[:candidates]
	}
	texts := make([]string, len(docs))
	for i, d := range docs {
		if d == nil {
			continue
		}
		texts[i] = d.Content
	}
	results, err := reranker.Rerank(ctx, query, texts, topK)
	if err != nil {
		return nil, fmt.Errorf("retrieve: rerank: %w", err)
	}
	out := make([]*schema.Document, 0, len(results))
	for _, r := range results {
		if r.Index < 0 || r.Index >= len(docs) {
			continue
		}
		doc := docs[r.Index]
		if doc == nil {
			continue
		}
		copy := *doc
		copy.Score = r.Score
		out = append(out, &copy)
	}
	return out, nil
}
