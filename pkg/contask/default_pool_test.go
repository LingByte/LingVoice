package contask

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestGlobalPool_nonNil(t *testing.T) {
	p := GlobalPool()
	if p == nil {
		t.Fatal("GlobalPool() nil after init")
	}
}

func TestGlobalPool_submit(t *testing.T) {
	var done atomic.Bool
	tk := NewTask[any, any](0, struct{}{}, func(ctx context.Context, p any) (any, error) {
		done.Store(true)
		return nil, nil
	})
	ctx := context.Background()
	if err := GlobalPool().Submit(ctx, tk); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := tk.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	if !done.Load() {
		t.Fatal("handler did not run")
	}
}

func TestSetGlobalPool_swapAndRestore(t *testing.T) {
	old := GlobalPool()
	t.Cleanup(func() {
		SetGlobalPool(old)
	})

	p, err := NewPool[any, any](PoolOptions{
		MaxWorkers: 2,
		QueueCap:   4,
		Policy:     RejectPolicyBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.Close()
		p.Wait()
	})

	SetGlobalPool(p)
	if GlobalPool() != p {
		t.Fatal("pointer not swapped")
	}

	tk := NewTask[any, any](0, 0, func(ctx context.Context, x any) (any, error) { return 7, nil })
	ctx := context.Background()
	if err := GlobalPool().Submit(ctx, tk); err != nil {
		t.Fatal(err)
	}
	v, err := tk.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if iv, ok := v.(int); !ok || iv != 7 {
		t.Fatalf("result %v (%T)", v, v)
	}
}

func TestSetGlobalPool_nilNoOp(t *testing.T) {
	cur := GlobalPool()
	SetGlobalPool(nil)
	if GlobalPool() != cur {
		t.Fatal("nil should not replace pool")
	}
}
