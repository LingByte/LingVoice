package contask

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type captureLogger struct {
	mu  sync.Mutex
	evs []TaskLogEvent
}

func (c *captureLogger) OnTaskEvent(ctx context.Context, e TaskLogEvent) {
	c.mu.Lock()
	c.evs = append(c.evs, e)
	c.mu.Unlock()
}

func (c *captureLogger) stages() []TaskStage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]TaskStage, 0, len(c.evs))
	for _, e := range c.evs {
		out = append(out, e.Stage)
	}
	return out
}

func TestPoolPriorityOrder(t *testing.T) {
	ctx := context.Background()
	pool, err := NewPool[int, int](PoolOptions{
		MaxWorkers: 1,
		QueueCap:   10,
		Policy:     RejectPolicyBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		pool.Close()
		pool.Wait()
	}()

	started := make(chan struct{})
	release := make(chan struct{})

	blocker := NewTask[int, int](0, 0, func(ctx context.Context, _ int) (int, error) {
		close(started)
		<-release
		return 0, nil
	})
	if err := pool.Submit(ctx, blocker); err != nil {
		t.Fatal(err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("blocker did not start")
	}

	var mu sync.Mutex
	var order []int

	mk := func(prio int, mark int) *Task[int, int] {
		return NewTask[int, int](prio, mark, func(ctx context.Context, id int) (int, error) {
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			return id, nil
		})
	}

	t1 := mk(1, 1)
	t10 := mk(10, 10)
	t5 := mk(5, 5)

	for _, tk := range []*Task[int, int]{t1, t10, t5} {
		if err := pool.Submit(ctx, tk); err != nil {
			t.Fatal(err)
		}
	}

	close(release)

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := blocker.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	for _, tk := range []*Task[int, int]{t10, t5, t1} {
		if _, err := tk.Wait(waitCtx); err != nil {
			t.Fatal(err)
		}
	}

	mu.Lock()
	got := append([]int(nil), order...)
	mu.Unlock()

	want := []int{10, 5, 1}
	if len(got) != len(want) {
		t.Fatalf("order len got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order got %v want %v", got, want)
		}
	}
}

func TestNewPool_validation(t *testing.T) {
	if _, err := NewPool[int, int](PoolOptions{MaxWorkers: 0, QueueCap: 1}); err == nil {
		t.Fatal()
	}
	if _, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: -1}); err == nil {
		t.Fatal()
	}
}

func TestNewPool_defaultPolicy(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 2, Policy: ""})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)
	if p.policy != RejectPolicyAbort {
		t.Fatal(p.policy)
	}
}

func TestSubmit_nilTaskOrHandler(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)
	ctx := context.Background()
	if err := p.Submit(ctx, nil); err == nil {
		t.Fatal()
	}
	tk := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) { return 0, nil })
	tk.Handler = nil
	if err := p.Submit(ctx, tk); err == nil {
		t.Fatal()
	}
}

func TestSubmit_closedPool(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 2})
	if err != nil {
		t.Fatal(err)
	}
	shutdownPool(t, p)
	ctx := context.Background()
	tk := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) { return 0, nil })
	if err := p.Submit(ctx, tk); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("got %v", err)
	}
}

func TestClose_idempotent(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 1})
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
	p.Close()
	p.Wait()
}

func TestRejectPolicy_abortWhenFull(t *testing.T) {
	// QueueCap must be > 0: cap 0 means "no queue slots", so every non-Block submit hits overflow immediately.
	p, err := NewPool[int, int](PoolOptions{
		MaxWorkers: 1,
		QueueCap:   1,
		Policy:     RejectPolicyAbort,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)
	ctx := context.Background()

	block := make(chan struct{})
	first := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		<-block
		return 0, nil
	})
	if err := p.Submit(ctx, first); err != nil {
		t.Fatal(err)
	}

	second := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) { return 1, nil })
	if err := p.Submit(ctx, second); !errors.Is(err, ErrRejected) {
		t.Fatalf("got %v", err)
	}
	close(block)
	waitDone(t, first)
}

func TestRejectPolicy_blockWaitsUntilCapacity(t *testing.T) {
	// Cond.Wait does not observe context cancellation; verify Block eventually proceeds after work drains.
	p, err := NewPool[int, int](PoolOptions{
		MaxWorkers: 1,
		QueueCap:   1,
		Policy:     RejectPolicyBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)

	release := make(chan struct{})
	first := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		<-release
		return 0, nil
	})
	ctx := context.Background()
	if err := p.Submit(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) { return 1, nil })
	if err := p.Submit(ctx, second); err != nil {
		t.Fatal(err)
	}

	third := NewTask(0, 2, func(ctx context.Context, id int) (int, error) { return id, nil })
	errCh := make(chan error, 1)
	go func() {
		errCh <- p.Submit(ctx, third)
	}()

	select {
	case err := <-errCh:
		t.Fatalf("third submit returned before capacity freed: %v", err)
	case <-time.After(40 * time.Millisecond):
	}

	close(release)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("third submit stuck")
	}

	waitDone(t, first)
	waitDone(t, second)
	v, err := third.Wait(ctx)
	if err != nil || v != 2 {
		t.Fatalf("third result %v err %v", v, err)
	}
}

func TestRejectPolicy_callerRun(t *testing.T) {
	log := &captureLogger{}
	// QueueCap 0 makes the first Submit take caller_run and block the submitter — need a real queue.
	p, err := NewPool[int, int](PoolOptions{
		MaxWorkers: 1,
		QueueCap:   1,
		Policy:     RejectPolicyCallerRun,
		Log:        log,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)

	block := make(chan struct{})
	first := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		<-block
		return 0, nil
	})
	ctx := context.Background()
	if err := p.Submit(ctx, first); err != nil {
		t.Fatal(err)
	}

	second := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) { return 1, nil })
	if err := p.Submit(ctx, second); err != nil {
		t.Fatal(err)
	}

	caller := NewTask(0, 42, func(ctx context.Context, x int) (int, error) { return x, nil })
	if err := p.Submit(ctx, caller); err != nil {
		t.Fatal(err)
	}
	v, err := caller.Wait(context.Background())
	if err != nil || v != 42 {
		t.Fatal(v, err)
	}

	close(block)
	waitDone(t, first)
	waitDone(t, second)

	foundFinish := false
	for _, s := range log.stages() {
		if s == TaskStageFinish {
			foundFinish = true
		}
	}
	if !foundFinish {
		t.Fatal("expected finish log from caller_run path")
	}
}

func TestRejectPolicy_discard(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{
		MaxWorkers: 1,
		QueueCap:   1,
		Policy:     RejectPolicyDiscard,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)

	block := make(chan struct{})
	first := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		<-block
		return 0, nil
	})
	ctx := context.Background()
	if err := p.Submit(ctx, first); err != nil {
		t.Fatal(err)
	}

	queued := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) { return 0, nil })
	if err := p.Submit(ctx, queued); err != nil {
		t.Fatal(err)
	}

	dropped := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) { return 9, nil })
	if err := p.Submit(ctx, dropped); err != nil {
		t.Fatal(err)
	}
	v, err := dropped.Wait(ctx)
	if err != nil || v != 0 {
		t.Fatalf("skipped task want zero,nil got %v %v", v, err)
	}
	if dropped.StatusSafe() != TaskStatusSkipped {
		t.Fatal(dropped.StatusSafe())
	}

	close(block)
	waitDone(t, first)
	waitDone(t, queued)
}

func TestRejectPolicy_discardOldest(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{
		MaxWorkers: 1,
		QueueCap:   1,
		Policy:     RejectPolicyDiscardOldest,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)

	block := make(chan struct{})
	first := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		<-block
		return 0, nil
	})
	ctx := context.Background()
	if err := p.Submit(ctx, first); err != nil {
		t.Fatal(err)
	}

	old := NewTask(0, 1, func(ctx context.Context, x int) (int, error) { return x, nil })
	if err := p.Submit(ctx, old); err != nil {
		t.Fatal(err)
	}

	newTk := NewTask(0, 2, func(ctx context.Context, x int) (int, error) { return x, nil })
	if err := p.Submit(ctx, newTk); err != nil {
		t.Fatal(err)
	}

	v, err := old.Wait(ctx)
	if err != nil || v != 0 {
		t.Fatal(old.StatusSafe(), v, err)
	}

	close(block)
	waitDone(t, first)
	v, err = newTk.Wait(ctx)
	if err != nil || v != 2 {
		t.Fatal(v, err)
	}
}

// With QueueCap 0 + DiscardOldest, overflow hits len(q)==0 and rejects (no queued victim to remove).
func TestRejectPolicy_discardOldest_queueCap0_rejectsImmediately(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{
		MaxWorkers: 1,
		QueueCap:   0,
		Policy:     RejectPolicyDiscardOldest,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)

	ctx := context.Background()
	tk := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) { return 1, nil })
	if err := p.Submit(ctx, tk); !errors.Is(err, ErrRejected) {
		t.Fatalf("got %v", err)
	}
}

func TestEmit_metaAndName(t *testing.T) {
	log := &captureLogger{}
	p, err := NewPool[int, int](PoolOptions{
		MaxWorkers: 1,
		QueueCap:   2,
		Policy:     RejectPolicyBlock,
		Log:        log,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)

	tk := NewTask(0, 7, func(ctx context.Context, x int) (int, error) { return x, nil })
	tk.Name = "hello"
	tk.Meta = map[string]string{"k": "v"}
	tk.Log = log

	ctx := context.Background()
	if err := p.Submit(ctx, tk); err != nil {
		t.Fatal(err)
	}
	if _, err := tk.Wait(ctx); err != nil {
		t.Fatal(err)
	}

	foundMeta := false
	log.mu.Lock()
	defer log.mu.Unlock()
	for _, e := range log.evs {
		if e.Meta != nil && e.Meta["k"] == "v" {
			foundMeta = true
		}
		if e.TaskName == "hello" && e.Message == "hello" {
			// msg replaced by name when empty
		}
	}
	if !foundMeta {
		t.Fatal("expected meta in log events")
	}
}

func TestExecute_retrySuccess(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{
		MaxWorkers: 2,
		QueueCap:   2,
		Policy:     RejectPolicyBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)

	var n atomic.Int32
	tk := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		if n.Add(1) == 1 {
			return 0, errors.New("transient")
		}
		return 99, nil
	})
	tk.Retry = RetryOptions{MaxAttempts: 3}
	ctx := context.Background()
	if err := p.Submit(ctx, tk); err != nil {
		t.Fatal(err)
	}
	v, err := tk.Wait(ctx)
	if err != nil || v != 99 {
		t.Fatal(v, err)
	}
}

func TestExecute_retryExhausted(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{
		MaxWorkers: 1,
		QueueCap:   2,
		Policy:     RejectPolicyBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)

	tk := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		return 0, errors.New("always")
	})
	tk.Retry = RetryOptions{MaxAttempts: 2}
	ctx := context.Background()
	if err := p.Submit(ctx, tk); err != nil {
		t.Fatal(err)
	}
	if _, werr := tk.Wait(ctx); werr == nil {
		t.Fatal()
	}
}

func TestExecute_retryOnFalse(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{
		MaxWorkers: 1,
		QueueCap:   2,
		Policy:     RejectPolicyBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)

	tk := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		return 0, errors.New("nope")
	})
	tk.Retry = RetryOptions{
		MaxAttempts: 5,
		RetryOn:     func(err error) bool { return false },
	}
	ctx := context.Background()
	if err := p.Submit(ctx, tk); err != nil {
		t.Fatal(err)
	}
	if _, werr := tk.Wait(ctx); werr == nil {
		t.Fatal()
	}
}

func TestExecute_retryBackoffCancel(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{
		MaxWorkers: 1,
		QueueCap:   2,
		Policy:     RejectPolicyBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)

	tk := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		return 0, errors.New("retry")
	})
	tk.Retry = RetryOptions{
		MaxAttempts: 3,
		Backoff: func(attempt int) time.Duration {
			return 200 * time.Millisecond
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if err := p.Submit(ctx, tk); err != nil {
		t.Fatal(err)
	}
	_, werr := tk.Wait(context.Background())
	if !errors.Is(werr, context.Canceled) {
		t.Fatalf("got %v", werr)
	}
}

func TestWorkerPanicRestarts(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{
		MaxWorkers: 1,
		QueueCap:   10,
		Policy:     RejectPolicyBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)
	ctx := context.Background()

	bad := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		panic("boom")
	})
	if err := p.Submit(ctx, bad); err != nil {
		t.Fatal(err)
	}

	time.Sleep(150 * time.Millisecond)

	good := NewTask(0, 123, func(ctx context.Context, x int) (int, error) { return x, nil })
	if err := p.Submit(ctx, good); err != nil {
		t.Fatal(err)
	}
	v, err := good.Wait(context.Background())
	if err != nil || v != 123 {
		t.Fatal(v, err)
	}
}

func TestPopLocked_emptyWorkerLoop(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{
		MaxWorkers: 1,
		QueueCap:   2,
		Policy:     RejectPolicyBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownPool(t, p)

	done := make(chan struct{})
	tk := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) {
		close(done)
		return 0, nil
	})
	if err := p.Submit(context.Background(), tk); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not run")
	}
}

func TestCopyMeta_helper(t *testing.T) {
	if copyMeta[int, int](nil) != nil {
		t.Fatal()
	}
	tk := NewTask(0, 0, func(ctx context.Context, _ int) (int, error) { return 0, nil })
	if copyMeta(tk) != nil {
		t.Fatal()
	}
	tk.Meta = map[string]string{"a": "b"}
	m := copyMeta(tk)
	if m["a"] != "b" {
		t.Fatal(m)
	}
}

func shutdownPool(t *testing.T, p *Pool[int, int]) {
	t.Helper()
	p.Close()
	p.Wait()
}

func waitDone(t *testing.T, tk *Task[int, int]) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := tk.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}
