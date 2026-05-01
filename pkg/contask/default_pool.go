package contask

import (
	"sync/atomic"
)

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

// AnyPool is the shared pool type for heterogeneous submissions (Params/Result as any).
type AnyPool = Pool[any, any]

var globalPool atomic.Pointer[AnyPool]

func init() {
	p, err := NewPool[any, any](PoolOptions{
		MaxWorkers: 8,
		QueueCap:   1024,
		Policy:     RejectPolicyBlock,
	})
	if err != nil {
		panic("contask: init global pool: " + err.Error())
	}
	globalPool.Store(p)
}

// GlobalPool returns the process-wide pool created during package init.
func GlobalPool() *AnyPool {
	return globalPool.Load()
}

// SetGlobalPool swaps the global pool. If p is nil, the current pool is unchanged.
// When replacing a pool you created, Close it and Wait after switching if you need a clean shutdown.
func SetGlobalPool(p *AnyPool) {
	if p == nil {
		return
	}
	globalPool.Store(p)
}
