package joblet

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTaskRun_Success(t *testing.T) {
	task := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) { return 7, nil })
	task.Run(context.Background())
	got, err := task.Wait(context.Background())
	if err != nil || got != 7 {
		t.Fatalf("unexpected: got=%v err=%v", got, err)
	}
	if task.StatusSafe() != TaskStatusSuccess {
		t.Fatalf("expected success, got %s", task.StatusSafe())
	}
}

func TestTaskRun_FailedCanceledTimeout(t *testing.T) {
	boom := errors.New("boom")
	failed := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) { return 0, boom })
	failed.Run(context.Background())
	_, err := failed.Wait(context.Background())
	if !errors.Is(err, boom) || failed.StatusSafe() != TaskStatusFailed {
		t.Fatalf("expected failed boom, got err=%v status=%s", err, failed.StatusSafe())
	}

	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) { return 0, ctx.Err() })
	canceled.Run(cctx)
	_, err = canceled.Wait(context.Background())
	if !errors.Is(err, context.Canceled) || canceled.StatusSafe() != TaskStatusCanceled {
		t.Fatalf("expected canceled, got err=%v status=%s", err, canceled.StatusSafe())
	}

	tctx, tcancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	time.Sleep(1 * time.Millisecond)
	defer tcancel()
	timeout := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) { return 0, ctx.Err() })
	timeout.Run(tctx)
	_, err = timeout.Wait(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) || timeout.StatusSafe() != TaskStatusTimeout {
		t.Fatalf("expected timeout, got err=%v status=%s", err, timeout.StatusSafe())
	}
}

func TestTaskWait_CtxDone(t *testing.T) {
	release := make(chan struct{})
	task := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) {
		<-release
		return 1, nil
	})
	go task.Run(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := task.Wait(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected ctx canceled, got %v", err)
	}
	close(release)
}

func TestAttempts_NilReceiver(t *testing.T) {
	var tsk *Task[int, int]
	if tsk.Attempts() != 0 {
		t.Fatalf("expected 0 attempts for nil receiver")
	}
}

func TestRetryOptionsDefaults(t *testing.T) {
	var o RetryOptions
	if o.maxAttempts() != 1 {
		t.Fatalf("expected default maxAttempts=1, got %d", o.maxAttempts())
	}
	if o.backoff(2) != 0 {
		t.Fatalf("expected default backoff=0")
	}
	if !o.shouldRetry(context.Background(), errors.New("x")) {
		t.Fatalf("expected default shouldRetry=true for non-nil err")
	}
	if o.shouldRetry(context.Background(), nil) {
		t.Fatalf("expected shouldRetry=false for nil err")
	}
}

func TestRetryOptionsNoRetryOnCtxDone(t *testing.T) {
	o := RetryOptions{MaxAttempts: 3}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if o.shouldRetry(ctx, errors.New("x")) {
		t.Fatalf("expected shouldRetry=false when ctx done")
	}
}

func TestRetryOptionsPredicateAndBackoff(t *testing.T) {
	want := errors.New("want")
	o := RetryOptions{
		MaxAttempts: 3,
		Backoff:     func(attempt int) time.Duration { return time.Duration(attempt) * time.Millisecond },
		RetryOn:     func(err error) bool { return errors.Is(err, want) },
	}
	if o.backoff(3) != 3*time.Millisecond {
		t.Fatalf("unexpected backoff")
	}
	if !o.shouldRetry(context.Background(), want) {
		t.Fatalf("expected retry for want")
	}
	if o.shouldRetry(context.Background(), errors.New("other")) {
		t.Fatalf("expected no retry for other")
	}
}
