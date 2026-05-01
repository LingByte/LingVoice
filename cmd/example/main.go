package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/LingByte/LingVoice/pkg/contask"
)

type stdoutLogger struct{}

func (l stdoutLogger) OnTaskEvent(ctx context.Context, e contask.TaskLogEvent) {
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

	pool, err := contask.NewPool[int, string](contask.PoolOptions{
		MaxWorkers: 2,
		QueueCap:   8,
		Policy:     contask.RejectPolicyBlock,
		Log:        stdoutLogger{},
	})
	if err != nil {
		panic(err)
	}
	defer func() {
		pool.Close()
		pool.Wait()
	}()

	release := make(chan struct{})
	blocker := contask.NewTask[int, string](100, 1, func(ctx context.Context, params int) (string, error) {
		<-release
		return "unblocked", nil
	})
	blocker.Name = "blocker"
	blocker.Meta = map[string]string{"demo": "queue"}
	_ = pool.Submit(ctx, blocker)

	for i := 0; i < 8; i++ {
		prio := i % 3
		task := contask.NewTask[int, string](prio, i, func(ctx context.Context, params int) (string, error) {
			time.Sleep(40 * time.Millisecond)
			return fmt.Sprintf("work-%d", params), nil
		})
		task.Name = fmt.Sprintf("work-%d", i)
		task.Meta = map[string]string{"kind": "work"}
		_ = pool.Submit(ctx, task)
	}

	var n atomic.Int32
	retryTask := contask.NewTask[int, string](5, 300, func(ctx context.Context, params int) (string, error) {
		if n.Add(1) < 3 {
			return "", errors.New("temporary failure")
		}
		return "retry-success", nil
	})
	retryTask.Name = "retry-task"
	retryTask.Retry = contask.RetryOptions{
		MaxAttempts: 3,
		Backoff: func(attempt int) time.Duration {
			return 30 * time.Millisecond
		},
		RetryOn: func(err error) bool { return true },
	}
	_ = pool.Submit(ctx, retryTask)

	time.Sleep(200 * time.Millisecond)
	close(release)

	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r3, e3 := retryTask.Wait(waitCtx)
	fmt.Printf("retry: res=%q err=%v status=%s attempts=%d\n", r3, e3, retryTask.StatusSafe(), retryTask.Attempts())
}
