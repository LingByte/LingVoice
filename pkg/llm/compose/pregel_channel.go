package compose

import (
	"fmt"
	"sync"
)

// PregelChannelKind is the eino-style channel update strategy.
type PregelChannelKind int

const (
	// ChannelKindLastValue keeps the latest value per edge (default).
	ChannelKindLastValue PregelChannelKind = iota
	// ChannelKindAppend accumulates values per edge.
	ChannelKindAppend
	// ChannelKindReduce merges values with a custom reducer.
	ChannelKindReduce
	// ChannelKindBroadcast copies one value to all target edges.
	ChannelKindBroadcast
	// ChannelKindBarrier waits until all predecessors post before release.
	ChannelKindBarrier
)

// ChannelReducer merges fan-in values (Eino channel reducer subset).
type ChannelReducer func(values []any) (any, error)

// ChannelSpec configures a fan-in node's channel behavior at compile time.
type ChannelSpec struct {
	Kind     PregelChannelKind
	Reducer  ChannelReducer
	Required int // for Barrier: number of preds that must post
}

const pregelMailboxKey = "__pregel_mailbox"

// PregelMailbox holds per-edge channel buffers for BSP fan-in (Eino mailbox).
type PregelMailbox struct {
	mu       sync.Mutex
	edges    map[string][]any
	specs    map[string]ChannelSpec
	barriers map[string]map[string]bool // target -> source -> posted
}

// NewPregelMailbox creates an empty mailbox.
func NewPregelMailbox() *PregelMailbox {
	return &PregelMailbox{
		edges:    map[string][]any{},
		specs:    map[string]ChannelSpec{},
		barriers: map[string]map[string]bool{},
	}
}

func mailboxEdgeKey(target, source string) string {
	return target + ":" + source
}

// SetSpec configures channel behavior for a fan-in target node.
func (m *PregelMailbox) SetSpec(target string, spec ChannelSpec) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.specs[target] = spec
	if spec.Kind == ChannelKindBarrier {
		if m.barriers[target] == nil {
			m.barriers[target] = map[string]bool{}
		}
	}
}

// Post delivers a value from source to target channel.
func (m *PregelMailbox) Post(target, source string, value any) {
	if m == nil || target == "" || source == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	spec := m.specs[target]
	key := mailboxEdgeKey(target, source)
	switch spec.Kind {
	case ChannelKindAppend:
		m.edges[key] = append(m.edges[key], value)
	case ChannelKindBroadcast:
		m.edges[key] = []any{value}
	default:
		m.edges[key] = []any{value}
	}
	if spec.Kind == ChannelKindBarrier {
		if m.barriers[target] == nil {
			m.barriers[target] = map[string]bool{}
		}
		m.barriers[target][source] = true
	}
}

// Collect returns merged inputs for a fan-in node; clears consumed edges.
func (m *PregelMailbox) Collect(target string, preds []string) (map[string]any, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	spec := m.specs[target]
	if spec.Kind == ChannelKindBarrier {
		req := spec.Required
		if req <= 0 {
			req = len(preds)
		}
		bar := m.barriers[target]
		if len(bar) < req {
			return nil, fmt.Errorf("compose: barrier %q waiting (%d/%d)", target, len(bar), req)
		}
	}
	out := map[string]any{}
	var reduceInputs []any
	for _, pred := range preds {
		if pred == START {
			continue
		}
		key := mailboxEdgeKey(target, pred)
		vals := m.edges[key]
		if len(vals) == 0 {
			continue
		}
		var val any
		switch spec.Kind {
		case ChannelKindAppend:
			val = append([]any(nil), vals...)
		case ChannelKindReduce:
			reduceInputs = append(reduceInputs, vals[len(vals)-1])
			val = vals[len(vals)-1]
		default:
			val = vals[len(vals)-1]
		}
		out[pred] = val
		delete(m.edges, key)
		if spec.Kind == ChannelKindBarrier {
			delete(m.barriers[target], pred)
		}
	}
	if spec.Kind == ChannelKindReduce && spec.Reducer != nil && len(reduceInputs) > 0 {
		merged, err := spec.Reducer(reduceInputs)
		if err != nil {
			return nil, err
		}
		out["__reduced__"] = merged
	}
	return out, nil
}

// Legacy PregelChannelMerge aliases ChannelKind for compile options.
type PregelChannelMerge = PregelChannelKind

const (
	ChannelLast   = ChannelKindLastValue
	ChannelAppend = ChannelKindAppend
)

func mailboxFromState(st *GraphState) *PregelMailbox {
	if st == nil || st.Vars == nil {
		return nil
	}
	mb, _ := st.Vars[pregelMailboxKey].(*PregelMailbox)
	return mb
}

func ensureMailbox(st *GraphState) *PregelMailbox {
	if st == nil {
		return nil
	}
	if st.Vars == nil {
		st.Vars = map[string]any{}
	}
	if mb, ok := st.Vars[pregelMailboxKey].(*PregelMailbox); ok && mb != nil {
		return mb
	}
	mb := NewPregelMailbox()
	st.Vars[pregelMailboxKey] = mb
	return mb
}

func applyMailboxToNode(st *GraphState, nodeName string, preds []string) error {
	mb := mailboxFromState(st)
	if mb == nil || len(preds) <= 1 {
		return nil
	}
	collected, err := mb.Collect(nodeName, preds)
	if err != nil {
		return err
	}
	if len(collected) == 0 {
		return nil
	}
	if st.Vars == nil {
		st.Vars = map[string]any{}
	}
	st.Vars["mailbox:"+nodeName] = collected
	return nil
}

func postMailboxFromNode(st *GraphState, nodeName string, succs []string) {
	mb := mailboxFromState(st)
	if mb == nil {
		return
	}
	payload := nodeOutputMap(st)
	for _, succ := range succs {
		if succ == END {
			continue
		}
		spec := mb.specs[succ]
		if spec.Kind == ChannelKindBroadcast {
			for _, other := range succs {
				if other != END {
					mb.Post(other, nodeName, payload)
				}
			}
		}
		mb.Post(succ, nodeName, payload)
	}
}

// WithPregelChannelMerge sets mailbox merge mode for fan-in nodes at compile time.
func WithPregelChannelMerge(mode PregelChannelMerge, nodes ...string) GraphCompileOption {
	return WithChannelSpec(ChannelSpec{Kind: mode}, nodes...)
}

// WithChannelSpec sets full channel spec for fan-in nodes.
func WithChannelSpec(spec ChannelSpec, nodes ...string) GraphCompileOption {
	return func(c *graphCompileConfig) {
		if c.channelSpecs == nil {
			c.channelSpecs = map[string]ChannelSpec{}
		}
		for _, n := range nodes {
			c.channelSpecs[n] = spec
		}
	}
}

// WithPregelChannelReducer sets a reduce channel for fan-in nodes.
func WithPregelChannelReducer(reducer ChannelReducer, nodes ...string) GraphCompileOption {
	return WithChannelSpec(ChannelSpec{Kind: ChannelKindReduce, Reducer: reducer}, nodes...)
}

func applyCompiledChannelSpecs(st *GraphState, specs map[string]ChannelSpec, merges map[string]PregelChannelMerge) {
	mb := ensureMailbox(st)
	for n, spec := range specs {
		mb.SetSpec(n, spec)
	}
	for n, mode := range merges {
		if _, ok := specs[n]; ok {
			continue
		}
		mb.SetSpec(n, ChannelSpec{Kind: PregelChannelKind(mode)})
	}
}

func cloneChannelSpecs(in map[string]ChannelSpec) map[string]ChannelSpec {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]ChannelSpec, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func applyCompiledPregelMerge(st *GraphState, merges map[string]PregelChannelMerge) {
	applyCompiledChannelSpecs(st, nil, merges)
}

func applyPregelMergeModes(st *GraphState, cfg *graphCompileConfig) {
	if st == nil || cfg == nil {
		return
	}
	applyCompiledChannelSpecs(st, cfg.channelSpecs, cfg.pregelMerge)
}

// WithTransformMode sets Transform behavior for compiled graphs.
func WithTransformMode(mode TransformMode) GraphCompileOption {
	return func(c *graphCompileConfig) {
		c.transformMode = mode
	}
}
