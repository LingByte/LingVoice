package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// CheckPointStore persists graph checkpoints (Eino compose.CheckpointStore subset).
type CheckPointStore interface {
	Get(ctx context.Context, key string) (value []byte, existed bool, err error)
	Set(ctx context.Context, key string, value []byte) error
}

// CheckPointDeleter removes checkpoints after successful completion (Eino subset).
type CheckPointDeleter interface {
	Delete(ctx context.Context, key string) error
}

// Checkpoint is serialized graph execution state.
type Checkpoint struct {
	GraphName string            `json:"graph_name"`
	NextNode  string            `json:"next_node"`
	State     *GraphState       `json:"state"`
	Pregel    *PregelCheckpoint `json:"pregel,omitempty"`
	Interrupt *InterruptInfo    `json:"interrupt,omitempty"`
	SavedAt   time.Time         `json:"saved_at"`
}

// MemoryCheckPointStore is an in-memory CheckPointStore for development.
type MemoryCheckPointStore struct {
	mu sync.RWMutex
	m  map[string][]byte
}

// NewMemoryCheckPointStore creates an empty in-memory store.
func NewMemoryCheckPointStore() *MemoryCheckPointStore {
	return &MemoryCheckPointStore{m: make(map[string][]byte)}
}

func (s *MemoryCheckPointStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	if s == nil {
		return nil, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.m[key]
	if !ok {
		return nil, false, nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out, true, nil
}

func (s *MemoryCheckPointStore) Set(_ context.Context, key string, value []byte) error {
	if s == nil {
		return fmt.Errorf("compose: nil CheckPointStore")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b := make([]byte, len(value))
	copy(b, value)
	s.m[key] = b
	return nil
}

// Delete removes a checkpoint key.
func (s *MemoryCheckPointStore) Delete(_ context.Context, key string) error {
	if s == nil {
		return fmt.Errorf("compose: nil CheckPointStore")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

func marshalCheckpoint(cp *Checkpoint) ([]byte, error) {
	if cp == nil {
		return nil, fmt.Errorf("compose: nil checkpoint")
	}
	return json.Marshal(cp)
}

func unmarshalCheckpoint(b []byte) (*Checkpoint, error) {
	var cp Checkpoint
	if err := json.Unmarshal(b, &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

// TransformMode controls non-ReAct Transform behavior.
type TransformMode int

const (
	// TransformPerChunk invokes once per stream chunk (legacy subset).
	TransformPerChunkMode TransformMode = iota
	// TransformBatchConcat drains input then invokes once (Eino default batch).
	TransformBatchConcatMode
)

type graphCompileConfig struct {
	checkpointStore   CheckPointStore
	interruptBefore   map[string]bool
	interruptAfter    map[string]bool
	clearOnComplete   bool
	runMode           GraphRunMode
	compileCallback   CompileCallback
	nodeTrigger       map[string]NodeTriggerMode
	pregelMerge       map[string]PregelChannelMerge
	channelSpecs      map[string]ChannelSpec
	transformMode     TransformMode
}

// WithGraphRunMode overrides the graph run mode at compile time.
func WithGraphRunMode(mode GraphRunMode) GraphCompileOption {
	return func(c *graphCompileConfig) {
		c.runMode = mode
	}
}

// GraphCompileOption configures graph compilation.
type GraphCompileOption func(*graphCompileConfig)

// WithCheckPointStore binds checkpoint persistence (Eino compose.WithCheckPointStore).
func WithCheckPointStore(store CheckPointStore) GraphCompileOption {
	return func(c *graphCompileConfig) {
		c.checkpointStore = store
	}
}

// WithInterruptBeforeNodes pauses before listed nodes run.
func WithInterruptBeforeNodes(nodes ...string) GraphCompileOption {
	return func(c *graphCompileConfig) {
		if c.interruptBefore == nil {
			c.interruptBefore = make(map[string]bool)
		}
		for _, n := range nodes {
			c.interruptBefore[n] = true
		}
	}
}

// WithClearCheckpointOnCompleteCompile deletes checkpoints when graphs finish successfully.
func WithClearCheckpointOnCompleteCompile() GraphCompileOption {
	return func(c *graphCompileConfig) {
		c.clearOnComplete = true
	}
}

// WithInterruptAfterNodes pauses after listed nodes complete.
func WithInterruptAfterNodes(nodes ...string) GraphCompileOption {
	return func(c *graphCompileConfig) {
		if c.interruptAfter == nil {
			c.interruptAfter = make(map[string]bool)
		}
		for _, n := range nodes {
			c.interruptAfter[n] = true
		}
	}
}

type graphInvokeConfig struct {
	checkpointID     string
	stateModifier    func(context.Context, *GraphState) error
	resumeData       map[string]any
	chatModelOpts    []llm.Option
	clearCheckpoint  bool
	streamCheckpoint bool
	pregelCheckpoint bool
}

// GraphInvokeOption configures a single graph invocation.
type GraphInvokeOption func(*graphInvokeConfig)

// WithCheckPointID sets the checkpoint key for save/resume (Eino compose.WithCheckPointID).
func WithCheckPointID(id string) GraphInvokeOption {
	return func(c *graphInvokeConfig) {
		c.checkpointID = id
	}
}

// WithStateModifier mutates state when resuming from a checkpoint.
func WithStateModifier(fn func(context.Context, *GraphState) error) GraphInvokeOption {
	return func(c *graphInvokeConfig) {
		c.stateModifier = fn
	}
}

// WithResumeData passes resume payloads keyed by interrupt ID.
func WithResumeData(data map[string]any) GraphInvokeOption {
	return func(c *graphInvokeConfig) {
		c.resumeData = data
	}
}

// WithClearCheckpointOnComplete deletes the checkpoint after a successful END.
func WithClearCheckpointOnComplete() GraphInvokeOption {
	return func(c *graphInvokeConfig) {
		c.clearCheckpoint = true
	}
}

func applyInvokeOptions(opts ...GraphInvokeOption) graphInvokeConfig {
	var cfg graphInvokeConfig
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	return cfg
}

func (r *CompiledGraph) saveCheckpoint(ctx context.Context, id string, cp *Checkpoint) error {
	if r == nil || r.checkpointStore == nil || id == "" {
		return nil
	}
	if cp == nil {
		return fmt.Errorf("compose: nil checkpoint")
	}
	cp.GraphName = r.name
	cp.SavedAt = time.Now()
	b, err := marshalCheckpoint(cp)
	if err != nil {
		return err
	}
	return r.checkpointStore.Set(ctx, id, b)
}

func (r *CompiledGraph) loadCheckpoint(ctx context.Context, id string) (*Checkpoint, bool, error) {
	if r == nil || r.checkpointStore == nil || id == "" {
		return nil, false, nil
	}
	b, ok, err := r.checkpointStore.Get(ctx, id)
	if err != nil || !ok {
		return nil, false, err
	}
	cp, err := unmarshalCheckpoint(b)
	if err != nil {
		return nil, false, err
	}
	return cp, true, nil
}

func cloneGraphState(st *GraphState) *GraphState {
	if st == nil {
		return &GraphState{Vars: map[string]any{}}
	}
	out := &GraphState{
		Messages:   append([]*schema.Message(nil), st.Messages...),
		LastOutput: st.LastOutput,
		Vars:       make(map[string]any, len(st.Vars)),
	}
	for k, v := range st.Vars {
		out.Vars[k] = v
	}
	return out
}
