package joblet

import (
	"context"
	"sync"
	"testing"
)

type captureLogger struct {
	mu     sync.Mutex
	events []TaskLogEvent
}

func (c *captureLogger) OnTaskEvent(ctx context.Context, e TaskLogEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func TestLoggerHookCalled(t *testing.T) {
	pl := &captureLogger{}
	tl := &captureLogger{}

	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 10, Policy: RejectPolicyAbort, Log: pl})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p.Close(); p.Wait() }()

	task := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) { return 1, nil })
	task.Log = tl
	if err := p.Submit(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := task.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}

	pl.mu.Lock()
	pCount := len(pl.events)
	pl.mu.Unlock()
	tl.mu.Lock()
	tCount := len(tl.events)
	tl.mu.Unlock()

	if pCount == 0 || tCount == 0 {
		t.Fatalf("expected events in both loggers; pool=%d task=%d", pCount, tCount)
	}
}

