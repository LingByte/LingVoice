package tool

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// ErrorHandler converts a tool error into a model-visible string (Eino utils.ErrorHandler).
type ErrorHandler func(ctx context.Context, err error) string

// DefaultErrorHandler is the default soft-failure formatter.
func DefaultErrorHandler(_ context.Context, err error) string {
	return "tool execution failed: " + err.Error()
}

type errorWrapper struct {
	inner InvokableTool
	h     ErrorHandler
}

// WrapInvokableToolWithErrorHandler wraps a tool so errors become string results.
func WrapInvokableToolWithErrorHandler(t InvokableTool, h ErrorHandler) InvokableTool {
	if t == nil {
		return nil
	}
	if h == nil {
		h = DefaultErrorHandler
	}
	return &errorWrapper{inner: t, h: h}
}

func (w *errorWrapper) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.inner.Info(ctx)
}

func (w *errorWrapper) InvokableRun(ctx context.Context, argumentsInJSON string) (string, error) {
	result, err := w.inner.InvokableRun(ctx, argumentsInJSON)
	if err != nil {
		return w.h(ctx, err), nil
	}
	return result, nil
}
