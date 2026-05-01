package joblet

import "testing"

func TestDefaultPoolNotNil(t *testing.T) {
	p := DefaultPool()
	if p == nil {
		t.Fatalf("expected default pool")
	}
}

func TestSetDefaultPool(t *testing.T) {
	p, err := NewPool[any, any](PoolOptions{MaxWorkers: 1, QueueCap: 1, Policy: RejectPolicyAbort})
	if err != nil {
		t.Fatal(err)
	}
	SetDefaultPool(p)
	if DefaultPool() != p {
		t.Fatalf("expected default pool replaced")
	}
	p.Close()
	p.Wait()
}

