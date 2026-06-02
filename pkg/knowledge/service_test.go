package knowledge

import (
	"context"
	"sync"
	"testing"

	"github.com/LingByte/LingVoice/pkg/knowledge/retrieve"
	"github.com/LingByte/LingVoice/pkg/search"
)

type memHandler struct {
	mu      sync.Mutex
	records map[string]Record
	deleted []string
}

func (h *memHandler) Provider() string { return "mem" }

func (h *memHandler) Upsert(_ context.Context, records []Record, _ *UpsertOptions) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.records == nil {
		h.records = map[string]Record{}
	}
	for _, rec := range records {
		h.records[rec.ID] = rec
	}
	return nil
}

func (h *memHandler) Query(context.Context, string, *QueryOptions) ([]QueryResult, error) {
	return nil, nil
}

func (h *memHandler) Get(_ context.Context, ids []string, _ *GetOptions) ([]Record, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Record
	for _, id := range ids {
		if rec, ok := h.records[id]; ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (h *memHandler) List(_ context.Context, opts *ListOptions) (*ListResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Record
	for _, rec := range h.records {
		if opts != nil && len(opts.Filters) > 0 {
			pid, _ := rec.Metadata["parent_id"].(string)
			if pid != opts.Filters[0].Value[0] {
				continue
			}
		}
		out = append(out, rec)
	}
	return &ListResult{Records: out}, nil
}

func (h *memHandler) Delete(_ context.Context, ids []string, _ *DeleteOptions) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range ids {
		delete(h.records, id)
		h.deleted = append(h.deleted, id)
	}
	return nil
}

func (h *memHandler) Ping(context.Context) error                      { return nil }
func (h *memHandler) CreateNamespace(context.Context, string) error   { return nil }
func (h *memHandler) DeleteNamespace(context.Context, string) error { return nil }
func (h *memHandler) ListNamespaces(context.Context) ([]string, error) {
	return nil, nil
}

type memSearchEngine struct {
	mu      sync.Mutex
	docs    map[string]search.Doc
	deleted []string
}

func (m *memSearchEngine) Index(_ context.Context, doc search.Doc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.docs == nil {
		m.docs = map[string]search.Doc{}
	}
	m.docs[doc.ID] = doc
	return nil
}

func (m *memSearchEngine) IndexBatch(ctx context.Context, docs []search.Doc) error {
	for _, d := range docs {
		if err := m.Index(ctx, d); err != nil {
			return err
		}
	}
	return nil
}

func (m *memSearchEngine) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.docs, id)
	m.deleted = append(m.deleted, id)
	return nil
}

func (m *memSearchEngine) Search(_ context.Context, req search.SearchRequest) (search.SearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var hits []search.Hit
	for id, d := range m.docs {
		body, _ := d.Fields["content"].(string)
		if req.Keyword == "" || body == req.Keyword {
			hits = append(hits, search.Hit{ID: id, Score: 1, Fields: d.Fields})
		}
	}
	return search.SearchResult{Hits: hits}, nil
}

func (m *memSearchEngine) GetAutoCompleteSuggestions(context.Context, string) ([]string, error) {
	return nil, nil
}
func (m *memSearchEngine) GetSearchSuggestions(context.Context, string) ([]string, error) {
	return nil, nil
}
func (m *memSearchEngine) Close() error { return nil }

func TestIndexer_DeleteAndUpdateDocuments(t *testing.T) {
	store := &memHandler{}
	searchEngine := &memSearchEngine{}
	idx, err := NewIndexer(store, countingChunker{name: "structured"})
	if err != nil {
		t.Fatal(err)
	}
	idx.Search = searchEngine
	ctx := context.Background()

	doc := DocumentInput{ID: "d1", Content: "hello world", Strategy: ChunkStrategyStructured}
	res, err := idx.IndexDocuments(ctx, []DocumentInput{doc}, nil)
	if err != nil || len(res) != 1 || len(res[0].ChunkIDs) != 1 {
		t.Fatalf("index: res=%v err=%v", res, err)
	}
	if err := idx.DeleteDocuments(ctx, []string{"d1"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.deleted) != 1 || len(searchEngine.deleted) != 1 {
		t.Fatalf("deleted store=%v search=%v", store.deleted, searchEngine.deleted)
	}

	doc.Content = "updated content"
	res, err = idx.UpdateDocuments(ctx, []DocumentInput{doc}, nil)
	if err != nil || len(res) != 1 {
		t.Fatalf("update: res=%v err=%v", res, err)
	}
	store.mu.Lock()
	n := len(store.records)
	store.mu.Unlock()
	if n != 1 {
		t.Fatalf("records=%d", n)
	}
}

func TestService_IndexRetrieve(t *testing.T) {
	store := &memHandler{}
	searchEngine := &memSearchEngine{}
	svc, err := NewService(ServiceConfig{
		Handler: store,
		Indexer: IndexerConfig{
			Chunker: countingChunker{name: "structured"},
			Search:  searchEngine,
		},
		Retrieve: retrieve.Config{
			Strategy: retrieve.StrategyKeyword,
			Search:   searchEngine,
			TopK:     1,
		},
		TopK: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = svc.IndexDocuments(ctx, []DocumentInput{
		{ID: "x", Content: "mailbox", Strategy: ChunkStrategyStructured},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := svc.Retrieve(ctx, "mailbox", 1)
	if err != nil || len(docs) != 1 {
		t.Fatalf("retrieve docs=%v err=%v", docs, err)
	}
}
