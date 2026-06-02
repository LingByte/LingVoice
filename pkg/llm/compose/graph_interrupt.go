package compose

import (
	"context"
	"time"
)

type graphCancelKey struct{}

// GraphInterruptOptions configures external graph interrupt behavior.
type GraphInterruptOptions struct {
	Timeout *time.Duration
}

// WithGraphInterrupt creates a context that can be externally interrupted (Eino subset).
// Call interrupt() to pause the graph and persist checkpoint when configured.
func WithGraphInterrupt(parent context.Context) (context.Context, func(opts ...GraphInterruptOption)) {
	ctx, cancel := context.WithCancel(parent)
	slot := &graphInterruptSlot{cancel: cancel}
	ctx = context.WithValue(ctx, graphCancelKey{}, slot)
	return ctx, func(opts ...GraphInterruptOption) {
		var cfg GraphInterruptOptions
		for _, o := range opts {
			if o != nil {
				o(&cfg)
			}
		}
		if cfg.Timeout != nil {
			time.AfterFunc(*cfg.Timeout, cancel)
		} else {
			cancel()
		}
	}
}

type GraphInterruptOption func(*GraphInterruptOptions)

// WithGraphInterruptTimeout waits up to timeout before forcing interrupt.
func WithGraphInterruptTimeout(timeout time.Duration) GraphInterruptOption {
	return func(o *GraphInterruptOptions) { o.Timeout = &timeout }
}

type graphInterruptSlot struct {
	cancel context.CancelFunc
}

func graphInterruptRequested(ctx context.Context) bool {
	slot, _ := ctx.Value(graphCancelKey{}).(*graphInterruptSlot)
	if slot == nil {
		return false
	}
	select {
	case <-ctx.Done():
		return ctx.Err() == context.Canceled
	default:
		return false
	}
}
