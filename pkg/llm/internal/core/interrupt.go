// Package core holds shared primitives for compose and tool layers.
package core

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
)

// InterruptError signals resumable pause (Eino compose/tool interrupt).
type InterruptError struct {
	Info  any
	State any
	Node  string
	ID    string
	Addr  Address
}

func (e *InterruptError) Error() string {
	if e == nil {
		return "interrupt"
	}
	if e.Node != "" {
		return fmt.Sprintf("interrupt at %q", e.Node)
	}
	return "interrupt"
}

var interruptSeq atomic.Uint64

// NewInterruptID generates a unique interrupt id.
func NewInterruptID() string {
	return fmt.Sprintf("intr-%d", interruptSeq.Add(1))
}

// Interrupt pauses with user-facing info.
func Interrupt(_ context.Context, info any) error {
	return &InterruptError{Info: info, ID: NewInterruptID()}
}

// StatefulInterrupt pauses and saves local state for resume.
func StatefulInterrupt(_ context.Context, info, state any) error {
	return &InterruptError{Info: info, State: state, ID: NewInterruptID()}
}

// IsInterrupt reports whether err is an interrupt signal.
func IsInterrupt(err error) bool {
	var ie *InterruptError
	return errors.As(err, &ie)
}

// AsInterrupt unwraps interrupt metadata.
func AsInterrupt(err error) (*InterruptError, bool) {
	var ie *InterruptError
	if errors.As(err, &ie) {
		return ie, true
	}
	return nil, false
}

// Address identifies an interrupt point in nested graphs (Eino Address subset).
type Address []string

// String renders address as a path.
func (a Address) String() string {
	if len(a) == 0 {
		return ""
	}
	out := a[0]
	for i := 1; i < len(a); i++ {
		out += "/" + a[i]
	}
	return out
}

type addressKey struct{}
type toolCallIDKey struct{}
type resumeDataKey struct{}

// WithResumeData injects resume payloads keyed by interrupt id.
func WithResumeData(ctx context.Context, data map[string]any) context.Context {
	if len(data) == 0 {
		return ctx
	}
	return context.WithValue(ctx, resumeDataKey{}, data)
}

// ResumeData reads resume payloads from context.
func ResumeData(ctx context.Context) map[string]any {
	m, _ := ctx.Value(resumeDataKey{}).(map[string]any)
	return m
}

// WithAddress appends a segment to the current address path.
func WithAddress(ctx context.Context, segment string) context.Context {
	cur, _ := ctx.Value(addressKey{}).(Address)
	next := append(append(Address(nil), cur...), segment)
	return context.WithValue(ctx, addressKey{}, next)
}

// CurrentAddress returns the active address path.
func CurrentAddress(ctx context.Context) Address {
	a, _ := ctx.Value(addressKey{}).(Address)
	return append(Address(nil), a...)
}

// WithToolCallID binds the active tool call id into context.
func WithToolCallID(ctx context.Context, callID string) context.Context {
	return context.WithValue(ctx, toolCallIDKey{}, callID)
}

// ToolCallID reads the active tool call id from context.
func ToolCallID(ctx context.Context) string {
	id, _ := ctx.Value(toolCallIDKey{}).(string)
	return id
}
