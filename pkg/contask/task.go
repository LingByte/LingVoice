package contask

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

var (
	ErrTaskNil     = errors.New("contask: task is nil")
	ErrHandlerNil  = errors.New("contask: handler is nil")
)

// Task is a generic asynchronous unit of work.
type Task[Params, Result any] struct {
	ID         string                  `json:"id"`
	Name       string                  `json:"name"`
	Priority   int                     `json:"priority"`
	Params     Params                  `json:"params"`
	Handler    Handler[Params, Result] `json:"-"`
	Result     chan Result             `json:"-"`
	Err        chan error              `json:"-"`
	Status     atomic.Value            `json:"-"`
	Progress   atomic.Int32            `json:"-"`
	SubmitTime time.Time               `json:"submit_time"`
	Retry      RetryOptions            `json:"-"`
	Log        TaskLogger              `json:"-"`
	Meta       map[string]string       `json:"meta,omitempty"`
	attempts   atomic.Int32
	finishOnce sync.Once
}

// NewTask builds a task with a generated ID, priority, input params, and handler.
func NewTask[Params, Result any](priority int, params Params, handler Handler[Params, Result]) *Task[Params, Result] {
	t := &Task[Params, Result]{
		ID:         "ct_" + base.SnowflakeUtil.GenID(),
		Priority:   priority,
		Params:     params,
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

// Attempts returns how many executions have been started (including in-flight).
func (t *Task[Params, Result]) Attempts() int {
	if t == nil {
		return 0
	}
	return int(t.attempts.Load())
}

// SetProgress stores completion percentage in [0, 100]. Values outside the range are clamped.
func (t *Task[Params, Result]) SetProgress(pct int) {
	if t == nil {
		return
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	t.Progress.Store(int32(pct))
}

// ProgressPercent returns the stored progress percentage.
func (t *Task[Params, Result]) ProgressPercent() int {
	if t == nil {
		return 0
	}
	return int(t.Progress.Load())
}

// Run executes one handler attempt. Scheduler/pool layers own retries.
func (t *Task[Params, Result]) Run(ctx context.Context) {
	if t == nil {
		return
	}
	if t.Handler == nil {
		var zero Result
		t.deliverFinal(TaskStatusFailed, zero, ErrHandlerNil)
		return
	}
	t.attempts.Add(1)
	t.Status.Store(TaskStatusRunning)
	res, err := t.Handler(ctx, t.Params)
	t.deliverDerived(res, err)
}

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

func (t *Task[Params, Result]) deliverFinal(status TaskStatus, res Result, err error) {
	t.finishOnce.Do(func() {
		t.Status.Store(status)
		t.Result <- res
		t.Err <- err
		close(t.Result)
		close(t.Err)
	})
}

// Wait blocks until the task finishes or ctx is done.
func (t *Task[Params, Result]) Wait(ctx context.Context) (Result, error) {
	var zero Result
	if t == nil {
		return zero, ErrTaskNil
	}
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case err := <-t.Err:
		res := <-t.Result
		return res, err
	}
}

// StatusSafe returns the current status, defaulting to pending if unset or unexpected.
func (t *Task[Params, Result]) StatusSafe() TaskStatus {
	if t == nil {
		return TaskStatusPending
	}
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

// taskQueueItem is one entry in the priority heap: task, stable enqueue order, optional submit context.
type taskQueueItem[Params, Result any] struct {
	task *Task[Params, Result]
	seq  uint64
	ctx  context.Context
}

// taskPriorityHeap is a max-heap by Task.Priority (higher first), then seq ascending for FIFO within the same priority.
type taskPriorityHeap[Params, Result any] []taskQueueItem[Params, Result]

func (h taskPriorityHeap[Params, Result]) Len() int { return len(h) }

func (h taskPriorityHeap[Params, Result]) Less(i, j int) bool {
	if h[i].task.Priority != h[j].task.Priority {
		return h[i].task.Priority > h[j].task.Priority
	}
	return h[i].seq < h[j].seq
}

func (h taskPriorityHeap[Params, Result]) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *taskPriorityHeap[Params, Result]) Push(x any) {
	*h = append(*h, x.(taskQueueItem[Params, Result]))
}

func (h *taskPriorityHeap[Params, Result]) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
