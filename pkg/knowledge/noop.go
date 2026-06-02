package knowledge

import "context"

// NoopHandler is a vector-store stub for keyword-only indexing demos and tests.
// Upsert/Delete/List are no-ops; Query returns empty results.
type NoopHandler struct{}

func (NoopHandler) Provider() string { return "noop" }

func (NoopHandler) Upsert(context.Context, []Record, *UpsertOptions) error { return nil }

func (NoopHandler) Query(context.Context, string, *QueryOptions) ([]QueryResult, error) {
	return nil, nil
}

func (NoopHandler) Get(context.Context, []string, *GetOptions) ([]Record, error) { return nil, nil }

func (NoopHandler) List(context.Context, *ListOptions) (*ListResult, error) { return nil, nil }

func (NoopHandler) Delete(context.Context, []string, *DeleteOptions) error { return nil }

func (NoopHandler) Ping(context.Context) error { return nil }

func (NoopHandler) CreateNamespace(context.Context, string) error { return nil }

func (NoopHandler) DeleteNamespace(context.Context, string) error { return nil }

func (NoopHandler) ListNamespaces(context.Context) ([]string, error) { return nil, nil }

var _ KnowledgeHandler = NoopHandler{}
