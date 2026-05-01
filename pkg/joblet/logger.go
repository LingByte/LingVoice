package joblet

import (
	"context"
	"time"
)

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

// TaskStage describes a task's lifecycle stage for logging/telemetry.
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

// TaskLogEvent provides structured context for task stage logs.
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

// TaskLogger is a hook for collecting task logs across stages.
// Implementation should be fast and non-blocking; it is called inline.
type TaskLogger interface {
	OnTaskEvent(ctx context.Context, e TaskLogEvent)
}
