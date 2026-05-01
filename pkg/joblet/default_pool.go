package joblet

import (
	"sync"
	"sync/atomic"
)

// Default pool is an application-level shared pool for heterogeneous tasks.
// Use Pool[any, any] so different business modules can submit their own Params structs.
type AnyPool = Pool[any, any]

var defaultPool atomic.Pointer[AnyPool]
var defaultPoolOnce sync.Once

// DefaultPool returns a lazily created global pool instance.
// The default configuration is conservative; you can override via SetDefaultPool.
func DefaultPool() *AnyPool {
	defaultPoolOnce.Do(func() {
		p, _ := NewPool[any, any](PoolOptions{
			MaxWorkers: 8,
			QueueCap:   1024,
			Policy:     RejectPolicyBlock,
			DeadCap:    1024,
		})
		defaultPool.Store(p)
	})
	return defaultPool.Load()
}

// SetDefaultPool replaces the global default pool.
// Caller owns the old pool lifecycle (Shutdown/Close).
func SetDefaultPool(p *AnyPool) {
	if p == nil {
		return
	}
	defaultPool.Store(p)
}

