package retrieve_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/knowledge/retrieve"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
	"github.com/LingByte/LingVoice/pkg/search"
)

type vecStub struct {
	docs []*schema.Document
}

func (v vecStub) Retrieve(_ context.Context, _ string, topK int) ([]*schema.Document, error) {
	if topK <= 0 || topK >= len(v.docs) {
		return v.docs, nil
	}
	return v.docs[:topK], nil
}

type memSearch struct {
	docs map[string]search.Doc
}

func (m *memSearch) Index(_ context.Context, doc search.Doc) error {
	m.docs[doc.ID] = doc
	return nil
}
func (m *memSearch) IndexBatch(ctx context.Context, docs []search.Doc) error {
	for _, d := range docs {
		if err := m.Index(ctx, d); err != nil {
			return err
		}
	}
	return nil
}
func (m *memSearch) Delete(context.Context, string) error { return nil }
func (m *memSearch) Search(_ context.Context, req search.SearchRequest) (search.SearchResult, error) {
	var hits []search.Hit
	for id, d := range m.docs {
		body, _ := d.Fields["content"].(string)
		if req.Keyword == "" || containsFold(body, req.Keyword) || containsFold(id, req.Keyword) {
			hits = append(hits, search.Hit{ID: id, Score: 1.0, Fields: d.Fields})
		}
	}
	return search.SearchResult{Hits: hits}, nil
}
func (m *memSearch) GetAutoCompleteSuggestions(context.Context, string) ([]string, error) {
	return nil, nil
}
func (m *memSearch) GetSearchSuggestions(context.Context, string) ([]string, error) { return nil, nil }
func (m *memSearch) Close() error                                                   { return nil }

func containsFold(s, sub string) bool {
	return len(sub) > 0 && (s == sub || len(s) >= len(sub) && indexFold(s, sub) >= 0)
}

func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestStrategyRetriever_Hybrid(t *testing.T) {
	sr, err := retrieve.New(retrieve.Config{
		Strategy: retrieve.StrategyHybrid,
		Vector: vecStub{docs: []*schema.Document{
			{ID: "a", Content: "pregel bsp", Score: 0.9},
			{ID: "b", Content: "a2a jsonrpc", Score: 0.2},
		}},
		Search: &memSearch{docs: map[string]search.Doc{
			"b": {ID: "b", Fields: map[string]any{"content": "a2a jsonrpc streaming"}},
			"c": {ID: "c", Fields: map[string]any{"content": "voice asr tts"}},
		}},
		TopK: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := sr.Retrieve(context.Background(), "a2a", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("expected hits")
	}
}

func TestStrategyRetriever_KeywordOnly(t *testing.T) {
	sr, err := retrieve.New(retrieve.Config{
		Strategy: retrieve.StrategyKeyword,
		Search: &memSearch{docs: map[string]search.Doc{
			"x": {ID: "x", Fields: map[string]any{"content": "mailbox channel"}},
		}},
		TopK: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := sr.Retrieve(context.Background(), "mailbox", 1)
	if err != nil || len(out) != 1 || out[0].ID != "x" {
		t.Fatalf("out=%v err=%v", out, err)
	}
}
