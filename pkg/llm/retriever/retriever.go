package retriever

import (
	"context"
	"strings"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// Retriever fetches relevant documents for a query (Eino retriever subset).
type Retriever interface {
	Retrieve(ctx context.Context, query string, topK int) ([]*schema.Document, error)
}

// InMemoryRetriever is a keyword-scoring retriever for demos and tests.
type InMemoryRetriever struct {
	Docs []*schema.Document
}

// Retrieve ranks documents by simple keyword overlap.
func (r *InMemoryRetriever) Retrieve(_ context.Context, query string, topK int) ([]*schema.Document, error) {
	if r == nil || len(r.Docs) == 0 {
		return nil, nil
	}
	if topK <= 0 {
		topK = 3
	}
	type scored struct {
		doc   *schema.Document
		score float64
	}
	var ranked []scored
	terms := strings.Fields(strings.ToLower(query))
	for _, d := range r.Docs {
		if d == nil {
			continue
		}
		text := strings.ToLower(d.Content)
		var score float64
		for _, t := range terms {
			if strings.Contains(text, t) {
				score++
			}
		}
		if score > 0 {
			copy := *d
			copy.Score = score
			ranked = append(ranked, scored{doc: &copy, score: score})
		}
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
	for i, s := range ranked {
		out[i] = s.doc
	}
	return out, nil
}
