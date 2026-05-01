package contask

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTaskStatus_String(t *testing.T) {
	cases := []TaskStatus{
		TaskStatusPending,
		TaskStatusScheduled,
		TaskStatusRunning,
		TaskStatusSuccess,
		TaskStatusFailed,
		TaskStatusCanceled,
		TaskStatusRetry,
		TaskStatusTimeout,
		TaskStatusPaused,
		TaskStatusSkipped,
		TaskStatusBlocked,
		TaskStatusTerminated,
	}
	for _, s := range cases {
		if s.String() != string(s) {
			t.Fatalf("%v String() got %q want %q", s, s.String(), string(s))
		}
	}
}

func TestTaskStageConstants(t *testing.T) {
	stages := []TaskStage{
		TaskStageSubmit, TaskStageEnqueue, TaskStageReject, TaskStageDequeue,
		TaskStageStart, TaskStageFinish, TaskStageCallerRun, TaskStageDiscard,
		TaskStageDiscardOldest, TaskStagePoolClosed, TaskStageWorkerStarted,
		TaskStageWorkerStopped, TaskStageWorkerRecovered, TaskStageRetrying, TaskStageDead,
	}
	for _, s := range stages {
		if string(s) == "" {
			t.Fatal("empty stage")
		}
	}
}

func TestRetryOptions_maxAttempts(t *testing.T) {
	var o RetryOptions
	if o.maxAttempts() != 1 {
		t.Fatalf("zero MaxAttempts -> %d", o.maxAttempts())
	}
	o.MaxAttempts = -1
	if o.maxAttempts() != 1 {
		t.Fatal()
	}
	o.MaxAttempts = 4
	if o.maxAttempts() != 4 {
		t.Fatal()
	}
}

func TestRetryOptions_backoff(t *testing.T) {
	var o RetryOptions
	if d := o.backoff(1); d != 0 {
		t.Fatal(d)
	}
	o.Backoff = func(attempt int) time.Duration {
		return time.Duration(attempt) * time.Millisecond
	}
	if o.backoff(3) != 3*time.Millisecond {
		t.Fatal()
	}
}

func TestRetryOptions_shouldRetry(t *testing.T) {
	ctx := context.Background()
	var o RetryOptions
	if o.shouldRetry(ctx, nil) {
		t.Fatal("nil err")
	}
	ctx2, cancel := context.WithCancel(ctx)
	cancel()
	if o.shouldRetry(ctx2, errors.New("x")) {
		t.Fatal("ctx done")
	}
	if !o.shouldRetry(ctx, errors.New("x")) {
		t.Fatal("default retry")
	}
	o.RetryOn = func(err error) bool { return false }
	if o.shouldRetry(ctx, errors.New("x")) {
		t.Fatal("RetryOn false")
	}
	o.RetryOn = func(err error) bool { return true }
	if !o.shouldRetry(ctx, errors.New("x")) {
		t.Fatal("RetryOn true")
	}
}

func TestRejectPolicyConstants(t *testing.T) {
	policies := []RejectPolicy{
		RejectPolicyAbort, RejectPolicyCallerRun, RejectPolicyDiscard,
		RejectPolicyDiscardOldest, RejectPolicyBlock,
	}
	for _, p := range policies {
		if string(p) == "" {
			t.Fatal("empty policy")
		}
	}
}

func TestErrorsSentinel(t *testing.T) {
	if ErrPoolClosed.Error() == "" || ErrRejected.Error() == "" {
		t.Fatal()
	}
}
