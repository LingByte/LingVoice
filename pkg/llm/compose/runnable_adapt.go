package compose

import (
	"context"
	"errors"
	"io"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// MessageRunnable is the unified four-mode message runnable (Eino Runnable subset).
type MessageRunnable interface {
	Invoke(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.Message, error)
	Stream(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error)
	Collect(ctx context.Context, sr *schema.StreamReader[*schema.Message], opts ...GraphInvokeOption) (*schema.Message, error)
	Transform(ctx context.Context, sr *schema.StreamReader[*schema.Message], opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error)
}

// compiledMessageRunnable adapts CompiledGraph to MessageRunnable with auto sync→stream.
type compiledMessageRunnable struct {
	graph *CompiledGraph
}

// AsMessageRunnable returns a four-mode runnable for a compiled graph.
func AsMessageRunnable(g *CompiledGraph) MessageRunnable {
	if g == nil {
		return nil
	}
	return &compiledMessageRunnable{graph: g}
}

func (r *compiledMessageRunnable) Invoke(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.Message, error) {
	if r == nil || r.graph == nil {
		return nil, errors.New("compose: nil message runnable")
	}
	return r.graph.InvokeAsMessage(ctx, messages, opts...)
}

func (r *compiledMessageRunnable) Stream(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	if r == nil || r.graph == nil {
		return nil, errors.New("compose: nil message runnable")
	}
	return r.graph.StreamMessages(ctx, messages, opts...)
}

func (r *compiledMessageRunnable) Collect(ctx context.Context, sr *schema.StreamReader[*schema.Message], opts ...GraphInvokeOption) (*schema.Message, error) {
	if r == nil || r.graph == nil {
		return nil, errors.New("compose: nil message runnable")
	}
	return r.graph.Collect(ctx, sr, opts...)
}

func (r *compiledMessageRunnable) Transform(ctx context.Context, sr *schema.StreamReader[*schema.Message], opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	if r == nil || r.graph == nil {
		return nil, errors.New("compose: nil message runnable")
	}
	return r.graph.Transform(ctx, sr, opts...)
}

// HasStreamCapability reports whether the graph can stream natively (ReAct or ChatModel nodes).
func (r *CompiledGraph) HasStreamCapability() bool {
	if r == nil {
		return false
	}
	return r.react != nil || len(r.chatModels) > 0
}

// InvokeToStream pipes Invoke output as a single-chunk stream (sync→stream fallback).
func InvokeToStream(fn func(context.Context, []*schema.Message, ...GraphInvokeOption) (*schema.Message, error)) func(context.Context, []*schema.Message, ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	return func(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
		out, err := fn(ctx, messages, opts...)
		if err != nil {
			return nil, err
		}
		return pipeSingleMessage(out), nil
	}
}

func pipeSingleMessage(msg *schema.Message) *schema.StreamReader[*schema.Message] {
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		defer sw.Close()
		sw.Send(msg, nil)
	}()
	return sr
}

// TransformBatchConcat drains the input stream, invokes once, streams the result (Eino batch transform).
func TransformBatchConcat(
	ctx context.Context,
	sr *schema.StreamReader[*schema.Message],
	invoke func(context.Context, []*schema.Message, ...GraphInvokeOption) (*schema.Message, error),
	opts ...GraphInvokeOption,
) (*schema.StreamReader[*schema.Message], error) {
	msgs, err := ConcatStreamReader(sr)
	if err != nil {
		return nil, err
	}
	out, err := invoke(ctx, []*schema.Message{msgs}, opts...)
	if err != nil {
		return nil, err
	}
	return pipeSingleMessage(out), nil
}

// TransformPerChunk maps each input chunk through invoke (legacy subset behavior).
func TransformPerChunk(
	ctx context.Context,
	sr *schema.StreamReader[*schema.Message],
	invoke func(context.Context, []*schema.Message, ...GraphInvokeOption) (*schema.Message, error),
	opts ...GraphInvokeOption,
) (*schema.StreamReader[*schema.Message], error) {
	outSR, outSW := schema.Pipe[*schema.Message](8)
	go func() {
		defer outSW.Close()
		defer sr.Close()
		for {
			chunk, err := sr.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				outSW.Send(nil, err)
				return
			}
			msg, err := invoke(ctx, []*schema.Message{chunk}, opts...)
			if err != nil {
				outSW.Send(nil, err)
				return
			}
			if msg != nil {
				outSW.Send(msg, nil)
			}
		}
	}()
	return outSR, nil
}
