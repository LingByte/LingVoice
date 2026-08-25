package webrtc

import (
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// mockHandler 测试用 EventHandler
type mockHandler struct {
	events     []common.ProtocolEvent
	mediaFrames []common.MediaFrame
	dataMsgs   []common.DataMessage
}

func (h *mockHandler) OnEvent(event common.ProtocolEvent) error {
	h.events = append(h.events, event)
	return nil
}

func (h *mockHandler) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	h.mediaFrames = append(h.mediaFrames, frame)
	return nil
}

func (h *mockHandler) OnData(sessionID string, msg common.DataMessage) error {
	h.dataMsgs = append(h.dataMsgs, msg)
	return nil
}

func newTestHandler() *mockHandler {
	return &mockHandler{}
}

func newTestConfig() Config {
	cfg := DefaultConfig()
	// 使用更小的端口范围加速测试
	cfg.EphemeralUDPPortRange = [2]uint16{50000, 50100}
	cfg.PLIInterval = 10 * time.Second
	return cfg
}

// TestSessionCreate 测试会话创建
func TestSessionCreate(t *testing.T) {
	cfg := newTestConfig()
	handler := newTestHandler()
	log := zap.NewNop()

	sess, err := newSession(uuid.NewString(), cfg, handler, log)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer sess.Close()

	if sess.ID() == "" {
		t.Error("session ID is empty")
	}
	if sess.Protocol() != common.ProtocolWebRTC {
		t.Errorf("protocol = %s, want webrtc", sess.Protocol())
	}
}

// TestSessionInterfaces 测试 Session 实现的接口
func TestSessionInterfaces(t *testing.T) {
	cfg := newTestConfig()
	handler := newTestHandler()
	log := zap.NewNop()

	sess, err := newSession(uuid.NewString(), cfg, handler, log)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer sess.Close()

	// SignalSession
	var _ common.SignalSession = sess

	// MediaSession
	var _ common.MediaSession = sess

	// TrackManager
	var _ common.TrackManager = sess

	// DataSession
	var _ common.DataSession = sess
}

// TestAddRemoveTrack 测试添加和移除轨道
func TestAddRemoveTrack(t *testing.T) {
	cfg := newTestConfig()
	handler := newTestHandler()
	log := zap.NewNop()

	sess, err := newSession(uuid.NewString(), cfg, handler, log)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer sess.Close()

	// 添加音频轨道
	trackID, err := sess.AddTrack(common.TrackConfig{
		Kind:       common.TrackAudio,
		Codec:      common.CodecOpus,
		SampleRate: 48000,
		Channels:   2,
		StreamID:   "test",
		Label:      "audio",
	})
	if err != nil {
		t.Fatalf("add track: %v", err)
	}
	if trackID == "" {
		t.Error("track ID is empty")
	}

	// 验证轨道存在
	tracks := sess.Tracks()
	found := false
	for _, tr := range tracks {
		if tr.ID == trackID {
			found = true
			if tr.Kind != common.TrackAudio {
				t.Errorf("track kind = %d, want %d", tr.Kind, common.TrackAudio)
			}
		}
	}
	if !found {
		t.Error("added track not found in Tracks()")
	}

	// 移除轨道
	if err := sess.RemoveTrack(trackID); err != nil {
		t.Fatalf("remove track: %v", err)
	}

	// 验证轨道已移除
	tracks = sess.Tracks()
	for _, tr := range tracks {
		if tr.ID == trackID {
			t.Error("removed track still in Tracks()")
		}
	}
}

// TestQoS 测试 QoS 指标采集
func TestQoS(t *testing.T) {
	cfg := newTestConfig()
	handler := newTestHandler()
	log := zap.NewNop()

	sess, err := newSession(uuid.NewString(), cfg, handler, log)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer sess.Close()

	stats := sess.QoS()
	if stats.Timestamp.IsZero() {
		t.Error("QoS timestamp is zero")
	}
	if stats.Tracks == nil {
		t.Error("QoS tracks map is nil")
	}
}

// TestSendData 测试数据通道发送（未连接时应返回错误）
func TestSendData(t *testing.T) {
	cfg := newTestConfig()
	handler := newTestHandler()
	log := zap.NewNop()

	sess, err := newSession(uuid.NewString(), cfg, handler, log)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer sess.Close()

	// 未连接时发送数据应失败
	err = sess.SendData("reliable", []byte("test"))
	if err == nil {
		t.Error("expected error sending data on non-existent channel")
	}
}

// TestIceRestart 测试 ICE restart 命令
func TestIceRestart(t *testing.T) {
	cfg := newTestConfig()
	handler := newTestHandler()
	log := zap.NewNop()

	sess, err := newSession(uuid.NewString(), cfg, handler, log)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer sess.Close()

	// ICE restart 在未连接状态下可能失败，但不应 panic
	_ = sess.SendCommand(common.ProtocolCommand{
		Type: common.CmdIceRestart,
	})
}

// TestServerCreate 测试服务器创建
func TestServerCreate(t *testing.T) {
	cfg := newTestConfig()
	handler := newTestHandler()
	log := zap.NewNop()

	srv := NewServer(cfg, handler, log)
	if srv == nil {
		t.Fatal("server is nil")
	}

	// 测试 Handler 方法返回非 nil
	h := srv.Handler()
	if h == nil {
		t.Error("handler is nil")
	}
}
