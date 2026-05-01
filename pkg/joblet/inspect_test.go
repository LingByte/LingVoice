package joblet

import (
	"context"
	"testing"
)

func TestPositionOf(t *testing.T) {
	p := &Pool[int, int]{
		queueCap: 10,
		q:        make(taskHeap[int, int], 0),
	}

	low := NewTask[int, int](1, 1, func(ctx context.Context, params int) (int, error) { return 1, nil })
	high := NewTask[int, int](10, 2, func(ctx context.Context, params int) (int, error) { return 2, nil })
	mid := NewTask[int, int](5, 3, func(ctx context.Context, params int) (int, error) { return 3, nil })

	p.pushLocked(context.Background(), low)
	p.pushLocked(context.Background(), high)
	p.pushLocked(context.Background(), mid)

	pos, ok := p.PositionOf(high.ID)
	if !ok || pos.Position != 1 || pos.Ahead != 0 || pos.Total != 3 {
		t.Fatalf("unexpected high pos: ok=%v pos=%+v", ok, pos)
	}

	pos, ok = p.PositionOf(mid.ID)
	if !ok || pos.Position != 2 || pos.Ahead != 1 || pos.Total != 3 {
		t.Fatalf("unexpected mid pos: ok=%v pos=%+v", ok, pos)
	}

	pos, ok = p.PositionOf(low.ID)
	if !ok || pos.Position != 3 || pos.Ahead != 2 || pos.Total != 3 {
		t.Fatalf("unexpected low pos: ok=%v pos=%+v", ok, pos)
	}

	if size := p.QueueSize(); size != 3 {
		t.Fatalf("expected size 3, got %d", size)
	}

	if _, ok := p.PositionOf("not-exists"); ok {
		t.Fatalf("expected ok=false for missing task")
	}
}

