package joblet

import (
	"math"
	"sort"
	"sync"
	"time"
)

type PoolStats struct {
	CreatedAt    time.Time
	Uptime       time.Duration
	MaxWorkers   int
	Workers      int
	QueueCap     int
	QueueSize    int
	Submitted    uint64
	Rejected     uint64
	Started      uint64
	Finished     uint64
	Succeeded    uint64
	Failed       uint64
	Canceled     uint64
	TimedOut     uint64
	Skipped      uint64
	SubmitPerSec float64
	FinishPerSec float64
}

type LatencyQuantiles struct {
	Samples int
	P50     time.Duration
	P95     time.Duration
	P99     time.Duration
	Max     time.Duration
}

func (p *Pool[Params, Result]) Stats() PoolStats {
	now := time.Now()

	p.mu.Lock()
	qsize := len(p.q)
	workers := p.workers
	p.mu.Unlock()

	sub := p.stats.submitted.Load()
	fin := p.stats.finished.Load()
	u := now.Sub(p.createdAt)
	sec := u.Seconds()
	subRate := 0.0
	finRate := 0.0
	if sec > 0 {
		subRate = float64(sub) / sec
		finRate = float64(fin) / sec
	}

	return PoolStats{
		CreatedAt:    p.createdAt,
		Uptime:       u,
		MaxWorkers:   p.maxWorkers,
		Workers:      workers,
		QueueCap:     p.queueCap,
		QueueSize:    qsize,
		Submitted:    sub,
		Rejected:     p.stats.rejected.Load(),
		Started:      p.stats.started.Load(),
		Finished:     fin,
		Succeeded:    p.stats.succeeded.Load(),
		Failed:       p.stats.failed.Load(),
		Canceled:     p.stats.canceled.Load(),
		TimedOut:     p.stats.timedOut.Load(),
		Skipped:      p.stats.skipped.Load(),
		SubmitPerSec: subRate,
		FinishPerSec: finRate,
	}
}

func (p *Pool[Params, Result]) Latency() LatencyQuantiles {
	if p.latency == nil {
		return LatencyQuantiles{}
	}
	return p.latency.quantiles()
}

type latencyRing struct {
	mu     sync.Mutex
	buf    []time.Duration
	idx    int
	filled bool
}

func newLatencyRing(capacity int) *latencyRing {
	if capacity <= 0 {
		return &latencyRing{buf: make([]time.Duration, 0)}
	}
	return &latencyRing{buf: make([]time.Duration, capacity)}
}

func (r *latencyRing) add(d time.Duration) {
	if r == nil || len(r.buf) == 0 {
		return
	}
	r.mu.Lock()
	r.buf[r.idx] = d
	r.idx++
	if r.idx >= len(r.buf) {
		r.idx = 0
		r.filled = true
	}
	r.mu.Unlock()
}

func (r *latencyRing) quantiles() LatencyQuantiles {
	if r == nil || len(r.buf) == 0 {
		return LatencyQuantiles{}
	}
	r.mu.Lock()
	var data []time.Duration
	if r.filled {
		data = make([]time.Duration, len(r.buf))
		copy(data, r.buf)
	} else {
		data = make([]time.Duration, r.idx)
		copy(data, r.buf[:r.idx])
	}
	r.mu.Unlock()

	if len(data) == 0 {
		return LatencyQuantiles{}
	}

	sort.Slice(data, func(i, j int) bool { return data[i] < data[j] })
	return LatencyQuantiles{
		Samples: len(data),
		P50:     pick(data, 0.50),
		P95:     pick(data, 0.95),
		P99:     pick(data, 0.99),
		Max:     data[len(data)-1],
	}
}

func pick(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	// nearest-rank
	rank := int(math.Ceil(q*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}
