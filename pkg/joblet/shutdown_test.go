package joblet

import (
	"context"
	"testing"
	"time"
)

func TestShutdownDrain(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 10, Policy: RejectPolicyAbort})
	if err != nil {
		t.Fatal(err)
	}

	task := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) {
		time.Sleep(30 * time.Millisecond)
		return 1, nil
	})
	if err := p.Submit(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("unexpected shutdown err: %v", err)
	}

	// Task should be finished (drained).
	if _, err := task.Wait(context.Background()); err != nil {
		t.Fatalf("unexpected task err: %v", err)
	}
}

func TestShutdownNoDrainDropsQueued(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 10, Policy: RejectPolicyAbort})
	if err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	// Block worker on first task.
	first := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) {
		<-release
		return 1, nil
	})
	if err := p.Submit(context.Background(), first); err != nil {
		t.Fatal(err)
	}

	// Queue another.
	second := NewTask[int, int](0, 2, func(ctx context.Context, params int) (int, error) { return 2, nil })
	if err := p.Submit(context.Background(), second); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx, ShutdownOptions{Drain: false}); err != nil {
		t.Fatalf("unexpected shutdown err: %v", err)
	}

	// second should be dropped as skipped.
	if second.StatusSafe() != TaskStatusSkipped {
		t.Fatalf("expected skipped, got %s", second.StatusSafe())
	}

	close(release)
}

func TestShutdownTimeout(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 10, Policy: RejectPolicyAbort})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	first := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) {
		<-release
		return 1, nil
	})
	if err := p.Submit(context.Background(), first); err != nil {
		t.Fatal(err)
	}

	// This call should time out because a running task is blocked.
	err = p.ShutdownTimeout(30*time.Millisecond, true)
	if err == nil {
		t.Fatalf("expected timeout error")
	}

	close(release)
	p.Close()
	p.Wait()
}

