package compose

import (
	"context"
	"errors"

	"github.com/LingByte/LingVoice/pkg/llm/internal/core"
)

// InterruptCtx records one resumable interrupt point (Eino compose.InterruptCtx subset).
type InterruptCtx struct {
	ID    string `json:"id"`
	Node  string `json:"node,omitempty"`
	Info  any    `json:"info,omitempty"`
	State any    `json:"state,omitempty"`
}

// InterruptInfo is returned when graph execution pauses (Eino compose.InterruptInfo subset).
type InterruptInfo struct {
	State             *GraphState     `json:"state,omitempty"`
	BeforeNodes       []string        `json:"before_nodes,omitempty"`
	AfterNodes        []string        `json:"after_nodes,omitempty"`
	RerunNodes        []string        `json:"rerun_nodes,omitempty"`
	InterruptContexts []*InterruptCtx `json:"interrupt_contexts,omitempty"`
	SubGraphs         map[string]*InterruptInfo `json:"sub_graphs,omitempty"`
}

// InterruptError re-exports core interrupt error.
type InterruptError = core.InterruptError

// Interrupt pauses execution with user-facing info.
func Interrupt(ctx context.Context, info any) error {
	return core.Interrupt(ctx, info)
}

// StatefulInterrupt pauses and saves local component state for resume.
func StatefulInterrupt(ctx context.Context, info, state any) error {
	return core.StatefulInterrupt(ctx, info, state)
}

// ExtractInterruptInfo unwraps interrupt metadata from err.
func ExtractInterruptInfo(err error) (*InterruptInfo, bool) {
	if ie, ok := core.AsInterrupt(err); ok {
		return &InterruptInfo{
			InterruptContexts: []*InterruptCtx{{
				ID: ie.ID, Node: ie.Node, Info: ie.Info, State: ie.State,
			}},
		}, true
	}
	var wrapped *interruptWrap
	if errors.As(err, &wrapped) {
		return wrapped.info, true
	}
	return nil, false
}

type interruptWrap struct {
	info *InterruptInfo
}

func (e *interruptWrap) Error() string {
	return "compose: interrupted"
}

func wrapInterruptInfo(info *InterruptInfo) error {
	if info == nil {
		return core.Interrupt(nil, nil)
	}
	return &interruptWrap{info: info}
}

type interruptResumeKey struct{}

// BatchResumeWithData injects resume payloads keyed by interrupt ID.
func BatchResumeWithData(ctx context.Context, data map[string]any) context.Context {
	return core.WithResumeData(ctx, data)
}

// ResumeWithData resumes one interrupt point with data.
func ResumeWithData(ctx context.Context, interruptID string, data any) context.Context {
	m := core.ResumeData(ctx)
	next := make(map[string]any, len(m)+1)
	for k, v := range m {
		next[k] = v
	}
	next[interruptID] = data
	return core.WithResumeData(ctx, next)
}

// GetResumeContext returns resume data for interruptID when this run is a resume target.
func GetResumeContext[T any](ctx context.Context, interruptID string) (isTarget, hasData bool, data T) {
	m := core.ResumeData(ctx)
	if m == nil {
		return false, false, data
	}
	v, ok := m[interruptID]
	if !ok {
		return false, false, data
	}
	d, ok := v.(T)
	return true, ok, d
}

// GetInterruptState reports whether the node is re-entering after StatefulInterrupt.
func GetInterruptState[T any](ctx context.Context) (wasInterrupted bool, info any, state T) {
	if ctx == nil {
		return false, nil, state
	}
	if r, ok := ctx.Value(interruptResumeKey{}).(*InterruptCtx); ok && r != nil {
		st, _ := r.State.(T)
		return true, r.Info, st
	}
	return false, nil, state
}

func withInterruptResume(ctx context.Context, ic *InterruptCtx) context.Context {
	if ic == nil {
		return ctx
	}
	return context.WithValue(ctx, interruptResumeKey{}, ic)
}

// ActiveInterruptID returns the interrupt ID when resuming into an interrupted node.
func ActiveInterruptID(ctx context.Context) string {
	if r, ok := ctx.Value(interruptResumeKey{}).(*InterruptCtx); ok && r != nil {
		return r.ID
	}
	return ""
}

func newInterruptID() string { return core.NewInterruptID() }
