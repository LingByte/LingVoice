package joblet

import (
	"context"
	"time"
)

type ShutdownOptions struct {
	// Drain controls whether to finish queued tasks before returning.
	// If false, queued tasks are dropped as skipped; running tasks can't be force-killed.
	Drain bool
}

// Shutdown gracefully stops the pool.
//
// Default behavior is Drain=true.
// If ctx is done before completion, Shutdown returns ctx.Err() and performs a best-effort force close:
// it stops accepting new tasks and drops queued tasks, but cannot kill running goroutines.
func (p *Pool[Params, Result]) Shutdown(ctx context.Context, opts ...ShutdownOptions) error {
	drain := true
	if len(opts) > 0 {
		drain = opts[0].Drain
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	if !drain {
		p.dropQueuedLocked()
	}
	p.notEmpty.Broadcast()
	p.notFull.Broadcast()
	p.mu.Unlock()

	// Non-drain mode does not wait for running tasks (cannot force-kill goroutines).
	if !drain {
		return nil
	}

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		// Force: drop queued tasks and return.
		p.mu.Lock()
		p.closed = true
		p.dropQueuedLocked()
		p.notEmpty.Broadcast()
		p.notFull.Broadcast()
		p.mu.Unlock()
		return ctx.Err()
	}
}

func (p *Pool[Params, Result]) dropQueuedLocked() {
	for len(p.q) > 0 {
		t, _ := p.popLocked()
		if t == nil {
			continue
		}
		var zero Result
		t.deliverFinal(TaskStatusSkipped, zero, nil)
		p.stats.skipped.Add(1)
		p.stats.finished.Add(1)
		p.emit(context.Background(), t, TaskStageDiscard, "shutdown_drop", nil, len(p.q), p.workers, t.Attempts())
	}
}

// Deprecated: prefer Shutdown(ctx).
func (p *Pool[Params, Result]) ShutdownTimeout(d time.Duration, drain bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return p.Shutdown(ctx, ShutdownOptions{Drain: drain})
}

