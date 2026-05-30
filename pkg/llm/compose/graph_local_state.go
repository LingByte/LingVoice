package compose

import (
	"context"
	"fmt"
	"reflect"
	"sync"
)

// GenLocalState generates per-run typed local state shared across graph nodes (Eino subset).
type GenLocalState[S any] func(ctx context.Context) S

type graphOptions struct {
	genLocalState func(context.Context) any
}

// NewGraphOption configures graph construction (Eino NewGraphOption subset).
type NewGraphOption func(*graphOptions)

// WithGenLocalState registers a generator for per-run local state (Eino WithGenLocalState subset).
func WithGenLocalState[S any](gls GenLocalState[S]) NewGraphOption {
	return func(o *graphOptions) {
		o.genLocalState = func(ctx context.Context) any {
			return gls(ctx)
		}
	}
}

type localStateKey struct{}

type internalLocalState struct {
	state  any
	mu     sync.Mutex
	parent *internalLocalState
}

// ProcessState accesses typed local state from ctx in a concurrency-safe way (Eino ProcessState subset).
func ProcessState[S any](ctx context.Context, handler func(context.Context, S) error) error {
	s, mu, err := getLocalState[S](ctx)
	if err != nil {
		return fmt.Errorf("compose: get local state: %w", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return handler(ctx, s)
}

func getLocalState[S any](ctx context.Context) (S, *sync.Mutex, error) {
	var zero S
	raw := ctx.Value(localStateKey{})
	if raw == nil {
		return zero, nil, fmt.Errorf("local state not set")
	}
	chain, ok := raw.(*internalLocalState)
	if !ok || chain == nil {
		return zero, nil, fmt.Errorf("invalid local state chain")
	}
	for cur := chain; cur != nil; cur = cur.parent {
		if typed, ok := cur.state.(S); ok {
			return typed, &cur.mu, nil
		}
	}
	return zero, nil, fmt.Errorf("local state type %v not found", reflect.TypeOf(zero))
}

func initLocalState(ctx context.Context, gen func(context.Context) any) context.Context {
	if gen == nil {
		return ctx
	}
	return context.WithValue(ctx, localStateKey{}, &internalLocalState{state: gen(ctx)})
}

func withChildLocalState(ctx context.Context, gen func(context.Context) any) context.Context {
	if gen == nil {
		return ctx
	}
	parent, _ := ctx.Value(localStateKey{}).(*internalLocalState)
	child := &internalLocalState{
		state:  gen(ctx),
		parent: parent,
	}
	return context.WithValue(ctx, localStateKey{}, child)
}
