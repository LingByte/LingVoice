package joblet

import (
	"context"
	"testing"
	"time"
)

func TestStatsAndLatency(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 10, Policy: RejectPolicyAbort})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p.Close(); p.Wait() }()

	task := NewTask[int, int](0, 1, func(ctx context.Context, params int) (int, error) {
		time.Sleep(10 * time.Millisecond)
		return 1, nil
	})
	task.Name = "stats-demo"
	task.Meta = map[string]string{"kind": "demo"}

	if err := p.Submit(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := task.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}

	s := p.Stats()
	if s.Submitted == 0 || s.Finished == 0 || s.Succeeded == 0 {
		t.Fatalf("unexpected stats: %+v", s)
	}
	if s.MaxWorkers != 1 {
		t.Fatalf("unexpected MaxWorkers %d", s.MaxWorkers)
	}
	lq := p.Latency()
	if lq.Samples == 0 || lq.Max <= 0 || lq.P50 <= 0 {
		t.Fatalf("unexpected latency: %+v", lq)
	}
}

func TestLatencyRingEdgeAndPick(t *testing.T) {
	// capacity <= 0
	r0 := newLatencyRing(0)
	r0.add(1 * time.Millisecond)
	if q := r0.quantiles(); q.Samples != 0 {
		t.Fatalf("expected 0 samples")
	}

	// pick boundaries
	sorted := []time.Duration{1, 2, 3, 4, 5}
	if pick(sorted, 0) != 1 || pick(sorted, 1) != 5 {
		t.Fatalf("unexpected pick boundary")
	}
	if pick(nil, 0.5) != 0 {
		t.Fatalf("expected 0 for empty slice")
	}
}

func TestPoolLatencyNil(t *testing.T) {
	p, err := NewPool[int, int](PoolOptions{MaxWorkers: 1, QueueCap: 1})
	if err != nil {
		t.Fatal(err)
	}
	// force nil ring (package-level access)
	p.latency = nil
	if q := p.Latency(); q.Samples != 0 {
		t.Fatalf("expected empty quantiles")
	}
	p.Close()
	p.Wait()
}

func TestLatencyRingFilledQuantiles(t *testing.T) {
	r := newLatencyRing(2)
	r.add(10 * time.Millisecond)
	r.add(30 * time.Millisecond)
	r.add(20 * time.Millisecond) // overwrite, ring becomes filled
	q := r.quantiles()
	if q.Samples != 2 {
		t.Fatalf("expected 2 samples, got %d", q.Samples)
	}
	if q.Max <= 0 || q.P50 <= 0 {
		t.Fatalf("unexpected quantiles: %+v", q)
	}
}

