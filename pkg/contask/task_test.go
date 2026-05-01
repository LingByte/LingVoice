package contask

import (
	"container/heap"
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewTaskAndAttempts(t *testing.T) {
	tk := NewTask[int, int](7, 42, func(ctx context.Context, n int) (int, error) {
		return n * 2, nil
	})
	if tk.ID == "" || tk.Priority != 7 || tk.Params != 42 {
		t.Fatalf("task fields %+v", tk)
	}
	if tk.Attempts() != 0 {
		t.Fatal()
	}
	var nilTk *Task[int, int]
	if nilTk.Attempts() != 0 {
		t.Fatal()
	}
}

func TestTaskProgress(t *testing.T) {
	var nilTk *Task[int, int]
	nilTk.SetProgress(50)
	if nilTk.ProgressPercent() != 0 {
		t.Fatal()
	}

	tk := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) { return 0, nil })
	tk.SetProgress(-10)
	if tk.ProgressPercent() != 0 {
		t.Fatal()
	}
	tk.SetProgress(200)
	if tk.ProgressPercent() != 100 {
		t.Fatal()
	}
	tk.SetProgress(33)
	if tk.ProgressPercent() != 33 {
		t.Fatal()
	}
}

func TestTaskRun_nilTask(t *testing.T) {
	var tk *Task[int, int]
	tk.Run(context.Background())
}

func TestTaskRun_nilHandler(t *testing.T) {
	tk := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) { return 0, nil })
	tk.Handler = nil
	tk.Run(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := tk.Wait(ctx)
	if !errors.Is(err, ErrHandlerNil) {
		t.Fatalf("got %v", err)
	}
}

func TestTaskRun_successAndErrors(t *testing.T) {
	ok := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) { return 9, nil })
	ok.Run(context.Background())
	ctx := context.Background()
	r, err := ok.Wait(ctx)
	if err != nil || r != 9 {
		t.Fatal(err, r)
	}

	fail := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		return 0, errors.New("boom")
	})
	fail.Run(context.Background())
	if _, err := fail.Wait(ctx); err == nil {
		t.Fatal()
	}

	_, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		return 0, context.Canceled
	})
	canceled.Run(context.Background())
	_, err = canceled.Wait(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	deadline := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		return 0, context.DeadlineExceeded
	})
	deadline.Run(context.Background())
	_, err = deadline.Wait(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestTaskWait_nilAndCancel(t *testing.T) {
	ctx := context.Background()
	var nilTk *Task[int, int]
	if _, err := nilTk.Wait(ctx); !errors.Is(err, ErrTaskNil) {
		t.Fatal(err)
	}

	tk := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(30 * time.Second):
			return 0, nil
		}
	})
	go func() {
		time.Sleep(20 * time.Millisecond)
		tk.Run(context.Background())
	}()
	waitCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tk.Wait(waitCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestTaskPriorityHeap(t *testing.T) {
	h := &taskPriorityHeap[int, int]{}
	heap.Init(h)

	a := NewTask(1, 1, func(ctx context.Context, x int) (int, error) { return x, nil })
	b := NewTask(10, 2, func(ctx context.Context, x int) (int, error) { return x, nil })
	c := NewTask(10, 3, func(ctx context.Context, x int) (int, error) { return x, nil })

	ctx := context.Background()
	heap.Push(h, taskQueueItem[int, int]{task: a, seq: 1, ctx: ctx})
	heap.Push(h, taskQueueItem[int, int]{task: b, seq: 2, ctx: ctx})
	heap.Push(h, taskQueueItem[int, int]{task: c, seq: 3, ctx: ctx})

	if h.Len() != 3 {
		t.Fatal()
	}

	first := heap.Pop(h).(taskQueueItem[int, int])
	if first.task.Priority != 10 || first.task.Params != 2 {
		t.Fatalf("want prio 10 seq-first (params 2), got prio %d params %v", first.task.Priority, first.task.Params)
	}
	second := heap.Pop(h).(taskQueueItem[int, int])
	if second.task.Priority != 10 || second.task.Params != 3 {
		t.Fatal()
	}
	third := heap.Pop(h).(taskQueueItem[int, int])
	if third.task.Priority != 1 {
		t.Fatal()
	}
	if h.Len() != 0 {
		t.Fatal()
	}
}
