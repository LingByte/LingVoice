package knowledge

import (
	"context"
	"strings"
	"sync"
	"testing"
)

type captureHandler struct {
	mu      sync.Mutex
	records []Record
}

func (h *captureHandler) Provider() string { return "capture" }

func (h *captureHandler) Upsert(_ context.Context, records []Record, _ *UpsertOptions) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, records...)
	return nil
}

func (h *captureHandler) Query(context.Context, string, *QueryOptions) ([]QueryResult, error) {
	return nil, nil
}
func (h *captureHandler) Get(context.Context, []string, *GetOptions) ([]Record, error) {
	return nil, nil
}
func (h *captureHandler) List(context.Context, *ListOptions) (*ListResult, error) {
	return nil, nil
}
func (h *captureHandler) Delete(context.Context, []string, *DeleteOptions) error { return nil }
func (h *captureHandler) Ping(context.Context) error                           { return nil }
func (h *captureHandler) CreateNamespace(context.Context, string) error        { return nil }
func (h *captureHandler) DeleteNamespace(context.Context, string) error        { return nil }
func (h *captureHandler) ListNamespaces(context.Context) ([]string, error)     { return nil, nil }

func (h *captureHandler) snapshot() []Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Record, len(h.records))
	copy(out, h.records)
	return out
}

type countingChunker struct {
	name string
}

func (c countingChunker) Provider() string { return c.name }
func (c countingChunker) Chunk(_ context.Context, text string, _ *ChunkOptions) ([]Chunk, error) {
	return []Chunk{{Text: text}}, nil
}

func TestRoutingChunker_ForcedStrategy(t *testing.T) {
	rc := &RoutingChunker{
		Structured: countingChunker{name: "structured"},
		TableKV:    countingChunker{name: "table_kv"},
		LLM:        countingChunker{name: "llm"},
	}
	out, err := rc.Chunk(context.Background(), "name: value\nage: 30", &ChunkOptions{
		Strategy: ChunkStrategyStructured,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Metadata["chunk_strategy"] != "structured" {
		t.Fatalf("strategy=%v", out[0].Metadata["chunk_strategy"])
	}
}

func TestIndexer_UsesStrategyPerDocument(t *testing.T) {
	store := &captureHandler{}
	idx, err := NewIndexer(store, DefaultRoutingChunker(nil))
	if err != nil {
		t.Fatal(err)
	}

	tableDoc := "| name | age |\n| --- | --- |\n| Ada | 30 |\n| Bob | 31 |"
	structuredDoc := "# Intro\n\nParagraph one.\n\n## Section\n\nParagraph two is longer."

	results, err := idx.IndexDocuments(context.Background(), []DocumentInput{
		{ID: "table-1", Title: "People", Content: tableDoc, Strategy: ChunkStrategyTableKV},
		{ID: "doc-1", Title: "Manual", Content: structuredDoc},
	}, &IndexOptions{Namespace: "demo", Chunk: &ChunkOptions{MaxChars: 80}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].Strategy != "table_kv" {
		t.Fatalf("table strategy=%q", results[0].Strategy)
	}
	if results[1].DocType != "structured" {
		t.Fatalf("doc type=%q", results[1].DocType)
	}

	recs := store.snapshot()
	if len(recs) == 0 {
		t.Fatal("expected indexed records")
	}
	for _, r := range recs {
		if !strings.HasPrefix(r.ID, "table-1#") && !strings.HasPrefix(r.ID, "doc-1#") {
			t.Fatalf("unexpected id %q", r.ID)
		}
		if r.Metadata["parent_id"] == nil {
			t.Fatalf("missing parent_id on %q", r.ID)
		}
	}
}

func TestIndexer_ChunkDocumentAutoDetectsTable(t *testing.T) {
	idx, err := NewIndexer(&captureHandler{}, DefaultRoutingChunker(nil))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join([]string{
		"name: Alice",
		"role: engineer",
		"team: platform",
		"level: senior",
		"location: Shanghai",
		"email: a@example.com",
	}, "\n")
	chunks, err := idx.ChunkDocument(context.Background(), DocumentInput{
		ID: "kv-1", Content: text,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if chunks[0].Metadata["document_type"] != "table_kv" {
		t.Fatalf("type=%v", chunks[0].Metadata["document_type"])
	}
}

func TestNewIndexer_NilHandler(t *testing.T) {
	if _, err := NewIndexer(nil, nil); err == nil {
		t.Fatal("expected error")
	}
}
