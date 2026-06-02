package retrieve_test

import (
	"context"
	"sort"
	"testing"

	"github.com/LingByte/LingVoice/pkg/knowledge/embed"
	"github.com/LingByte/LingVoice/pkg/knowledge/retrieve"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type stubReranker struct{}

func (stubReranker) Rerank(_ context.Context, _ string, documents []string, topN int) ([]embed.RerankResult, error) {
	results := make([]embed.RerankResult, len(documents))
	for i, doc := range documents {
		results[i] = embed.RerankResult{Index: i, Score: float64(len(doc))}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if topN <= 0 || topN > len(results) {
		topN = len(results)
	}
	return results[:topN], nil
}

func TestStrategyRetriever_Rerank(t *testing.T) {
	sr, err := retrieve.New(retrieve.Config{
		Strategy: retrieve.StrategyVector,
		Vector: vecStub{docs: []*schema.Document{
			{ID: "short", Content: "a2a", Score: 0.1},
			{ID: "long", Content: "pregel mailbox streaming channel", Score: 0.9},
		}},
		TopK:             1,
		Reranker:         stubReranker{},
		RerankCandidates: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := sr.Retrieve(context.Background(), "pregel", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].ID != "long" {
		t.Fatalf("expected reranked long doc, got %+v", out)
	}
}
