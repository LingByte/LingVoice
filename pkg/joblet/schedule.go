package joblet

import (
	"container/heap"
	"context"
	"time"
)

type taskItem[Params, Result any] struct {
	task *Task[Params, Result]
	seq  uint64
	ctx  context.Context
}

// taskHeap orders tasks by (priority desc, seq asc).
type taskHeap[Params, Result any] []taskItem[Params, Result]

func (h taskHeap[Params, Result]) Len() int { return len(h) }
func (h taskHeap[Params, Result]) Less(i, j int) bool {
	if h[i].task.Priority != h[j].task.Priority {
		return h[i].task.Priority > h[j].task.Priority
	}
	return h[i].seq < h[j].seq
}
func (h taskHeap[Params, Result]) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *taskHeap[Params, Result]) Push(x any)   { *h = append(*h, x.(taskItem[Params, Result])) }
func (h *taskHeap[Params, Result]) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func (p *Pool[Params, Result]) pushLocked(ctx context.Context, t *Task[Params, Result]) {
	p.seq++
	t.SubmitTime = time.Now()
	t.Status.Store(TaskStatusScheduled)
	heap.Push(&p.q, taskItem[Params, Result]{task: t, seq: p.seq, ctx: ctx})
}

func (p *Pool[Params, Result]) popLocked() (*Task[Params, Result], context.Context) {
	if len(p.q) == 0 {
		return nil, nil
	}
	item := heap.Pop(&p.q).(taskItem[Params, Result])
	return item.task, item.ctx
}

// popOldestLocked removes and returns the oldest queued task (smallest seq).
// This is O(n) and only used under pressure with DiscardOldest policy.
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
