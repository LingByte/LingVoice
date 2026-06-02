package metrics

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// RunRecord is one ChatModel invocation metric snapshot.
type RunRecord struct {
	ID            string             `json:"id"`
	RunName       string             `json:"run_name,omitempty"`
	Component     string             `json:"component,omitempty"`
	ProviderType  string             `json:"provider_type,omitempty"`
	Model         string             `json:"model"`
	Stream        bool               `json:"stream"`
	StartedAt     time.Time          `json:"started_at"`
	EndedAt       time.Time          `json:"ended_at"`
	Duration      time.Duration      `json:"-"`
	DurationMs    float64            `json:"duration_ms"`
	InputMessages int                `json:"input_messages"`
	Usage         *schema.TokenUsage `json:"usage,omitempty"`
	FinishReason  string             `json:"finish_reason,omitempty"`

	// Error classification
	Error      string    `json:"error,omitempty"`
	ErrorType  ErrorType `json:"error_type,omitempty"`
	ErrorCode  string    `json:"error_code,omitempty"`

	// Latency (TTFT essential for streaming)
	TTFTMs            float64 `json:"ttft_ms,omitempty"`
	UpstreamLatencyMs float64 `json:"upstream_latency_ms,omitempty"`
	TokensPerSecond   float64 `json:"tokens_per_second,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`
}

// Snapshot aggregates store statistics (callable by framework users).
type Snapshot struct {
	TotalRuns            int64              `json:"total_runs"`
	SuccessRuns          int64              `json:"success_runs"`
	ErrorRuns            int64              `json:"error_runs"`
	ErrorsByType         map[string]int64   `json:"errors_by_type,omitempty"`
	TotalTokens          int64              `json:"total_tokens"`
	PromptTokens         int64              `json:"prompt_tokens"`
	CompletionTokens     int64              `json:"completion_tokens"`
	AvgDurationMs        float64            `json:"avg_duration_ms"`
	AvgTTFTMs            float64            `json:"avg_ttft_ms"`
	AvgUpstreamLatencyMs float64            `json:"avg_upstream_latency_ms"`
	AvgTokensPerSecond   float64            `json:"avg_tokens_per_second"`
}

// Store records and queries RunRecord asynchronously.
type Store interface {
	// Enqueue schedules async persistence (non-blocking).
	Enqueue(record RunRecord)
	// Get returns a recorded run by id.
	Get(id string) (RunRecord, bool)
	// List returns recent runs newest-first, up to limit (0 = all).
	List(limit int) []RunRecord
	// Snapshot returns aggregate metrics.
	Snapshot() Snapshot
	// Close stops the background worker.
	Close()
}

type runIDKey struct{}
type runSlotKey struct{}

// RunSlot is filled by instrumented models during a call.
type RunSlot struct {
	ID string
}

// WithRunSlot attaches a slot pointer callers can read after Generate/Stream setup.
func WithRunSlot(ctx context.Context) (context.Context, *RunSlot) {
	slot := &RunSlot{}
	return context.WithValue(ctx, runSlotKey{}, slot), slot
}

func slotFrom(ctx context.Context) *RunSlot {
	if s, ok := ctx.Value(runSlotKey{}).(*RunSlot); ok {
		return s
	}
	return nil
}

// WithRunID stores the active run id in context (for callers after Generate).
func WithRunID(ctx context.Context, id string) context.Context {
	if slot := slotFrom(ctx); slot != nil {
		slot.ID = id
	}
	return context.WithValue(ctx, runIDKey{}, id)
}

// RunIDFromContext returns the run id set by instrumented ChatModel.
func RunIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(runIDKey{}).(string); ok {
		return v
	}
	return ""
}

// MemoryStore is an in-process async metrics store.
type MemoryStore struct {
	ch        chan RunRecord
	done      chan struct{}
	closed    atomic.Bool
	mu        sync.RWMutex
	byID      map[string]RunRecord
	order     []string
	snap      Snapshot
	maxLen    int
	toolByID  map[string]ToolRunRecord
	toolOrder []string
	toolTotal int64
}

// MemoryStoreOption configures MemoryStore.
type MemoryStoreOption func(*MemoryStore)

// WithMaxRecords caps retained history (default 1000).
func WithMaxRecords(n int) MemoryStoreOption {
	return func(s *MemoryStore) {
		if n > 0 {
			s.maxLen = n
		}
	}
}

// WithQueueSize sets enqueue buffer (default 256).
func WithQueueSize(n int) MemoryStoreOption {
	return func(s *MemoryStore) {
		if n > 0 {
			s.ch = make(chan RunRecord, n)
		}
	}
}

// NewMemoryStore starts a background recorder goroutine.
func NewMemoryStore(opts ...MemoryStoreOption) *MemoryStore {
	s := &MemoryStore{
		ch:       make(chan RunRecord, 256),
		done:     make(chan struct{}),
		byID:     make(map[string]RunRecord),
		toolByID: make(map[string]ToolRunRecord),
		maxLen:   1000,
		snap:     Snapshot{ErrorsByType: make(map[string]int64)},
	}
	for _, o := range opts {
		o(s)
	}
	go s.worker()
	return s
}

func (s *MemoryStore) worker() {
	defer close(s.done)
	for rec := range s.ch {
		s.persist(rec)
	}
}

func (s *MemoryStore) persist(rec RunRecord) {
	if rec.DurationMs == 0 && rec.Duration > 0 {
		rec.DurationMs = float64(rec.Duration) / float64(time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byID[rec.ID]; !exists {
		s.order = append(s.order, rec.ID)
	}
	s.byID[rec.ID] = rec
	for len(s.order) > s.maxLen {
		old := s.order[0]
		s.order = s.order[1:]
		delete(s.byID, old)
	}
	s.snap.TotalRuns++
	if rec.Error != "" {
		s.snap.ErrorRuns++
		if rec.ErrorType != "" {
			s.snap.ErrorsByType[string(rec.ErrorType)]++
		} else {
			s.snap.ErrorsByType[string(ErrorTypeUnknown)]++
		}
	} else {
		s.snap.SuccessRuns++
	}
	if rec.Usage != nil {
		s.snap.TotalTokens += int64(rec.Usage.TotalTokens)
		s.snap.PromptTokens += int64(rec.Usage.PromptTokens)
		s.snap.CompletionTokens += int64(rec.Usage.CompletionTokens)
	}
	n := float64(s.snap.TotalRuns)
	prev := n - 1
	if n > 0 {
		s.snap.AvgDurationMs = (s.snap.AvgDurationMs*prev + rec.DurationMs) / n
		if rec.TTFTMs > 0 {
			s.snap.AvgTTFTMs = (s.snap.AvgTTFTMs*prev + rec.TTFTMs) / n
		}
		if rec.UpstreamLatencyMs > 0 {
			s.snap.AvgUpstreamLatencyMs = (s.snap.AvgUpstreamLatencyMs*prev + rec.UpstreamLatencyMs) / n
		}
		if rec.TokensPerSecond > 0 {
			s.snap.AvgTokensPerSecond = (s.snap.AvgTokensPerSecond*prev + rec.TokensPerSecond) / n
		}
	}
}

func (s *MemoryStore) Enqueue(rec RunRecord) {
	if s == nil || s.closed.Load() {
		return
	}
	select {
	case s.ch <- rec:
	default:
		go func(r RunRecord) {
			if s.closed.Load() {
				return
			}
			select {
			case s.ch <- r:
			default:
			}
		}(rec)
	}
}

func (s *MemoryStore) Get(id string) (RunRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.byID[id]
	return r, ok
}

func (s *MemoryStore) List(limit int) []RunRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := len(s.order)
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]RunRecord, 0, limit)
	for i := n - 1; i >= 0 && len(out) < limit; i-- {
		if r, ok := s.byID[s.order[i]]; ok {
			out = append(out, r)
		}
	}
	return out
}

func (s *MemoryStore) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.snap
	if len(out.ErrorsByType) > 0 {
		out.ErrorsByType = copyMap(out.ErrorsByType)
	}
	return out
}

func copyMap(m map[string]int64) map[string]int64 {
	c := make(map[string]int64, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func (s *MemoryStore) Close() {
	if s == nil || !s.closed.CompareAndSwap(false, true) {
		return
	}
	close(s.ch)
	<-s.done
}

// Default is the process-wide metrics store (optional, set by app bootstrap).
var Default *MemoryStore

func init() {
	Default = NewMemoryStore()
}
