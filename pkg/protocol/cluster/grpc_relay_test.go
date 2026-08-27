package cluster

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestServer 创建一个模拟 peer 节点的 httptest.Server, 根据 path 将
// 请求体原样解码后返回预设响应。handler 允许调用方按 path 定制响应。
func newTestServer(t *testing.T, handler map[string]any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, resp := range handler {
		resp := resp // capture
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			// 读取并丢弃请求体, 仅校验可解码
			var body any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
				t.Errorf("decode request body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("encode response: %v", err)
			}
		})
	}
	return httptest.NewServer(mux)
}

func TestSendHeartbeat(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/relay/heartbeat": HeartbeatResponse{NodeID: "peer-1", LoadScore: 42},
	})
	defer srv.Close()

	client := NewGrpcRelayClient(srv.Listener.Addr().String())
	defer client.Close()

	resp, err := client.SendHeartbeat(context.Background(), HeartbeatRequest{
		NodeID:       "node-1",
		Address:      ":50051",
		LoadScore:    10,
		SessionCount: 3,
		StreamCount:  2,
	})
	if err != nil {
		t.Fatalf("SendHeartbeat: %v", err)
	}
	if resp.NodeID != "peer-1" {
		t.Errorf("expected peer-1, got %s", resp.NodeID)
	}
	if resp.LoadScore != 42 {
		t.Errorf("expected load 42, got %d", resp.LoadScore)
	}
}

func TestRelayMedia(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/relay/media": RelayMediaResponse{Success: true},
	})
	defer srv.Close()

	client := NewGrpcRelayClient(srv.Listener.Addr().String())
	defer client.Close()

	resp, err := client.RelayMedia(context.Background(), RelayMediaRequest{
		FromNode: "node-1",
		ToNode:   "peer-1",
		StreamID: "stream-1",
		Payload:  []byte("audio-frame"),
		IsVideo:  false,
	})
	if err != nil {
		t.Fatalf("RelayMedia: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success, got error: %s", resp.Error)
	}
}

func TestRelaySignal(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/relay/signal": RelaySignalResponse{Success: true, Payload: []byte("ack")},
	})
	defer srv.Close()

	client := NewGrpcRelayClient(srv.Listener.Addr().String())
	defer client.Close()

	resp, err := client.RelaySignal(context.Background(), RelaySignalRequest{
		FromNode:   "node-1",
		ToNode:     "peer-1",
		StreamID:   "stream-1",
		SignalType: "offer",
		Payload:    []byte("sdp"),
	})
	if err != nil {
		t.Fatalf("RelaySignal: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success")
	}
	if string(resp.Payload) != "ack" {
		t.Errorf("expected payload 'ack', got %q", string(resp.Payload))
	}
}

func TestLookupStream(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/relay/lookup": LookupStreamResponse{
			Found:        true,
			NodeID:       "peer-1",
			ReplicaNodes: []string{"peer-2"},
		},
	})
	defer srv.Close()

	client := NewGrpcRelayClient(srv.Listener.Addr().String())
	defer client.Close()

	resp, err := client.LookupStream(context.Background(), LookupStreamRequest{StreamID: "stream-1"})
	if err != nil {
		t.Fatalf("LookupStream: %v", err)
	}
	if !resp.Found {
		t.Errorf("expected found=true")
	}
	if resp.NodeID != "peer-1" {
		t.Errorf("expected node peer-1, got %s", resp.NodeID)
	}
	if len(resp.ReplicaNodes) != 1 || resp.ReplicaNodes[0] != "peer-2" {
		t.Errorf("unexpected replicas: %v", resp.ReplicaNodes)
	}
}

func TestRelayConnectionFailure(t *testing.T) {
	// 使用一个保证连接失败的地址 (未被监听的端口)
	client := NewGrpcRelayClient("127.0.0.1:1")
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.SendHeartbeat(ctx, HeartbeatRequest{NodeID: "node-1"})
	if err == nil {
		t.Fatal("expected error for unreachable peer, got nil")
	}
}

// TestManagerRelayToNodeMedia 验证 Manager.RelayToNode 通过 GrpcRelayClient
// 成功转发 media 消息。
func TestManagerRelayToNodeMedia(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/relay/media": RelayMediaResponse{Success: true},
	})
	defer srv.Close()

	mgr := NewManager(Config{NodeID: "node-1", Address: ":50051"}, nil)
	peer := &NodeInfo{
		ID:            "peer-1",
		Address:       srv.Listener.Addr().String(),
		Status:        NodeOnline,
		LastHeartbeat: time.Now(),
		Metadata:      make(map[string]string),
	}
	mgr.AddPeer(peer)

	err := mgr.RelayToNode(context.Background(), "peer-1", RelayMessage{
		FromNode: "node-1",
		ToNode:   "peer-1",
		StreamID: "stream-1",
		Type:     "media",
		Payload:  []byte("frame"),
	})
	if err != nil {
		t.Fatalf("RelayToNode media: %v", err)
	}
}

// TestManagerRelayToNodeSignal 验证 signal 转发。
func TestManagerRelayToNodeSignal(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/relay/signal": RelaySignalResponse{Success: true, Payload: []byte("ok")},
	})
	defer srv.Close()

	mgr := NewManager(Config{NodeID: "node-1", Address: ":50051"}, nil)
	peer := &NodeInfo{
		ID:            "peer-1",
		Address:       srv.Listener.Addr().String(),
		Status:        NodeOnline,
		LastHeartbeat: time.Now(),
		Metadata:      make(map[string]string),
	}
	mgr.AddPeer(peer)

	err := mgr.RelayToNode(context.Background(), "peer-1", RelayMessage{
		FromNode: "node-1",
		ToNode:   "peer-1",
		StreamID: "stream-1",
		Type:     "signal",
		Payload:  []byte("sdp"),
	})
	if err != nil {
		t.Fatalf("RelayToNode signal: %v", err)
	}
}

// TestManagerRelayToNodeFailure 验证连接失败时 peer 被标记为降级。
func TestManagerRelayToNodeFailure(t *testing.T) {
	mgr := NewManager(Config{NodeID: "node-1", Address: ":50051"}, nil)
	peer := &NodeInfo{
		ID:            "peer-1",
		Address:       "127.0.0.1:1", // 不可达
		Status:        NodeOnline,
		LastHeartbeat: time.Now(),
		Metadata:      make(map[string]string),
	}
	mgr.AddPeer(peer)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := mgr.RelayToNode(ctx, "peer-1", RelayMessage{
		FromNode: "node-1",
		ToNode:   "peer-1",
		StreamID: "stream-1",
		Type:     "media",
		Payload:  []byte("frame"),
	})
	if err == nil {
		t.Fatal("expected error for unreachable peer")
	}

	got, ok := mgr.GetPeer("peer-1")
	if !ok {
		t.Fatal("peer missing")
	}
	if got.Status != NodeDegraded {
		t.Errorf("expected peer degraded, got %s", got.Status)
	}
}

// TestManagerSendHeartbeats 验证 sendHeartbeats 向 peer 发送心跳并更新状态。
func TestManagerSendHeartbeats(t *testing.T) {
	srv := newTestServer(t, map[string]any{
		"/relay/heartbeat": HeartbeatResponse{NodeID: "peer-1", LoadScore: 33},
	})
	defer srv.Close()

	mgr := NewManager(Config{NodeID: "node-1", Address: ":50051"}, nil)
	mgr.local.LoadScore.Store(10)
	mgr.local.SessionCount.Store(2)
	mgr.local.StreamCount.Store(1)

	peer := &NodeInfo{
		ID:            "peer-1",
		Address:       srv.Listener.Addr().String(),
		Status:        NodeOnline,
		LastHeartbeat: time.Now().Add(-1 * time.Hour), // 旧时间, 应被刷新
		Metadata:      make(map[string]string),
	}
	mgr.AddPeer(peer)

	mgr.sendHeartbeats()

	got, ok := mgr.GetPeer("peer-1")
	if !ok {
		t.Fatal("peer missing")
	}
	if got.LoadScore.Load() != 33 {
		t.Errorf("expected load 33, got %d", got.LoadScore.Load())
	}
	if time.Since(got.LastHeartbeat) > time.Second {
		t.Errorf("LastHeartbeat not updated: %v", got.LastHeartbeat)
	}
}

// TestManagerSendHeartbeatsFailure 验证心跳失败时 peer 被标记为降级。
func TestManagerSendHeartbeatsFailure(t *testing.T) {
	mgr := NewManager(Config{NodeID: "node-1", Address: ":50051"}, nil)
	peer := &NodeInfo{
		ID:            "peer-1",
		Address:       "127.0.0.1:1", // 不可达
		Status:        NodeOnline,
		LastHeartbeat: time.Now(),
		Metadata:      make(map[string]string),
	}
	mgr.AddPeer(peer)

	mgr.sendHeartbeats()

	got, ok := mgr.GetPeer("peer-1")
	if !ok {
		t.Fatal("peer missing")
	}
	if got.Status != NodeDegraded {
		t.Errorf("expected peer degraded after heartbeat failure, got %s", got.Status)
	}
}
