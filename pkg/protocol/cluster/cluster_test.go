package cluster

import (
	"testing"
	"time"
)

func TestNewManager(t *testing.T) {
	cfg := Config{
		NodeID:  "node-1",
		Address: ":50051",
		Region:  "us-east",
	}
	mgr := NewManager(cfg, nil)
	if mgr.LocalNode().ID != "node-1" {
		t.Errorf("expected 'node-1', got '%s'", mgr.LocalNode().ID)
	}
}

func TestAddRemovePeer(t *testing.T) {
	mgr := NewManager(DefaultConfig(), nil)

	peer := &NodeInfo{
		ID:        "peer-1",
		Address:   "10.0.0.2:50051",
		Status:    NodeOnline,
		LastHeartbeat: time.Now(),
		Metadata:  make(map[string]string),
	}
	mgr.AddPeer(peer)

	got, ok := mgr.GetPeer("peer-1")
	if !ok {
		t.Fatal("peer not found")
	}
	if got.Address != "10.0.0.2:50051" {
		t.Errorf("wrong address: %s", got.Address)
	}

	peers := mgr.ListPeers()
	if len(peers) != 1 {
		t.Errorf("expected 1 peer, got %d", len(peers))
	}

	mgr.RemovePeer("peer-1")
	if _, ok := mgr.GetPeer("peer-1"); ok {
		t.Error("peer should be removed")
	}
}

func TestRegisterLookupStream(t *testing.T) {
	mgr := NewManager(DefaultConfig(), nil)
	mgr.RegisterStream("stream-1")

	loc, ok := mgr.LookupStream("stream-1")
	if !ok {
		t.Fatal("stream not found")
	}
	if loc.StreamID != "stream-1" {
		t.Errorf("wrong stream id: %s", loc.StreamID)
	}
	if loc.NodeID != mgr.config.NodeID {
		t.Errorf("stream should be on local node")
	}

	mgr.UnregisterStream("stream-1")
	if _, ok := mgr.LookupStream("stream-1"); ok {
		t.Error("stream should be unregistered")
	}
}

func TestRouteStreamLocal(t *testing.T) {
	mgr := NewManager(Config{NodeID: "node-1", Address: ":50051"}, nil)
	mgr.RegisterStream("stream-1")

	node, err := mgr.RouteStream("stream-1")
	if err != nil {
		t.Fatalf("route failed: %v", err)
	}
	if node.ID != "node-1" {
		t.Errorf("expected local node, got '%s'", node.ID)
	}
}

func TestRouteStreamRemote(t *testing.T) {
	mgr := NewManager(Config{NodeID: "node-1", Address: ":50051"}, nil)

	// Add a peer with a stream
	peer := &NodeInfo{
		ID:        "node-2",
		Address:   "10.0.0.2:50051",
		Status:    NodeOnline,
		LastHeartbeat: time.Now(),
		Metadata:  make(map[string]string),
	}
	mgr.AddPeer(peer)

	// Register stream on remote node (simulate)
	mgr.streams.Store("remote-stream", &StreamLocation{
		StreamID:  "remote-stream",
		NodeID:    "node-2",
		CreatedAt: time.Now(),
	})

	node, err := mgr.RouteStream("remote-stream")
	if err != nil {
		t.Fatalf("route failed: %v", err)
	}
	if node.ID != "node-2" {
		t.Errorf("expected node-2, got '%s'", node.ID)
	}
}

func TestRouteStreamNotFound(t *testing.T) {
	mgr := NewManager(DefaultConfig(), nil)
	_, err := mgr.RouteStream("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent stream")
	}
}

func TestSelectNodeForNewStream(t *testing.T) {
	mgr := NewManager(Config{NodeID: "node-1", Address: ":50051"}, nil)
	mgr.local.LoadScore.Store(50)

	peer := &NodeInfo{
		ID:        "node-2",
		Address:   "10.0.0.2:50051",
		Status:    NodeOnline,
		LastHeartbeat: time.Now(),
		Metadata:  make(map[string]string),
	}
	peer.LoadScore.Store(20)
	mgr.AddPeer(peer)

	best := mgr.SelectNodeForNewStream()
	if best == nil {
		t.Fatal("expected a node, got nil")
	}
	if best.ID != "node-2" {
		t.Errorf("expected node-2 (lower load), got '%s'", best.ID)
	}
}

func TestSelectNodeSkipsOffline(t *testing.T) {
	mgr := NewManager(Config{NodeID: "node-1", Address: ":50051"}, nil)
	mgr.local.LoadScore.Store(50)

	peer := &NodeInfo{
		ID:        "node-2",
		Address:   "10.0.0.2:50051",
		Status:    NodeOffline,
		LastHeartbeat: time.Now(),
		Metadata:  make(map[string]string),
	}
	peer.LoadScore.Store(10)
	mgr.AddPeer(peer)

	best := mgr.SelectNodeForNewStream()
	if best == nil {
		t.Fatal("expected a node, got nil")
	}
	if best.ID != "node-1" {
		t.Errorf("expected local node (peer offline), got '%s'", best.ID)
	}
}

func TestSessionCount(t *testing.T) {
	mgr := NewManager(DefaultConfig(), nil)
	mgr.IncSession()
	mgr.IncSession()
	if mgr.local.SessionCount.Load() != 2 {
		t.Errorf("expected 2 sessions, got %d", mgr.local.SessionCount.Load())
	}
	mgr.DecSession()
	if mgr.local.SessionCount.Load() != 1 {
		t.Errorf("expected 1 session, got %d", mgr.local.SessionCount.Load())
	}
}

func TestStats(t *testing.T) {
	mgr := NewManager(Config{NodeID: "node-1", Address: ":50051"}, nil)
	mgr.AddPeer(&NodeInfo{
		ID:        "node-2",
		Address:   "10.0.0.2:50051",
		Status:    NodeOnline,
		LastHeartbeat: time.Now(),
		Metadata:  make(map[string]string),
	})
	mgr.RegisterStream("s1")
	mgr.RegisterStream("s2")
	mgr.IncSession()

	stats := mgr.Stats()
	if stats.TotalNodes != 2 {
		t.Errorf("expected 2 nodes, got %d", stats.TotalNodes)
	}
	if stats.TotalStreams != 2 {
		t.Errorf("expected 2 streams, got %d", stats.TotalStreams)
	}
	if stats.TotalSessions != 1 {
		t.Errorf("expected 1 session, got %d", stats.TotalSessions)
	}
}

func TestStartStop(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HeartbeatInterval = 100 * time.Millisecond
	mgr := NewManager(cfg, nil)

	if err := mgr.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	time.Sleep(250 * time.Millisecond) // let a couple heartbeats run

	mgr.Stop()
}
