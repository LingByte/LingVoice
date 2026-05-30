package compose

import (
	"context"
	"errors"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// Collect drains a message stream through the graph (Eino Runnable.Collect subset).
func (r *CompiledGraph) Collect(ctx context.Context, sr *schema.StreamReader[*schema.Message], opts ...GraphInvokeOption) (*schema.Message, error) {
	if r == nil {
		return nil, errors.New("compose: nil compiled graph")
	}
	msgs, err := ConcatStreamReader(sr)
	if err != nil {
		return nil, err
	}
	st, _, err := r.Invoke(ctx, []*schema.Message{msgs}, opts...)
	if err != nil {
		return nil, err
	}
	return FinalMessage(st), nil
}

// Transform streams input messages through the graph (auto sync→stream).
func (r *CompiledGraph) Transform(ctx context.Context, sr *schema.StreamReader[*schema.Message], opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	if r == nil {
		return nil, errors.New("compose: nil compiled graph")
	}
	if r.react != nil {
		in, err := ConcatStreamReader(sr)
		if err != nil {
			return nil, err
		}
		return r.Stream(ctx, []*schema.Message{in}, opts...)
	}
	invoke := func(ctx context.Context, msgs []*schema.Message, opts ...GraphInvokeOption) (*schema.Message, error) {
		return r.InvokeAsMessage(ctx, msgs, opts...)
	}
	if r.transformMode == TransformBatchConcatMode {
		return TransformBatchConcat(ctx, sr, invoke, opts...)
	}
	return TransformPerChunk(ctx, sr, invoke, opts...)
}

// StreamMessages auto-selects ReAct, ChatModel DAG stream, or invoke→pipe fallback.
func (r *CompiledGraph) StreamMessages(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	if r == nil {
		return nil, errors.New("compose: nil compiled graph")
	}
	if r.react != nil {
		return r.Stream(ctx, messages, opts...)
	}
	if len(r.chatModels) > 0 && r.runMode != RunModePregel && len(r.fanOut) == 0 {
		return r.streamGraphWalk(ctx, messages, opts...)
	}
	if r.HasPregelStreamCapability() {
		return r.streamPregelGraph(ctx, messages, opts...)
	}
	msg, err := r.InvokeAsMessage(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return pipeSingleMessage(msg), nil
}

// InvokeAsMessage runs the graph and returns the final assistant message.
func (r *CompiledGraph) InvokeAsMessage(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.Message, error) {
	st, _, err := r.Invoke(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return FinalMessage(st), nil
}

// ChainRunnable adapts a compiled chain graph to message Runnable helpers.
type ChainRunnable struct {
	Graph *CompiledGraph
}

// NewChainRunnable wraps a compiled chain graph.
func NewChainRunnable(g *CompiledGraph) *ChainRunnable {
	return &ChainRunnable{Graph: g}
}

func (c *ChainRunnable) Invoke(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.Message, error) {
	if c == nil || c.Graph == nil {
		return nil, errors.New("compose: nil chain runnable")
	}
	return c.Graph.InvokeAsMessage(ctx, messages, opts...)
}

func (c *ChainRunnable) Stream(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	if c == nil || c.Graph == nil {
		return nil, errors.New("compose: nil chain runnable")
	}
	return c.Graph.StreamMessages(ctx, messages, opts...)
}

func (c *ChainRunnable) Collect(ctx context.Context, sr *schema.StreamReader[*schema.Message], opts ...GraphInvokeOption) (*schema.Message, error) {
	if c == nil || c.Graph == nil {
		return nil, errors.New("compose: nil chain runnable")
	}
	return c.Graph.Collect(ctx, sr, opts...)
}

func (c *ChainRunnable) Transform(ctx context.Context, sr *schema.StreamReader[*schema.Message], opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	if c == nil || c.Graph == nil {
		return nil, errors.New("compose: nil chain runnable")
	}
	return c.Graph.Transform(ctx, sr, opts...)
}
