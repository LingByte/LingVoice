package compose

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)
type Runnable[I, O any] interface {
	Invoke(ctx context.Context, input I, opts ...llm.Option) (O, error)
}

// GraphRunnable adapts CompiledGraph to message input/output.
type GraphRunnable struct {
	Graph *CompiledGraph
}

// Invoke runs the graph and returns the final assistant message.
func (r *GraphRunnable) Invoke(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.Message, error) {
	if r == nil || r.Graph == nil {
		return nil, nil
	}
	st, _, err := r.Graph.Invoke(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return FinalMessage(st), nil
}

// Stream runs graph-backed streaming with auto sync→stream selection.
func (r *GraphRunnable) Stream(ctx context.Context, loop *ToolLoop, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	if loop != nil {
		return loop.Stream(ctx, messages)
	}
	if r == nil || r.Graph == nil {
		return nil, nil
	}
	return r.Graph.StreamMessages(ctx, messages, opts...)
}
