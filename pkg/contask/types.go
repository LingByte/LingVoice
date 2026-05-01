package contask

import (
	"context"
	"errors"
	"time"
)

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

// Handler defines the execution logic of a task.
// Context is always passed at the call site, not stored on the task.
type Handler[Params, Result any] func(ctx context.Context, params Params) (Result, error)

// TaskStatus is the lifecycle state of an asynchronous task.
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusScheduled  TaskStatus = "scheduled"
	TaskStatusRunning    TaskStatus = "running"
	TaskStatusSuccess    TaskStatus = "success"
	TaskStatusFailed     TaskStatus = "failed"
	TaskStatusCanceled   TaskStatus = "canceled"
	TaskStatusRetry      TaskStatus = "retry"
	TaskStatusTimeout    TaskStatus = "timeout"
	TaskStatusPaused     TaskStatus = "paused"
	TaskStatusSkipped    TaskStatus = "skipped"
	TaskStatusBlocked    TaskStatus = "blocked"
	TaskStatusTerminated TaskStatus = "terminated"
)

func (t TaskStatus) String() string { return string(t) }

// TaskStage describes a task lifecycle stage for logging and telemetry.
type TaskStage string

const (
	TaskStageSubmit          TaskStage = "submit"
	TaskStageEnqueue         TaskStage = "enqueue"
	TaskStageReject          TaskStage = "reject"
	TaskStageDequeue         TaskStage = "dequeue"
	TaskStageStart           TaskStage = "start"
	TaskStageFinish          TaskStage = "finish"
	TaskStageCallerRun       TaskStage = "caller_run"
	TaskStageDiscard         TaskStage = "discard"
	TaskStageDiscardOldest   TaskStage = "discard_oldest"
	TaskStagePoolClosed      TaskStage = "pool_closed"
	TaskStageWorkerStarted   TaskStage = "worker_started"
	TaskStageWorkerStopped   TaskStage = "worker_stopped"
	TaskStageWorkerRecovered TaskStage = "worker_recovered"
	TaskStageRetrying        TaskStage = "retrying"
	TaskStageDead            TaskStage = "dead"
)

// TaskLogEvent carries structured context for task stage logs.
type TaskLogEvent struct {
	TaskID     string
	TaskName   string
	Stage      TaskStage
	Status     TaskStatus
	Priority   int
	Attempt    int
	Meta       map[string]string
	QueueSize  int
	QueueCap   int
	Workers    int
	MaxWorkers int
	Err        error
	At         time.Time
	Message    string
}

// TaskLogger receives task stage notifications. Implementations must stay fast and non-blocking.
type TaskLogger interface {
	OnTaskEvent(ctx context.Context, e TaskLogEvent)
}

// RetryBackoff computes delay before the given attempt (1-based after first failure).
type RetryBackoff func(attempt int) time.Duration

// RetryOn decides whether a failed attempt should be retried.
type RetryOn func(err error) bool

// RetryOptions configures retry behavior for the scheduler layer.
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

// RejectPolicy defines how Submit behaves when the queue is at capacity.
type RejectPolicy string

const (
	RejectPolicyAbort         RejectPolicy = "abort"
	RejectPolicyCallerRun     RejectPolicy = "caller_run"
	RejectPolicyDiscard       RejectPolicy = "discard"
	RejectPolicyDiscardOldest RejectPolicy = "discard_oldest"
	RejectPolicyBlock         RejectPolicy = "block"
)

var (
	ErrPoolClosed = errors.New("contask: pool is closed")
	ErrRejected   = errors.New("contask: task rejected")
)
