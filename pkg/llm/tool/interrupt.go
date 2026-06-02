package tool

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/llm/internal/core"
)

// Interrupt pauses tool execution for HITL (Eino components/tool.Interrupt).
func Interrupt(ctx context.Context, info any) error {
	return core.Interrupt(ctx, info)
}

// StatefulInterrupt pauses and persists tool-local state.
func StatefulInterrupt(ctx context.Context, info, state any) error {
	return core.StatefulInterrupt(ctx, info, state)
}

// WithResumeData injects resume payloads for tool resume.
func WithResumeData(ctx context.Context, data map[string]any) context.Context {
	return core.WithResumeData(ctx, data)
}

// GetInterruptState reports re-entry after StatefulInterrupt.
func GetInterruptState[T any](ctx context.Context) (wasInterrupted bool, info any, state T) {
	if ctx == nil {
		return false, nil, state
	}
	if r, ok := ctx.Value(toolResumeKey{}).(*toolResume); ok && r != nil {
		st, _ := r.State.(T)
		return true, r.Info, st
	}
	return false, nil, state
}

// GetResumeContext returns resume payload for interruptID.
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

// ToolCallID returns the active tool call id.
func ToolCallID(ctx context.Context) string {
	return core.ToolCallID(ctx)
}

type toolResumeKey struct{}

type toolResume struct {
	ID    string
	Info  any
	State any
}

// WithToolResume injects resume context for a tool re-entry.
func WithToolResume(ctx context.Context, id string, info, state any) context.Context {
	return context.WithValue(ctx, toolResumeKey{}, &toolResume{ID: id, Info: info, State: state})
}

// ActiveInterruptID returns interrupt id during tool resume.
func ActiveInterruptID(ctx context.Context) string {
	if r, ok := ctx.Value(toolResumeKey{}).(*toolResume); ok && r != nil {
		return r.ID
	}
	return ""
}

// SyncResumeFromCompose copies compose resume map into tool context.
func SyncResumeFromCompose(ctx context.Context) context.Context {
	return core.WithResumeData(ctx, core.ResumeData(ctx))
}
