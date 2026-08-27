package tenant

import (
	"context"
	"testing"
)

func TestRegisterAndGet(t *testing.T) {
	mgr := NewManager(DefaultConfig(), nil)
	t1 := mgr.Register("tenant-a", "Tenant A", ResourceQuota{MaxSessions: 10})
	if t1.ID != "tenant-a" {
		t.Errorf("expected ID 'tenant-a', got '%s'", t1.ID)
	}

	got, ok := mgr.Get("tenant-a")
	if !ok {
		t.Fatal("tenant not found")
	}
	if got.Name != "Tenant A" {
		t.Errorf("expected name 'Tenant A', got '%s'", got.Name)
	}
}

func TestAcquireReleaseSession(t *testing.T) {
	mgr := NewManager(DefaultConfig(), nil)
	mgr.Register("tenant-a", "Tenant A", ResourceQuota{MaxSessions: 2})

	if err := mgr.AcquireSession("tenant-a"); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if err := mgr.AcquireSession("tenant-a"); err != nil {
		t.Fatalf("second acquire failed: %v", err)
	}
	// Third should fail (max 2)
	if err := mgr.AcquireSession("tenant-a"); err == nil {
		t.Error("expected error on third acquire (quota exceeded)")
	}

	mgr.ReleaseSession("tenant-a")
	if err := mgr.AcquireSession("tenant-a"); err != nil {
		t.Errorf("acquire after release failed: %v", err)
	}
}

func TestUnregisteredTenant(t *testing.T) {
	mgr := NewManager(DefaultConfig(), nil)
	// Unregistered tenant should auto-register with default quota
	if err := mgr.AcquireSession("unknown"); err != nil {
		t.Fatalf("acquire for unregistered tenant failed: %v", err)
	}
}

func TestActiveSessions(t *testing.T) {
	mgr := NewManager(DefaultConfig(), nil)
	mgr.Register("t1", "T1", DefaultQuota())

	_ = mgr.AcquireSession("t1")
	_ = mgr.AcquireSession("t1")

	if mgr.ActiveSessions("t1") != 2 {
		t.Errorf("expected 2 active, got %d", mgr.ActiveSessions("t1"))
	}
	if mgr.TotalSessions("t1") != 2 {
		t.Errorf("expected 2 total, got %d", mgr.TotalSessions("t1"))
	}
}

func TestIsProtocolAllowed(t *testing.T) {
	mgr := NewManager(DefaultConfig(), nil)
	mgr.Register("restricted", "Restricted", ResourceQuota{
		AllowedProtocols: []string{"rtmp", "hls"},
	})

	if !mgr.IsProtocolAllowed("restricted", "rtmp") {
		t.Error("rtmp should be allowed")
	}
	if mgr.IsProtocolAllowed("restricted", "sip") {
		t.Error("sip should not be allowed")
	}
	// Unregistered tenant allows all
	if !mgr.IsProtocolAllowed("unknown", "anything") {
		t.Error("unregistered tenant should allow all protocols")
	}
}

func TestListTenants(t *testing.T) {
	mgr := NewManager(DefaultConfig(), nil)
	mgr.Register("t1", "T1", DefaultQuota())
	mgr.Register("t2", "T2", DefaultQuota())

	list := mgr.List()
	if len(list) != 2 {
		t.Errorf("expected 2 tenants, got %d", len(list))
	}
}

func TestUnregister(t *testing.T) {
	mgr := NewManager(DefaultConfig(), nil)
	mgr.Register("t1", "T1", DefaultQuota())
	mgr.Unregister("t1")

	if _, ok := mgr.Get("t1"); ok {
		t.Error("tenant should be removed")
	}
}

func TestContextIntegration(t *testing.T) {
	ctx := WithContext(context.Background(), "tenant-x")
	id := FromContext(ctx)
	if id != "tenant-x" {
		t.Errorf("expected 'tenant-x', got '%s'", id)
	}

	emptyCtx := context.Background()
	if FromContext(emptyCtx) != "" {
		t.Error("empty context should return empty string")
	}
}
