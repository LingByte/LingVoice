package knowledge

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
)

// MemoryHandler is an in-process vector store for local demos and tests.
type MemoryHandler struct {
	Embedder Embedder
	mu       sync.RWMutex
	// namespace -> record id -> record
	stores map[string]map[string]Record
}

// NewMemoryHandler builds an in-memory handler. Embedder is required for Upsert/Query.
func NewMemoryHandler(embedder Embedder) (*MemoryHandler, error) {
	if embedder == nil {
		return nil, ErrEmbedderNotFound
	}
	return &MemoryHandler{
		Embedder: embedder,
		stores:   map[string]map[string]Record{},
	}, nil
}

func (h *MemoryHandler) Provider() string { return ProviderMemory }

func (h *MemoryHandler) Upsert(ctx context.Context, records []Record, opts *UpsertOptions) error {
	if h == nil {
		return ErrHandlerNotFound
	}
	if len(records) == 0 {
		return nil
	}
	ns, err := h.namespaceFrom(opts)
	if err != nil {
		return err
	}
	texts := make([]string, len(records))
	for i, rec := range records {
		texts[i] = rec.Content
	}
	vecs, err := h.Embedder.Embed(ctx, texts)
	if err != nil {
		return err
	}
	if len(vecs) != len(records) {
		return ErrInvalidVectorDimension
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	store := h.storeLocked(ns)
	for i, rec := range records {
		vec := float64ToFloat32(vecs[i])
		normalizeVec32(vec)
		copy := rec
		copy.Vector = vec
		store[rec.ID] = copy
	}
	return nil
}

func (h *MemoryHandler) Query(ctx context.Context, text string, opts *QueryOptions) ([]QueryResult, error) {
	if h == nil {
		return nil, ErrHandlerNotFound
	}
	if strings.TrimSpace(text) == "" {
		return nil, ErrEmptyQuery
	}
	if h.Embedder == nil {
		return nil, ErrEmbedderNotFound
	}
	ns := ""
	topK := 10
	minScore := 0.0
	var filters []Filter
	if opts != nil {
		ns = opts.Namespace
		if opts.TopK > 0 {
			topK = opts.TopK
		}
		minScore = opts.MinScore
		filters = opts.Filters
	}
	if ns == "" {
		return nil, ErrCollectionNotFound
	}

	vecs, err := h.Embedder.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, ErrInvalidVectorDimension
	}
	q32 := float64ToFloat32(vecs[0])
	normalizeVec32(q32)

	h.mu.RLock()
	store := h.stores[ns]
	candidates := make([]Record, 0, len(store))
	for _, rec := range store {
		if !recordMatchesFilters(rec, filters) {
			continue
		}
		candidates = append(candidates, rec)
	}
	h.mu.RUnlock()

	type scored struct {
		rec   Record
		score float64
	}
	ranked := make([]scored, 0, len(candidates))
	for _, rec := range candidates {
		if len(rec.Vector) == 0 {
			continue
		}
		s := cosineSimilarity32(q32, rec.Vector)
		if s < minScore {
			continue
		}
		ranked = append(ranked, scored{rec: rec, score: s})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if len(ranked) > topK {
		ranked = ranked[:topK]
	}
	out := make([]QueryResult, len(ranked))
	for i, s := range ranked {
		out[i] = QueryResult{Record: s.rec, Score: s.score}
	}
	return out, nil
}

func (h *MemoryHandler) Get(_ context.Context, ids []string, opts *GetOptions) ([]Record, error) {
	if h == nil {
		return nil, ErrHandlerNotFound
	}
	ns := ""
	if opts != nil {
		ns = opts.Namespace
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	store := h.stores[ns]
	var out []Record
	for _, id := range ids {
		if rec, ok := store[id]; ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (h *MemoryHandler) List(_ context.Context, opts *ListOptions) (*ListResult, error) {
	if h == nil {
		return nil, ErrHandlerNotFound
	}
	ns := ""
	limit := 20
	var filters []Filter
	if opts != nil {
		ns = opts.Namespace
		if opts.Limit > 0 {
			limit = opts.Limit
		}
		filters = opts.Filters
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	store := h.stores[ns]
	out := make([]Record, 0, len(store))
	for _, rec := range store {
		if !recordMatchesFilters(rec, filters) {
			continue
		}
		out = append(out, rec)
		if len(out) >= limit {
			break
		}
	}
	return &ListResult{Records: out}, nil
}

func (h *MemoryHandler) Delete(_ context.Context, ids []string, opts *DeleteOptions) error {
	if h == nil {
		return ErrHandlerNotFound
	}
	ns := ""
	if opts != nil {
		ns = opts.Namespace
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	store := h.storeLocked(ns)
	for _, id := range ids {
		delete(store, id)
	}
	return nil
}

func (h *MemoryHandler) Ping(context.Context) error {
	if h == nil {
		return ErrHandlerNotFound
	}
	return nil
}

func (h *MemoryHandler) CreateNamespace(_ context.Context, name string) error {
	if h == nil {
		return ErrHandlerNotFound
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrCollectionNotFound
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stores[name] == nil {
		h.stores[name] = map[string]Record{}
	}
	return nil
}

func (h *MemoryHandler) DeleteNamespace(_ context.Context, name string) error {
	if h == nil {
		return ErrHandlerNotFound
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.stores, strings.TrimSpace(name))
	return nil
}

func (h *MemoryHandler) ListNamespaces(_ context.Context) ([]string, error) {
	if h == nil {
		return nil, ErrHandlerNotFound
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.stores))
	for ns := range h.stores {
		out = append(out, ns)
	}
	return out, nil
}

func (h *MemoryHandler) namespaceFrom(opts *UpsertOptions) (string, error) {
	ns := ""
	if opts != nil {
		ns = strings.TrimSpace(opts.Namespace)
	}
	if ns == "" {
		return "", ErrCollectionNotFound
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.storeLocked(ns)
	return ns, nil
}

func (h *MemoryHandler) storeLocked(ns string) map[string]Record {
	if h.stores[ns] == nil {
		h.stores[ns] = map[string]Record{}
	}
	return h.stores[ns]
}

func normalizeVec32(v []float32) {
	if len(v) == 0 {
		return
	}
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum <= 0 {
		return
	}
	n := math.Sqrt(sum)
	for i := range v {
		v[i] = float32(float64(v[i]) / n)
	}
}

func float64ToFloat32(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x)
	}
	return out
}

func cosineSimilarity32(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot float64
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}

func recordMatchesFilters(rec Record, filters []Filter) bool {
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		if !recordMatchesFilter(rec, f) {
			return false
		}
	}
	return true
}

func recordMatchesFilter(rec Record, f Filter) bool {
	var got any
	switch f.Field {
	case "parent_id", "source", "title":
		if f.Field == "title" {
			got = rec.Title
		} else if f.Field == "source" {
			got = rec.Source
		} else {
			got = rec.Metadata[f.Field]
		}
	default:
		got = rec.Metadata[f.Field]
	}
	switch f.Operator {
	case FilterOpEqual:
		return len(f.Value) > 0 && fmt.Sprint(f.Value[0]) == fmt.Sprint(got)
	case FilterOpIn:
		s := fmt.Sprint(got)
		for _, v := range f.Value {
			if fmt.Sprint(v) == s {
				return true
			}
		}
		return false
	default:
		return true
	}
}

var _ KnowledgeHandler = (*MemoryHandler)(nil)
