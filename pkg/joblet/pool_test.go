package joblet

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewPoolValidation(t *testing.T) {
	if _, err := NewPool[int, int](PoolOptions{MaxWorkers: 0, QueueCap: 1}); err == nil {
		t.Fatalf("expected error for MaxWorkers<=0")
	}
	if _, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: -1}); err == nil {
		t.Fatalf("expected error for QueueCap<0")
	}
	if _, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 0, DeadCap: -1}); err == nil {
		t.Fatalf("expected error for DeadCap<0")
	}
}

func TestSubmitNilTaskAndNilHandler(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p.Close(); p.Wait() }()

	if err := p.Submit(context.Background(), nil); err == nil {
		t.Fatalf("expected error for nil task")
	}
	task := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) { return 1, nil })
	task.Handler = nil
	if err := p.Submit(context.Background(), task); err == nil {
		t.Fatalf("expected error for nil handler")
	}
}

func TestRejectPolicyAbort(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 1, Policy: RejectPolicyAbort})
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
		p.Close()
		p.Wait()
	}()

	started := make(chan struct{})
	first := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return params, nil
	})
	if err := p.Submit(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting first started")
	}

	// Queue one more.
	second := NewTask[int, int](0, 2, func(ctx context.Context, params int) (int, error) { return params, nil })
	if err := p.Submit(context.Background(), second); err != nil {
		t.Fatal(err)
	}

	third := NewTask[int, int](0, 3, func(ctx context.Context, params int) (int, error) { return params, nil })
	if err := p.Submit(context.Background(), third); !errors.Is(err, ErrRejected) {
		t.Fatalf("expected ErrRejected, got %v", err)
	}

	close(release)
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = first.Wait(waitCtx)
	_, _ = second.Wait(waitCtx)
}

func TestRejectPolicyBlockCancelWhileWaiting(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 1, Policy: RejectPolicyBlock})
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
		p.Close()
		p.Wait()
	}()

	started := make(chan struct{})
	first := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return 1, nil
	})
	if err := p.Submit(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting first started")
	}

	second := NewTask[int, int](0, 2, func(ctx context.Context, params int) (int, error) { return 2, nil })
	if err := p.Submit(context.Background(), second); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	third := NewTask[int, int](0, 3, func(ctx context.Context, params int) (int, error) { return 3, nil })
	if err := p.Submit(ctx, third); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected ctx canceled, got %v", err)
	}
}

func TestRetryAndDeadFeedback(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 10, Policy: RejectPolicyAbort, DeadCap: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p.Close(); p.Wait() }()

	task := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) {
		return 0, errors.New("fail")
	})
	task.Retry = RetryOptions{MaxAttempts: 2}

	if err := p.Submit(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = task.Wait(waitCtx)

	select {
	case d := <-p.Dead():
		if d.TaskID != task.ID || d.Attempts != 2 || d.LastError == nil {
			t.Fatalf("unexpected dead payload: %+v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected dead feedback")
	}
}

func TestShutdownForceDropOnCtxDone(t *testing.T) {
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

	// Queue one task.
	second := NewTask[int, int](0, 2, func(ctx context.Context, params int) (int, error) { return 2, nil })
	if err := p.Submit(context.Background(), second); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := p.Shutdown(ctx, ShutdownOptions{Drain: true}); err == nil {
		t.Fatalf("expected ctx timeout error")
	}

	// Shutdown should have force-dropped queued task.
	if second.StatusSafe() != TaskStatusSkipped {
		t.Fatalf("expected queued task skipped, got %s", second.StatusSafe())
	}

	close(release)
	p.Close()
	p.Wait()
}

func TestRejectPolicyCallerRun_NoQueue(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 0, Policy: RejectPolicyCallerRun})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p.Close(); p.Wait() }()

	var ran bool
	task := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) {
		ran = true
		return 42, nil
	})
	if err := p.Submit(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatalf("expected caller-run executed before submit returns")
	}
	got, err := task.Wait(context.Background())
	if err != nil || got != 42 {
		t.Fatalf("unexpected result got=%v err=%v", got, err)
	}
}

func TestRejectPolicyDiscard_NoQueue(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 0, Policy: RejectPolicyDiscard})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p.Close(); p.Wait() }()

	task := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) {
		t.Fatalf("handler should not run under discard")
		return 0, nil
	})
	if err := p.Submit(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	got, err := task.Wait(context.Background())
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if got != 0 || task.StatusSafe() != TaskStatusSkipped || task.Attempts() != 0 {
		t.Fatalf("unexpected discard outcome: got=%v status=%s attempts=%d", got, task.StatusSafe(), task.Attempts())
	}
}

func TestRejectPolicyDiscardOldest(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 1, Policy: RejectPolicyDiscardOldest})
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
		p.Close()
		p.Wait()
	}()

	started := make(chan struct{})
	first := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return params, nil
	})
	if err := p.Submit(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting first started")
	}

	second := NewTask[int, int](0, 2, func(ctx context.Context, params int) (int, error) { return params, nil })
	if err := p.Submit(context.Background(), second); err != nil {
		t.Fatal(err)
	}

	third := NewTask[int, int](0, 3, func(ctx context.Context, params int) (int, error) { return params, nil })
	if err := p.Submit(context.Background(), third); err != nil {
		t.Fatal(err)
	}

	if second.StatusSafe() != TaskStatusSkipped {
		t.Fatalf("expected second skipped, got %s", second.StatusSafe())
	}

	close(release)
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = first.Wait(waitCtx)
	got3, err := third.Wait(waitCtx)
	if err != nil || got3 != 3 {
		t.Fatalf("unexpected third got=%v err=%v", got3, err)
	}
}

func TestSubmitAfterClose(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 1, Policy: RejectPolicyAbort})
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
	defer p.Wait()

	task := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) { return params, nil })
	if err := p.Submit(context.Background(), task); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("expected ErrPoolClosed, got %v", err)
	}
}

func TestDefaultPolicyIsAbort(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 0, Policy: ""})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p.Close(); p.Wait() }()

	task := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) { return params, nil })
	if err := p.Submit(context.Background(), task); !errors.Is(err, ErrRejected) {
		t.Fatalf("expected ErrRejected, got %v", err)
	}
}

func TestUnknownPolicyRejects(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 0, Policy: RejectPolicy("weird")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p.Close(); p.Wait() }()

	task := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) { return params, nil })
	if err := p.Submit(context.Background(), task); !errors.Is(err, ErrRejected) {
		t.Fatalf("expected ErrRejected, got %v", err)
	}
}
