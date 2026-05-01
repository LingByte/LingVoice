package joblet

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

// RejectPolicy defines how Submit behaves when the queue is full.
type RejectPolicy string

const (
	RejectPolicyAbort         RejectPolicy = "abort"
	RejectPolicyCallerRun     RejectPolicy = "caller_run"
	RejectPolicyDiscard       RejectPolicy = "discard"
	RejectPolicyDiscardOldest RejectPolicy = "discard_oldest"
	RejectPolicyBlock         RejectPolicy = "block"
)

var (
	ErrPoolClosed = errors.New("joblet: pool is closed")
	ErrRejected   = errors.New("joblet: task rejected")
)

// DeadTask is sent to the pool's dead queue when a task fails permanently
// (e.g. retries exhausted).
type DeadTask[Params, Result any] struct {
	TaskID    string
	Priority  int
	Params    Params
	Attempts  int
	LastError error
	At        time.Time
}

// Pool is a priority-based goroutine pool for scheduling and executing tasks.
type Pool[Params, Result any] struct {
	maxWorkers int
	queueCap   int
	policy     RejectPolicy
	log        TaskLogger
	deadCh     chan DeadTask[Params, Result]
	mu         sync.Mutex
	notEmpty   *sync.Cond
	notFull    *sync.Cond
	closed     bool
	seq        uint64
	workers    int
	wg         sync.WaitGroup
	q          taskHeap[Params, Result]

	createdAt time.Time
	stats     poolStats
	latency   *latencyRing
}

type PoolOptions struct {
	MaxWorkers int
	QueueCap   int
	Policy     RejectPolicy
	Log        TaskLogger

	// DeadCap enables a dead queue when > 0.
	DeadCap int
}

func NewPool[Params, Result any](opts PoolOptions) (*Pool[Params, Result], error) {
	if opts.MaxWorkers <= 0 {
		return nil, fmt.Errorf("joblet: MaxWorkers must be > 0")
	}
	if opts.QueueCap < 0 {
		return nil, fmt.Errorf("joblet: QueueCap must be >= 0")
	}
	if opts.DeadCap < 0 {
		return nil, fmt.Errorf("joblet: DeadCap must be >= 0")
	}
	if opts.Policy == "" {
		opts.Policy = RejectPolicyAbort
	}

	p := &Pool[Params, Result]{
		maxWorkers: opts.MaxWorkers,
		queueCap:   opts.QueueCap,
		policy:     opts.Policy,
		log:        opts.Log,
		q:          make(taskHeap[Params, Result], 0),
		createdAt:  time.Now(),
		latency:    newLatencyRing(2048),
	}
	if opts.DeadCap > 0 {
		p.deadCh = make(chan DeadTask[Params, Result], opts.DeadCap)
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

// Dead returns the dead task feedback channel (nil if disabled).
func (p *Pool[Params, Result]) Dead() <-chan DeadTask[Params, Result] { return p.deadCh }

// Close stops accepting new tasks and wakes all waiters.
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

// Wait waits for all workers to exit.
func (p *Pool[Params, Result]) Wait() { p.wg.Wait() }

// Submit submits a task into the pool according to the configured reject policy.
// The returned error only indicates submission behavior; task execution errors are delivered via task.Wait().
func (p *Pool[Params, Result]) Submit(ctx context.Context, t *Task[Params, Result]) error {
	if t == nil {
		return errors.New("joblet: task is nil")
	}
	if t.Handler == nil {
		return errors.New("joblet: task handler is nil")
	}

	p.stats.submitted.Add(1)
	p.emit(ctx, t, TaskStageSubmit, "", nil, 0, 0, t.Attempts())

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		p.stats.rejected.Add(1)
		p.emitLocked(ctx, t, TaskStagePoolClosed, "pool closed", ErrPoolClosed)
		return ErrPoolClosed
	}

	// Block policy waits for capacity (or ctx done).
	if p.policy == RejectPolicyBlock {
		for p.queueCap > 0 && len(p.q) >= p.queueCap && !p.closed {
			if err := ctx.Err(); err != nil {
				p.stats.rejected.Add(1)
				p.emitLocked(ctx, t, TaskStageReject, "ctx done while blocking", err)
				return err
			}
			p.notFull.Wait()
		}
		if p.closed {
			p.stats.rejected.Add(1)
			p.emitLocked(ctx, t, TaskStagePoolClosed, "pool closed", ErrPoolClosed)
			return ErrPoolClosed
		}
		p.pushLocked(ctx, t)
		p.notEmpty.Signal()
		p.emitLocked(ctx, t, TaskStageEnqueue, "", nil)
		return nil
	}

	// QueueCap==0 means no queue (always "full" for enqueue).
	full := p.queueCap == 0 || (p.queueCap > 0 && len(p.q) >= p.queueCap)
	if !full {
		p.pushLocked(ctx, t)
		p.notEmpty.Signal()
		p.emitLocked(ctx, t, TaskStageEnqueue, "", nil)
		return nil
	}

	switch p.policy {
	case RejectPolicyAbort:
		p.stats.rejected.Add(1)
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
		p.stats.skipped.Add(1)
		p.stats.finished.Add(1)
		var zero Result
		t.deliverFinal(TaskStatusSkipped, zero, nil)
		return nil

	case RejectPolicyDiscardOldest:
		if len(p.q) == 0 {
			p.stats.rejected.Add(1)
			p.emitLocked(ctx, t, TaskStageReject, "queue is empty but treated as full", ErrRejected)
			return ErrRejected
		}
		old := p.popOldestLocked()
		if old != nil {
			p.emitLocked(ctx, old, TaskStageDiscardOldest, "discarded oldest", nil)
			p.stats.skipped.Add(1)
			p.stats.finished.Add(1)
			var zero Result
			old.deliverFinal(TaskStatusSkipped, zero, nil)
		}
		if p.queueCap == 0 {
			p.stats.rejected.Add(1)
			p.emitLocked(ctx, t, TaskStageReject, "no-queue capacity", ErrRejected)
			return ErrRejected
		}
		p.pushLocked(ctx, t)
		p.notEmpty.Signal()
		p.emitLocked(ctx, t, TaskStageEnqueue, "", nil)
		return nil

	default:
		p.stats.rejected.Add(1)
		p.emitLocked(ctx, t, TaskStageReject, "unknown policy", ErrRejected)
		return ErrRejected
	}
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
	if t != nil && t.Log != nil {
		t.Log.OnTaskEvent(ctx, TaskLogEvent{
			TaskID:     t.ID,
			TaskName:   t.Name,
			Stage:      stage,
			Status:     t.StatusSafe(),
			Priority:   t.Priority,
			Attempt:    attempt,
			Meta:       meta,
			QueueSize:  queueSize,
			QueueCap:   p.queueCap,
			Workers:    workers,
			MaxWorkers: p.maxWorkers,
			Err:        err,
			At:         now,
			Message:    msg,
		})
	}
	if p.log != nil && t != nil {
		p.log.OnTaskEvent(ctx, TaskLogEvent{
			TaskID:     t.ID,
			TaskName:   t.Name,
			Stage:      stage,
			Status:     t.StatusSafe(),
			Priority:   t.Priority,
			Attempt:    attempt,
			Meta:       meta,
			QueueSize:  queueSize,
			QueueCap:   p.queueCap,
			Workers:    workers,
			MaxWorkers: p.maxWorkers,
			Err:        err,
			At:         now,
			Message:    msg,
		})
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

type poolStats struct {
	submitted atomic.Uint64
	rejected  atomic.Uint64
	started   atomic.Uint64
	finished  atomic.Uint64
	succeeded atomic.Uint64
	failed    atomic.Uint64
	canceled  atomic.Uint64
	timedOut  atomic.Uint64
	skipped   atomic.Uint64
}

func (p *Pool[Params, Result]) startWorkerLocked() {
	p.workers++
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() {
			if r := recover(); r != nil {
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

// execute runs handler attempts until success or terminal failure. The returned error is the
// terminal execution error (nil on success) for finish-stage logging; it is not the same as
// Submit()'s queue/pool errors.
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
					p.stats.canceled.Add(1)
					p.stats.finished.Add(1)
					return ctx.Err()
				case <-timer.C:
				}
			}
		}

		// Single attempt execution (do NOT call t.Run which would finalize channels).
		p.stats.started.Add(1)
		t.attempts.Add(1)
		t.Status.Store(TaskStatusRunning)
		start := time.Now()
		res, err := t.Handler(ctx, t.Params)
		p.latency.add(time.Since(start))
		if err == nil {
			t.deliverFinal(TaskStatusSuccess, res, nil)
			p.stats.succeeded.Add(1)
			p.stats.finished.Add(1)
			return nil
		}

		lastErr = err
		if !t.Retry.shouldRetry(ctx, err) || attempt == maxAttempts {
			// Final failure.
			t.deliverDerived(res, err)
			switch {
			case errors.Is(err, context.Canceled):
				p.stats.canceled.Add(1)
			case errors.Is(err, context.DeadlineExceeded):
				p.stats.timedOut.Add(1)
			default:
				p.stats.failed.Add(1)
			}
			p.stats.finished.Add(1)
			p.maybeDead(ctx, t, attempt, err)
			return err
		}
	}
	return lastErr
}

func (p *Pool[Params, Result]) maybeDead(ctx context.Context, t *Task[Params, Result], attempts int, err error) {
	if p.deadCh == nil {
		return
	}
	d := DeadTask[Params, Result]{
		TaskID:    t.ID,
		Priority:  t.Priority,
		Params:    t.Params,
		Attempts:  attempts,
		LastError: err,
		At:        time.Now(),
	}
	select {
	case p.deadCh <- d:
		p.emit(ctx, t, TaskStageDead, "dead", err, 0, 0, attempts)
	default:
		// drop if dead queue is full
	}
}
