package contask

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

// Pool is a worker pool backed by a priority queue (higher Priority runs first).
type Pool[Params, Result any] struct {
	maxWorkers int
	queueCap   int
	policy     RejectPolicy
	log        TaskLogger

	mu       sync.Mutex
	notEmpty *sync.Cond
	notFull  *sync.Cond
	closed   bool
	seq      uint64
	workers  int
	wg       sync.WaitGroup
	q        taskPriorityHeap[Params, Result]
}

// PoolOptions configures a Pool.
type PoolOptions struct {
	MaxWorkers int
	QueueCap   int
	Policy     RejectPolicy
	Log        TaskLogger
}

// NewPool starts MaxWorkers goroutines and returns a pool. QueueCap 0 means no buffering (enqueue always “full”).
func NewPool[Params, Result any](opts PoolOptions) (*Pool[Params, Result], error) {
	if opts.MaxWorkers <= 0 {
		return nil, fmt.Errorf("contask: MaxWorkers must be > 0")
	}
	if opts.QueueCap < 0 {
		return nil, fmt.Errorf("contask: QueueCap must be >= 0")
	}
	if opts.Policy == "" {
		opts.Policy = RejectPolicyAbort
	}

	p := &Pool[Params, Result]{
		maxWorkers: opts.MaxWorkers,
		queueCap:   opts.QueueCap,
		policy:     opts.Policy,
		log:        opts.Log,
		q:          make(taskPriorityHeap[Params, Result], 0),
	}
	p.notEmpty = sync.NewCond(&p.mu)
	p.notFull = sync.NewCond(&p.mu)

	p.mu.Lock()
	for i := 0; i < p.maxWorkers; i++ {
		p.startWorkerLocked()
	}
	p.mu.Unlock()
	return p, nil
}

// Close stops accepting new work and signals workers to drain and exit.
func (p *Pool[Params, Result]) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.notEmpty.Broadcast()
	p.notFull.Broadcast()
	p.mu.Unlock()
}

// Wait blocks until all workers have exited after Close.
func (p *Pool[Params, Result]) Wait() { p.wg.Wait() }

// Submit enqueues a task per RejectPolicy. Execution errors are observed via task.Wait, not the returned error.
func (p *Pool[Params, Result]) Submit(ctx context.Context, t *Task[Params, Result]) error {
	if t == nil {
		return errors.New("contask: task is nil")
	}
	if t.Handler == nil {
		return errors.New("contask: task handler is nil")
	}

	p.emit(ctx, t, TaskStageSubmit, "", nil, 0, 0, t.Attempts())

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		p.emitLocked(ctx, t, TaskStagePoolClosed, "pool closed", ErrPoolClosed)
		return ErrPoolClosed
	}

	if p.policy == RejectPolicyBlock {
		for p.queueCap > 0 && len(p.q) >= p.queueCap && !p.closed {
			if err := ctx.Err(); err != nil {
				p.emitLocked(ctx, t, TaskStageReject, "ctx done while blocking", err)
				return err
			}
			p.notFull.Wait()
		}
		if p.closed {
			p.emitLocked(ctx, t, TaskStagePoolClosed, "pool closed", ErrPoolClosed)
			return ErrPoolClosed
		}
		p.pushLocked(ctx, t)
		p.notEmpty.Signal()
		p.emitLocked(ctx, t, TaskStageEnqueue, "", nil)
		return nil
	}

	full := p.queueCap == 0 || (p.queueCap > 0 && len(p.q) >= p.queueCap)
	if !full {
		p.pushLocked(ctx, t)
		p.notEmpty.Signal()
		p.emitLocked(ctx, t, TaskStageEnqueue, "", nil)
		return nil
	}

	switch p.policy {
	case RejectPolicyAbort:
		p.emitLocked(ctx, t, TaskStageReject, "queue full", ErrRejected)
		return ErrRejected

	case RejectPolicyCallerRun:
		p.emitLocked(ctx, t, TaskStageCallerRun, "executed by caller", nil)
		p.mu.Unlock()
		finishErr := p.execute(ctx, t)
		p.emit(ctx, t, TaskStageFinish, "", finishErr, 0, 0, t.Attempts())
		p.mu.Lock()
		return nil

	case RejectPolicyDiscard:
		p.emitLocked(ctx, t, TaskStageDiscard, "discarded", nil)
		var zero Result
		t.deliverFinal(TaskStatusSkipped, zero, nil)
		return nil

	case RejectPolicyDiscardOldest:
		if len(p.q) == 0 {
			p.emitLocked(ctx, t, TaskStageReject, "queue is empty but treated as full", ErrRejected)
			return ErrRejected
		}
		old := p.popOldestLocked()
		if old != nil {
			p.emitLocked(ctx, old, TaskStageDiscardOldest, "discarded oldest", nil)
			var zero Result
			old.deliverFinal(TaskStatusSkipped, zero, nil)
		}
		if p.queueCap == 0 {
			p.emitLocked(ctx, t, TaskStageReject, "no-queue capacity", ErrRejected)
			return ErrRejected
		}
		p.pushLocked(ctx, t)
		p.notEmpty.Signal()
		p.emitLocked(ctx, t, TaskStageEnqueue, "", nil)
		return nil

	default:
		p.emitLocked(ctx, t, TaskStageReject, "unknown policy", ErrRejected)
		return ErrRejected
	}
}

func (p *Pool[Params, Result]) pushLocked(ctx context.Context, t *Task[Params, Result]) {
	p.seq++
	t.SubmitTime = time.Now()
	t.Status.Store(TaskStatusScheduled)
	heap.Push(&p.q, taskQueueItem[Params, Result]{task: t, seq: p.seq, ctx: ctx})
}

func (p *Pool[Params, Result]) popLocked() (*Task[Params, Result], context.Context) {
	if len(p.q) == 0 {
		return nil, nil
	}
	item := heap.Pop(&p.q).(taskQueueItem[Params, Result])
	return item.task, item.ctx
}

// popOldestLocked removes the smallest seq entry (FIFO root among all). O(n); used for DiscardOldest.
func (p *Pool[Params, Result]) popOldestLocked() *Task[Params, Result] {
	if len(p.q) == 0 {
		return nil
	}
	oldestIdx := 0
	oldestSeq := p.q[0].seq
	for i := 1; i < len(p.q); i++ {
		if p.q[i].seq < oldestSeq {
			oldestSeq = p.q[i].seq
			oldestIdx = i
		}
	}
	oldest := p.q[oldestIdx].task
	heap.Remove(&p.q, oldestIdx)
	return oldest
}

func (p *Pool[Params, Result]) emitLocked(ctx context.Context, t *Task[Params, Result], stage TaskStage, msg string, err error) {
	p.emit(ctx, t, stage, msg, err, len(p.q), p.workers, t.Attempts())
}

func (p *Pool[Params, Result]) emit(ctx context.Context, t *Task[Params, Result], stage TaskStage, msg string, err error, queueSize int, workers int, attempt int) {
	now := time.Now()
	if msg == "" && t != nil && t.Name != "" {
		msg = t.Name
	}
	meta := copyMeta(t)
	ev := TaskLogEvent{
		TaskID:     "",
		TaskName:   "",
		Stage:      stage,
		Status:     TaskStatusPending,
		Priority:   0,
		Attempt:    attempt,
		Meta:       meta,
		QueueSize:  queueSize,
		QueueCap:   p.queueCap,
		Workers:    workers,
		MaxWorkers: p.maxWorkers,
		Err:        err,
		At:         now,
		Message:    msg,
	}
	if t != nil {
		ev.TaskID = t.ID
		ev.TaskName = t.Name
		ev.Status = t.StatusSafe()
		ev.Priority = t.Priority
	}
	if t != nil && t.Log != nil {
		t.Log.OnTaskEvent(ctx, ev)
	}
	if p.log != nil && t != nil {
		p.log.OnTaskEvent(ctx, ev)
	}
}

func copyMeta[Params, Result any](t *Task[Params, Result]) map[string]string {
	if t == nil || len(t.Meta) == 0 {
		return nil
	}
	out := make(map[string]string, len(t.Meta))
	for k, v := range t.Meta {
		out[k] = v
	}
	return out
}

func (p *Pool[Params, Result]) startWorkerLocked() {
	p.workers++
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				_ = r
				p.mu.Lock()
				p.workers--
				shouldRestart := !p.closed
				p.mu.Unlock()
				if shouldRestart {
					p.mu.Lock()
					p.startWorkerLocked()
					p.mu.Unlock()
				}
			}
		}()

		for {
			p.mu.Lock()
			for len(p.q) == 0 && !p.closed {
				p.notEmpty.Wait()
			}
			if p.closed && len(p.q) == 0 {
				p.workers--
				p.mu.Unlock()
				return
			}
			t, tctx := p.popLocked()
			if p.queueCap > 0 {
				p.notFull.Signal()
			}
			p.mu.Unlock()

			if t == nil {
				continue
			}
			if tctx == nil {
				tctx = context.Background()
			}

			p.emit(tctx, t, TaskStageDequeue, "", nil, 0, 0, t.Attempts())
			p.emit(tctx, t, TaskStageStart, "", nil, 0, 0, t.Attempts()+1)
			finishErr := p.execute(tctx, t)
			p.emit(tctx, t, TaskStageFinish, "", finishErr, 0, 0, t.Attempts())
		}
	}()
}

func (p *Pool[Params, Result]) execute(ctx context.Context, t *Task[Params, Result]) (terminalErr error) {
	maxAttempts := t.Retry.maxAttempts()
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			t.Status.Store(TaskStatusRetry)
			p.emit(ctx, t, TaskStageRetrying, "retrying", lastErr, 0, 0, attempt)

			d := t.Retry.backoff(attempt)
			if d > 0 {
				timer := time.NewTimer(d)
				select {
				case <-ctx.Done():
					timer.Stop()
					var zero Result
					t.deliverFinal(TaskStatusCanceled, zero, ctx.Err())
					return ctx.Err()
				case <-timer.C:
				}
			}
		}

		t.attempts.Add(1)
		t.Status.Store(TaskStatusRunning)
		res, err := t.Handler(ctx, t.Params)
		if err == nil {
			t.deliverFinal(TaskStatusSuccess, res, nil)
			return nil
		}

		lastErr = err
		if !t.Retry.shouldRetry(ctx, err) || attempt == maxAttempts {
			t.deliverDerived(res, err)
			return err
		}
	}
	return lastErr
}
