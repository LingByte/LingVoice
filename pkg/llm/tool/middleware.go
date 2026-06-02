package tool

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// Middleware wraps an InvokableTool (Eino-style tool middleware chain).
type Middleware func(InvokableTool) InvokableTool

// Chain applies middlewares outer-to-inner: Chain(t, m1, m2) => m1(m2(t)).
func Chain(t InvokableTool, mws ...Middleware) InvokableTool {
	if t == nil {
		return nil
	}
	out := t
	for i := len(mws) - 1; i >= 0; i-- {
		if mws[i] != nil {
			out = mws[i](out)
		}
	}
	return out
}

// WithErrorHandlerMiddleware wraps errors as model-visible strings.
func WithErrorHandlerMiddleware(h ErrorHandler) Middleware {
	return func(t InvokableTool) InvokableTool {
		return WrapInvokableToolWithErrorHandler(t, h)
	}
}

// WithInvokableWrapper applies a custom run wrapper.
func WithInvokableWrapper(fn func(ctx context.Context, inner InvokableTool, args string) (string, error)) Middleware {
	return func(t InvokableTool) InvokableTool {
		if fn == nil {
			return t
		}
		return &wrapperTool{inner: t, fn: fn}
	}
}

type wrapperTool struct {
	inner InvokableTool
	fn    func(ctx context.Context, inner InvokableTool, args string) (string, error)
}

func (w *wrapperTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.inner.Info(ctx)
}

func (w *wrapperTool) InvokableRun(ctx context.Context, argumentsInJSON string) (string, error) {
	return w.fn(ctx, w.inner, argumentsInJSON)
}
