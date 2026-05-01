package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LingByte/LingVoice/pkg/joblet"
)

type stdoutLogger struct{}

func (l stdoutLogger) OnTaskEvent(ctx context.Context, e joblet.TaskLogEvent) {
	// Keep this lightweight in production; this is just an example.
	if e.Err != nil {
		fmt.Printf("[%s] task=%s(%s) stage=%s status=%s prio=%d attempt=%d err=%v msg=%s meta=%v\n",
			e.At.Format(time.RFC3339Nano), e.TaskID, e.TaskName, e.Stage, e.Status, e.Priority, e.Attempt, e.Err, e.Message, e.Meta)
		return
	}
	fmt.Printf("[%s] task=%s(%s) stage=%s status=%s prio=%d attempt=%d msg=%s meta=%v\n",
		e.At.Format(time.RFC3339Nano), e.TaskID, e.TaskName, e.Stage, e.Status, e.Priority, e.Attempt, e.Message, e.Meta)
}

func main() {
	ctx := context.Background()

	pool, err := joblet.NewPool[int, string](joblet.PoolOptions{
		MaxWorkers: 2,
		QueueCap:   8,
		Policy:     joblet.RejectPolicyBlock,
		Log:        stdoutLogger{},
		DeadCap:    8,
	})
	if err != nil {
		panic(err)
	}
	defer func() {
		pool.Close()
		pool.Wait()
	}()

	// Dead task feedback consumer.
	if deadCh := pool.Dead(); deadCh != nil {
		go func() {
			for d := range deadCh {
				fmt.Printf("[DEAD] task=%s attempts=%d err=%v at=%s\n",
					d.TaskID, d.Attempts, d.LastError, d.At.Format(time.RFC3339Nano))
			}
		}()
	}

	// 1) Queue position demo.
	// Submit many tasks so you can observe "this task is #N in queue".
	release := make(chan struct{})
	blocker := joblet.NewTask[int, string](100, 1, func(ctx context.Context, params int) (string, error) {
		<-release
		return "unblocked", nil
	})
	blocker.Name = "blocker"
	blocker.Meta = map[string]string{"demo": "queue_position"}
	_ = pool.Submit(ctx, blocker)

	var targetID string
	var targetMu sync.Mutex

	for i := 0; i < 12; i++ {
		prio := i % 3
		task := joblet.NewTask[int, string](prio, i, func(ctx context.Context, params int) (string, error) {
			time.Sleep(80 * time.Millisecond)
			return fmt.Sprintf("work-%d", params), nil
		})
		task.Name = fmt.Sprintf("work-%d", i)
		task.Meta = map[string]string{"kind": "work"}
		if i == 9 {
			targetMu.Lock()
			targetID = task.ID
			targetMu.Unlock()
		}
		_ = pool.Submit(ctx, task)
	}

	// Observe queue position for a short period.
	obsDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		defer close(obsDone)
		deadline := time.Now().Add(800 * time.Millisecond)
		for time.Now().Before(deadline) {
			<-ticker.C
			targetMu.Lock()
			id := targetID
			targetMu.Unlock()
			if id == "" {
				continue
			}
			if pos, ok := pool.PositionOf(id); ok {
				fmt.Printf("[QUEUE] task=%s position=%d ahead=%d total=%d\n", id, pos.Position, pos.Ahead, pos.Total)
			} else {
				fmt.Printf("[QUEUE] task=%s not-in-queue (maybe running/finished)\n", id)
				return
			}
		}
	}()

	// 2) Retry demo: fail twice then succeed.
	var n atomic.Int32
	retryTask := joblet.NewTask[int, string](5, 300, func(ctx context.Context, params int) (string, error) {
		if n.Add(1) < 3 {
			return "", errors.New("temporary failure")
		}
		return "retry-success", nil
	})
	retryTask.Name = "retry-task"
	retryTask.Meta = map[string]string{"kind": "retry"}
	retryTask.Retry = joblet.RetryOptions{
		MaxAttempts: 3,
		Backoff: func(attempt int) time.Duration {
			// attempt starts from 2 when entering backoff (before 2nd try, 3rd try, ...)
			return 30 * time.Millisecond
		},
		RetryOn: func(err error) bool { return true },
	}
	_ = pool.Submit(ctx, retryTask)

	// 3) Dead task demo: retries exhausted -> goes to dead queue.
	deadTask := joblet.NewTask[int, string](5, 400, func(ctx context.Context, params int) (string, error) {
		return "", errors.New("permanent failure")
	})
	deadTask.Name = "dead-task"
	deadTask.Meta = map[string]string{"kind": "dead"}
	deadTask.Retry = joblet.RetryOptions{MaxAttempts: 2}
	_ = pool.Submit(ctx, deadTask)

	// Let some tasks run; then unblock the highest-priority blocker.
	time.Sleep(300 * time.Millisecond)
	close(release)

	// Wait results.
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	<-obsDone

	r3, e3 := retryTask.Wait(waitCtx)
	fmt.Printf("retry: res=%q err=%v status=%s attempts=%d\n", r3, e3, retryTask.StatusSafe(), retryTask.Attempts())

	r4, e4 := deadTask.Wait(waitCtx)
	fmt.Printf("dead:  res=%q err=%v status=%s attempts=%d\n", r4, e4, deadTask.StatusSafe(), deadTask.Attempts())

	// Give dead consumer a moment to print (example-only).
	time.Sleep(100 * time.Millisecond)
}

