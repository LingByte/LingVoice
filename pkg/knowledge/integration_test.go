package knowledge_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/knowledge"
	"github.com/LingByte/LingVoice/pkg/knowledge/embed"
	"github.com/LingByte/LingVoice/pkg/knowledge/retrieve"
	"github.com/LingByte/LingVoice/pkg/search"
)

func TestIndexDocuments_WithSearchEngine(t *testing.T) {
	dir := t.TempDir()
	idxPath := filepath.Join(dir, "bleve")
	engine, err := search.New(search.Config{
		IndexPath:           idxPath,
		DefaultSearchFields: []string{"title", "content", "body"},
		QueryTimeout:        5 * time.Second,
	}, search.BuildIndexMapping(""))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	store := &captureHandler{}
	idx, err := knowledge.NewIndexerFromConfig(knowledge.IndexerConfig{
		Handler: store,
		Search:  engine,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = idx.IndexDocuments(context.Background(), []knowledge.DocumentInput{
		{ID: "d1", Title: "Pregel", Content: "# Pregel\n\nMailbox channels in BSP graph runs.", Strategy: knowledge.ChunkStrategyStructured},
	}, &knowledge.IndexOptions{Namespace: "demo", Chunk: &knowledge.ChunkOptions{MaxChars: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.snapshot()) == 0 {
		t.Fatal("expected vector records")
	}

	sr, err := retrieve.New(retrieve.Config{
		Strategy: retrieve.StrategyKeyword,
		Search:   engine,
		TopK:     3,
	})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := sr.Retrieve(context.Background(), "Mailbox", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) == 0 {
		t.Fatal("expected keyword hit")
	}
}

func TestNewHandler_WithEmbedConfig(t *testing.T) {
	h, err := knowledge.NewHandler(knowledge.HandlerConfig{
		Provider: knowledge.ProviderQdrant,
		Qdrant:   knowledge.QdrantConfig{BaseURL: "http://127.0.0.1:6333"},
		Embed: embed.Config{
			Provider: embed.ProviderFunc,
			Func: embed.FuncConfig{
				Fn: func(_ context.Context, inputs []string) ([][]float64, error) {
					out := make([][]float64, len(inputs))
					for i := range inputs {
						out[i] = []float64{0.1, 0.2}
					}
					return out, nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if h == nil {
		t.Fatal("nil handler")
	}
}

// captureHandler from index_test - duplicate minimal version
type captureHandler struct{ records []knowledge.Record }

func (h *captureHandler) Provider() string { return "capture" }
func (h *captureHandler) Upsert(_ context.Context, records []knowledge.Record, _ *knowledge.UpsertOptions) error {
	h.records = append(h.records, records...)
	return nil
}
func (h *captureHandler) Query(context.Context, string, *knowledge.QueryOptions) ([]knowledge.QueryResult, error) {
	return nil, nil
}
func (h *captureHandler) Get(context.Context, []string, *knowledge.GetOptions) ([]knowledge.Record, error) {
	return nil, nil
}
func (h *captureHandler) List(context.Context, *knowledge.ListOptions) (*knowledge.ListResult, error) {
	return nil, nil
}
func (h *captureHandler) Delete(context.Context, []string, *knowledge.DeleteOptions) error { return nil }
func (h *captureHandler) Ping(context.Context) error                                       { return nil }
func (h *captureHandler) CreateNamespace(context.Context, string) error                    { return nil }
func (h *captureHandler) DeleteNamespace(context.Context, string) error                  { return nil }
func (h *captureHandler) ListNamespaces(context.Context) ([]string, error)                 { return nil, nil }
func (h *captureHandler) snapshot() []knowledge.Record {
	out := make([]knowledge.Record, len(h.records))
	copy(out, h.records)
	return out
}

var _ = os.TempDir
