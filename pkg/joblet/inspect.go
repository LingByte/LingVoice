package joblet

import (
	"sort"
)

// QueuePosition describes a task's position among queued (not running) tasks.
// Position is 1-based. Ahead equals Position-1.
type QueuePosition struct {
	Position int
	Ahead    int
	Total    int
}

// PositionOf returns the rank of the given task among currently queued tasks.
//
// Rank is computed by the scheduler order (priority desc, seq asc),
// NOT by heap internal layout.
//
// ok=false means the task is not currently in the queue (maybe running/finished/never submitted).
func (p *Pool[Params, Result]) PositionOf(taskID string) (pos QueuePosition, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if taskID == "" || len(p.q) == 0 {
		return QueuePosition{}, false
	}

	// snapshot & sort by scheduling order
	items := make([]taskItem[Params, Result], 0, len(p.q))
	items = append(items, p.q...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].task.Priority != items[j].task.Priority {
			return items[i].task.Priority > items[j].task.Priority
		}
		return items[i].seq < items[j].seq
	})

	for i := 0; i < len(items); i++ {
		if items[i].task != nil && items[i].task.ID == taskID {
			position := i + 1
			return QueuePosition{
				Position: position,
				Ahead:    position - 1,
				Total:    len(items),
			}, true
		}
	}
	return QueuePosition{}, false
}

// QueueSize returns current queued task count.
func (p *Pool[Params, Result]) QueueSize() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.q)
}

