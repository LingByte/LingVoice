package joblet

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LingByte/LingVoice/pkg/utils/base"
)

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

// Handler defines the execution logic of a task.
// NOTE: Keep ctx out of structs; always pass it at call sites.
type Handler[Params, Result any] func(ctx context.Context, params Params) (Result, error)

// TaskStatus defines the lifecycle state of an asynchronous task.
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"    // Task is created and waiting to be scheduled
	TaskStatusScheduled  TaskStatus = "scheduled"  // Task is scheduled and waiting for execution
	TaskStatusRunning    TaskStatus = "running"    // Task is currently executing
	TaskStatusSuccess    TaskStatus = "success"    // Task completed successfully
	TaskStatusFailed     TaskStatus = "failed"     // Task execution failed
	TaskStatusCanceled   TaskStatus = "canceled"   // Task was canceled by user or system
	TaskStatusRetry      TaskStatus = "retry"      // Task failed and is waiting for retry
	TaskStatusTimeout    TaskStatus = "timeout"    // Task execution timed out
	TaskStatusPaused     TaskStatus = "paused"     // Task is paused and can be resumed
	TaskStatusSkipped    TaskStatus = "skipped"    // Task was skipped due to conditions
	TaskStatusBlocked    TaskStatus = "blocked"    // Task is blocked by dependencies or resource limits
	TaskStatusTerminated TaskStatus = "terminated" // Task was forcefully terminated by the system
)

// String returns the string representation of TaskStatus.
func (t TaskStatus) String() string { return string(t) }

type RetryBackoff func(attempt int) time.Duration
type RetryOn func(err error) bool

type RetryOptions struct {
	MaxAttempts int
	Backoff     RetryBackoff
	RetryOn     RetryOn
}

func (o RetryOptions) maxAttempts() int {
	if o.MaxAttempts <= 0 {
		return 1
	}
	return o.MaxAttempts
}

func (o RetryOptions) backoff(attempt int) time.Duration {
	if o.Backoff == nil {
		return 0
	}
	return o.Backoff(attempt)
}

func (o RetryOptions) shouldRetry(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	if o.RetryOn == nil {
		return true
	}
	return o.RetryOn(err)
}

// Task represents a generic asynchronous unit of work.
type Task[Params, Result any] struct {
	ID         string                  `json:"id"`       // Unique task identifier
	Name       string                  `json:"name"`     // Optional task name (for logs/metrics)
	Priority   int                     `json:"priority"` // Task priority for scheduling
	Params     Params                  `json:"params"`   // Input parameters for the task handler
	Handler    Handler[Params, Result] // Task execution logic
	Result     chan Result             // Channel to receive the final result
	Err        chan error              // Channel to receive execution error
	Status     atomic.Value            // Current task status (atomic for thread safety)
	Progress   atomic.Int32            // Task execution progress (0-100)
	SubmitTime time.Time               // Time when the task was submitted
	Retry      RetryOptions            // Retry behavior (optional)
	Log        TaskLogger              // Optional per-task stage logger
	Meta       map[string]string       `json:"meta,omitempty"` // Optional extra fields for logs/metrics
	attempts   atomic.Int32
	finishOnce sync.Once // Ensures result delivery happens exactly once
}

// NewTask creates a new generic task with a unique ID, priority, parameters, and execution handler.
func NewTask[Params, Result any](priority int, param Params, handler Handler[Params, Result]) *Task[Params, Result] {
	t := &Task[Params, Result]{
		ID:         "tk_" + base.SnowflakeUtil.GenID(),
		Priority:   priority,
		Params:     param,
		Handler:    handler,
		Result:     make(chan Result, 1),
		Err:        make(chan error, 1),
		SubmitTime: time.Now(),
	}
	t.Status.Store(TaskStatusPending)
	t.Progress.Store(0)
	t.attempts.Store(0)
	return t
}

// Attempts returns the number of attempts already performed.
func (t *Task[Params, Result]) Attempts() int {
	if t == nil {
		return 0
	}
	return int(t.attempts.Load())
}

// Run executes a single attempt of the task handler with the provided context.
// Pool retry logic (if any) is handled by the pool/worker layer.
func (t *Task[Params, Result]) Run(ctx context.Context) {
	t.attempts.Add(1)
	t.Status.Store(TaskStatusRunning)
	res, err := t.Handler(ctx, t.Params)
	t.deliverDerived(res, err)
}

// deliverDerived sets final status from err and delivers res/err exactly once.
func (t *Task[Params, Result]) deliverDerived(res Result, err error) {
	switch {
	case err == nil:
		t.deliverFinal(TaskStatusSuccess, res, err)
	case errors.Is(err, context.Canceled):
		t.deliverFinal(TaskStatusCanceled, res, err)
	case errors.Is(err, context.DeadlineExceeded):
		t.deliverFinal(TaskStatusTimeout, res, err)
	default:
		t.deliverFinal(TaskStatusFailed, res, err)
	}
}

// deliverFinal delivers res/err and forces a terminal status exactly once.
func (t *Task[Params, Result]) deliverFinal(status TaskStatus, res Result, err error) {
	t.finishOnce.Do(func() {
		t.Status.Store(status)
		t.Result <- res
		t.Err <- err
		close(t.Result)
		close(t.Err)
	})
}

// Wait blocks until the task completes or the context is canceled.
func (t *Task[Params, Result]) Wait(ctx context.Context) (Result, error) {
	var zero Result
	if t == nil {
		return zero, errors.New("task is nil")
	}
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case err := <-t.Err:
		res := <-t.Result
		return res, err
	}
}

// StatusSafe safely loads and returns the current task status.
func (t *Task[Params, Result]) StatusSafe() TaskStatus {
	defer func() { _ = recover() }()
	v := t.Status.Load()
	if v == nil {
		return TaskStatusPending
	}
	if ts, ok := v.(TaskStatus); ok {
		return ts
	}
	return TaskStatusPending
}
