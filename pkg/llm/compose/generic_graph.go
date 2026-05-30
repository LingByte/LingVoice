package compose

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// GenericGraph is a typed I/O wrapper over Graph (Eino NewGraph[I,O] subset).
type GenericGraph[I, O any] struct {
	inner     *Graph
	fromInput func(context.Context, I) ([]*schema.Message, map[string]any, error)
	toOutput  func(*GraphState) (O, error)
}

// NewGenericGraph builds a typed graph with message boundary mappers.
func NewGenericGraph[I, O any](
	name string,
	fromInput func(context.Context, I) ([]*schema.Message, map[string]any, error),
	toOutput func(*GraphState) (O, error),
	opts ...NewGraphOption,
) *GenericGraph[I, O] {
	return &GenericGraph[I, O]{
		inner:     NewGraphWithOptions(name, opts...),
		fromInput: fromInput,
		toOutput:  toOutput,
	}
}

// Graph returns the underlying untyped graph builder.
func (g *GenericGraph[I, O]) Graph() *Graph {
	if g == nil {
		return nil
	}
	return g.inner
}

// Compile compiles the inner graph.
func (g *GenericGraph[I, O]) Compile(opts ...GraphCompileOption) (*TypedCompiledGraph[I, O], error) {
	if g == nil || g.inner == nil {
		return nil, fmt.Errorf("compose: nil generic graph")
	}
	cg, err := g.inner.Compile(opts...)
	if err != nil {
		return nil, err
	}
	return &TypedCompiledGraph[I, O]{
		inner:     cg,
		fromInput: g.fromInput,
		toOutput:  g.toOutput,
	}, nil
}

// TypedCompiledGraph runs a compiled graph with typed I/O.
type TypedCompiledGraph[I, O any] struct {
	inner     *CompiledGraph
	fromInput func(context.Context, I) ([]*schema.Message, map[string]any, error)
	toOutput  func(*GraphState) (O, error)
}

// Invoke runs the graph with typed input/output.
func (r *TypedCompiledGraph[I, O]) Invoke(ctx context.Context, input I, opts ...GraphInvokeOption) (O, *GraphState, []GraphStep, error) {
	var zero O
	if r == nil || r.inner == nil || r.fromInput == nil || r.toOutput == nil {
		return zero, nil, nil, fmt.Errorf("compose: nil typed compiled graph")
	}
	msgs, vars, err := r.fromInput(ctx, input)
	if err != nil {
		return zero, nil, nil, err
	}
	st, trace, err := r.inner.Invoke(ctx, msgs, opts...)
	if st != nil && vars != nil {
		if st.Vars == nil {
			st.Vars = map[string]any{}
		}
		for k, v := range vars {
			st.Vars[k] = v
		}
	}
	if err != nil {
		return zero, st, trace, err
	}
	out, err := r.toOutput(st)
	return out, st, trace, err
}

// Stream streams typed output through the inner graph.
func (r *TypedCompiledGraph[I, O]) Stream(ctx context.Context, input I, opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	if r == nil || r.inner == nil || r.fromInput == nil {
		return nil, fmt.Errorf("compose: nil typed compiled graph")
	}
	msgs, vars, err := r.fromInput(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(vars) > 0 {
		opts = append([]GraphInvokeOption{WithStateModifier(func(_ context.Context, st *GraphState) error {
			if st.Vars == nil {
				st.Vars = map[string]any{}
			}
			for k, v := range vars {
				st.Vars[k] = v
			}
			return nil
		})}, opts...)
	}
	return r.inner.StreamMessages(ctx, msgs, opts...)
}

// Collect drains a stream through typed I/O.
func (r *TypedCompiledGraph[I, O]) Collect(ctx context.Context, sr *schema.StreamReader[*schema.Message], opts ...GraphInvokeOption) (O, error) {
	var zero O
	if r == nil || r.inner == nil || r.toOutput == nil {
		return zero, fmt.Errorf("compose: nil typed compiled graph")
	}
	msg, err := r.inner.Collect(ctx, sr, opts...)
	if err != nil {
		return zero, err
	}
	st := &GraphState{LastOutput: msg, Messages: []*schema.Message{msg}}
	out, err := r.toOutput(st)
	return out, err
}

// Transform pipes a stream through typed graph streaming.
func (r *TypedCompiledGraph[I, O]) Transform(ctx context.Context, sr *schema.StreamReader[*schema.Message], opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	if r == nil || r.inner == nil {
		return nil, fmt.Errorf("compose: nil typed compiled graph")
	}
	return r.inner.Transform(ctx, sr, opts...)
}

// Inner returns the compiled untyped graph.
func (r *TypedCompiledGraph[I, O]) Inner() *CompiledGraph {
	if r == nil {
		return nil
	}
	return r.inner
}

// NewMessageGenericGraph is a convenience constructor for []*schema.Message → *schema.Message graphs.
func NewMessageGenericGraph(name string, opts ...NewGraphOption) *GenericGraph[[]*schema.Message, *schema.Message] {
	return NewGenericGraph(name,
		func(_ context.Context, in []*schema.Message) ([]*schema.Message, map[string]any, error) {
			return append([]*schema.Message(nil), in...), nil, nil
		},
		func(st *GraphState) (*schema.Message, error) {
			if st == nil {
				return nil, fmt.Errorf("compose: nil graph state")
			}
			if st.LastOutput != nil {
				return st.LastOutput, nil
			}
			return FinalMessage(st), nil
		},
		opts...,
	)
}

// NewGraphWithOptions creates a graph with NewGraphOption (Eino NewGraph opts subset).
func NewGraphWithOptions(name string, opts ...NewGraphOption) *Graph {
	g := NewGraph(name)
	var o graphOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.genLocalState != nil {
		g.genLocalState = o.genLocalState
	}
	return g
}
