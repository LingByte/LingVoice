package a2a

import (
	"sync"
	"time"
)

// PushDeadLetter records a push webhook delivery that exhausted retries.
type PushDeadLetter struct {
	TaskID    string                `json:"taskId"`
	URL       string                `json:"url"`
	Event     TaskStatusUpdateEvent `json:"event"`
	Attempts  int                   `json:"attempts"`
	LastError string                `json:"lastError"`
	CreatedAt string                `json:"createdAt"`
}

// PushRetryConfig configures webhook retry and dead-letter retention.
type PushRetryConfig struct {
	// MaxRetries is delivery attempts after the first try (default 3).
	MaxRetries int
	// Backoff is the initial retry delay; doubled after each failure (default 500ms).
	Backoff time.Duration
	// DeadLetterMax caps retained dead-letter entries (default 100).
	DeadLetterMax int
}

type pushDeadLetterQueue struct {
	mu    sync.RWMutex
	max   int
	items []PushDeadLetter
}

func newPushDeadLetterQueue(max int) *pushDeadLetterQueue {
	if max <= 0 {
		max = 100
	}
	return &pushDeadLetterQueue{max: max}
}

func (q *pushDeadLetterQueue) add(entry PushDeadLetter) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, entry)
	if len(q.items) > q.max {
		q.items = q.items[len(q.items)-q.max:]
	}
}

func (q *pushDeadLetterQueue) list() []PushDeadLetter {
	if q == nil {
		return nil
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make([]PushDeadLetter, len(q.items))
	copy(out, q.items)
	return out
}

func (q *pushDeadLetterQueue) take(taskID string) []PushDeadLetter {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if taskID == "" {
		out := make([]PushDeadLetter, len(q.items))
		copy(out, q.items)
		q.items = nil
		return out
	}
	var out []PushDeadLetter
	keep := q.items[:0]
	for _, item := range q.items {
		if item.TaskID == taskID {
			out = append(out, item)
			continue
		}
		keep = append(keep, item)
	}
	q.items = keep
	return out
}

func (q *pushDeadLetterQueue) requeue(entry PushDeadLetter) {
	q.add(entry)
}

func resolvePushRetry(cfg PushRetryConfig) (maxRetries int, backoff time.Duration, deadLetterMax int) {
	maxRetries = cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	backoff = cfg.Backoff
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}
	deadLetterMax = cfg.DeadLetterMax
	if deadLetterMax <= 0 {
		deadLetterMax = 100
	}
	return maxRetries, backoff, deadLetterMax
}

// PushDeadLetterListResult is the tasks/pushNotification/deadLetter/list response.
type PushDeadLetterListResult struct {
	Entries []PushDeadLetter `json:"entries"`
}

// PushDeadLetterRedriveParams selects dead-letter entries to retry.
type PushDeadLetterRedriveParams struct {
	TaskID string `json:"taskId,omitempty"`
}

// PushDeadLetterRedriveResult summarizes redrive attempts.
type PushDeadLetterRedriveResult struct {
	Redriven int `json:"redriven"`
	Failed   int `json:"failed"`
}
