package joblet

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSchedulePriorityAndFIFO(t *testing.T) {
	p := &Pool[int, int]{
		queueCap: 10,
		q:        make(taskHeap[int, int], 0),
	}

	a := NewTask[int, int](1, 1, func(ctx context.Context, params int) (int, error) { return params, nil })
	b := NewTask[int, int](1, 2, func(ctx context.Context, params int) (int, error) { return params, nil })
	c := NewTask[int, int](2, 3, func(ctx context.Context, params int) (int, error) { return params, nil }) // higher priority

	p.pushLocked(context.Background(), a)
	p.pushLocked(context.Background(), b)
	p.pushLocked(context.Background(), c)

	t1, _ := p.popLocked()
	if t1 != c {
		t.Fatalf("expected highest priority first")
	}
	t2, _ := p.popLocked()
	if t2 != a {
		t.Fatalf("expected FIFO among same priority (a before b)")
	}
	t3, _ := p.popLocked()
	if t3 != b {
		t.Fatalf("expected FIFO among same priority (b last)")
	}
}

func TestScheduleEmptyPopAndPopOldest(t *testing.T) {
	p := &Pool[int, int]{q: make(taskHeap[int, int], 0)}
	if tk, ctx := p.popLocked(); tk != nil || ctx != nil {
		t.Fatalf("expected nil pop on empty")
	}
	if old := p.popOldestLocked(); old != nil {
		t.Fatalf("expected nil oldest on empty")
	}
}

func TestPopOldestNotHeapRoot(t *testing.T) {
	// Create a case where the heap root isn't the oldest seq.
	p := &Pool[int, int]{
		queueCap: 10,
		q:        make(taskHeap[int, int], 0),
	}
	// Oldest has low priority; heap root will likely be high priority later task.
	oldestLow := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) { return 1, nil })
	high1 := NewTask[int, int](10, 2, func(ctx context.Context, params int) (int, error) { return 2, nil })
	high2 := NewTask[int, int](9, 3, func(ctx context.Context, params int) (int, error) { return 3, nil })

	p.pushLocked(context.Background(), oldestLow) // seq=1
	p.pushLocked(context.Background(), high1)     // seq=2
	p.pushLocked(context.Background(), high2)     // seq=3

	old := p.popOldestLocked()
	if old != oldestLow {
		t.Fatalf("expected oldest task returned")
	}
}

func TestCloseIdempotent(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 1})
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
	p.Close() // cover already-closed branch
	p.Wait()
}

func TestStatusSafeNilValue(t *testing.T) {
	var task Task[int, int]
	if task.StatusSafe() != TaskStatusPending {
		t.Fatalf("expected pending fallback")
	}
}

func TestWaitNilTask(t *testing.T) {
	var tsk *Task[int, int]
	_, err := tsk.Wait(context.Background())
	if err == nil {
		t.Fatalf("expected error for nil task")
	}
}

func TestRetryBackoffCanceled(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 10, Policy: RejectPolicyAbort})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p.Close(); p.Wait() }()

	ctx, cancel := context.WithCancel(context.Background())
	task := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) {
		return 0, errors.New("fail")
	})
	task.Retry = RetryOptions{
		MaxAttempts: 2,
		Backoff:     func(attempt int) time.Duration { return 5 * time.Second },
	}
	if err := p.Submit(ctx, task); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // allow first attempt to fail and enter backoff
	cancel()

	waitCtx, wcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer wcancel()
	_, err = task.Wait(waitCtx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled, got %v", err)
	}
	if task.StatusSafe() != TaskStatusCanceled {
		t.Fatalf("expected canceled status, got %s", task.StatusSafe())
	}
}

func TestDeadQueueFullDrop(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 10, Policy: RejectPolicyAbort, DeadCap: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p.Close(); p.Wait() }()

	fail := func(ctx context.Context, params int) (int, error) { return 0, errors.New("fail") }

	t1 := NewTask[int, int](0, 1, fail)
	t1.Retry = RetryOptions{MaxAttempts: 1}
	t2 := NewTask[int, int](0, 2, fail)
	t2.Retry = RetryOptions{MaxAttempts: 1}

	_ = p.Submit(context.Background(), t1)
	_ = p.Submit(context.Background(), t2)

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = t1.Wait(waitCtx)
	_, _ = t2.Wait(waitCtx)

	// deadCh capacity is 1; ensure we never block and at most 1 item is queued.
	count := 0
	for {
		select {
		case <-p.Dead():
			count++
		default:
			if count > 1 {
				t.Fatalf("expected at most 1 dead item, got %d", count)
			}
			return
		}
	}
}

func TestPositionOfEmptyInputs(t *testing.T) {
	p := &Pool[int, int]{q: make(taskHeap[int, int], 0)}
	if _, ok := p.PositionOf(""); ok {
		t.Fatalf("expected ok=false for empty id")
	}
	if _, ok := p.PositionOf("x"); ok {
		t.Fatalf("expected ok=false for empty queue")
	}
}
