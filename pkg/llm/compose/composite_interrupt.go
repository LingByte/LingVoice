package compose

import (
	"context"
	"errors"

	"github.com/LingByte/LingVoice/pkg/llm/internal/core"
)

// CompositeInterrupt bundles multiple interrupt signals (Eino compose.CompositeInterrupt).
func CompositeInterrupt(_ context.Context, info any, state any, errs ...error) error {
	var contexts []*InterruptCtx
	for _, err := range errs {
		if err == nil {
			continue
		}
		if ie, ok := core.AsInterrupt(err); ok {
			if info != nil && ie.Info == nil {
				ie.Info = info
			}
			if state != nil && ie.State == nil {
				ie.State = state
			}
			contexts = append(contexts, &InterruptCtx{
				ID: ie.ID, Node: ie.Node, Info: ie.Info, State: ie.State,
			})
			continue
		}
		if sub, ok := ExtractInterruptInfo(err); ok && sub != nil {
			contexts = append(contexts, sub.InterruptContexts...)
		}
	}
	if len(contexts) == 0 {
		return core.Interrupt(nil, info)
	}
	return wrapInterruptInfo(&InterruptInfo{InterruptContexts: contexts})
}

// CollectInterrupts gathers interrupt errors from parallel steps.
func CollectInterrupts(errs ...error) error {
	var collected []*InterruptCtx
	for _, err := range errs {
		if err == nil {
			continue
		}
		if ie, ok := core.AsInterrupt(err); ok {
			collected = append(collected, &InterruptCtx{
				ID: ie.ID, Node: ie.Node, Info: ie.Info, State: ie.State,
			})
			continue
		}
		if info, ok := ExtractInterruptInfo(err); ok {
			collected = append(collected, info.InterruptContexts...)
		} else {
			return err
		}
	}
	if len(collected) == 0 {
		return nil
	}
	if len(collected) == 1 {
		c := collected[0]
		return &InterruptError{ID: c.ID, Node: c.Node, Info: c.Info, State: c.State}
	}
	return wrapInterruptInfo(&InterruptInfo{InterruptContexts: collected})
}

// InterruptAtNode attaches node name to an interrupt error.
func InterruptAtNode(node string, err error) error {
	ie, ok := core.AsInterrupt(err)
	if !ok {
		return err
	}
	if ie.ID == "" {
		ie.ID = core.NewInterruptID()
	}
	ie.Node = node
	return ie
}

// IsInterrupt reports whether err is an interrupt.
func IsInterrupt(err error) bool {
	return core.IsInterrupt(err) || errors.As(err, new(*interruptWrap))
}

// WithToolCallID binds tool call id into context.
func WithToolCallID(ctx context.Context, callID string) context.Context {
	return core.WithToolCallID(ctx, callID)
}

// ToolCallID reads tool call id from context.
func ToolCallID(ctx context.Context) string {
	return core.ToolCallID(ctx)
}

// AppendAddressSegment extends the address path.
func AppendAddressSegment(ctx context.Context, segment string) context.Context {
	return core.WithAddress(ctx, segment)
}

// CurrentAddress returns the active address path.
func CurrentAddress(ctx context.Context) Address {
	return core.CurrentAddress(ctx)
}

// Address is a hierarchical interrupt path.
type Address = core.Address
