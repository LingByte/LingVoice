package moq

import (
	"testing"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
)

type mockHandler struct {
	events      []common.ProtocolEvent
	mediaFrames []common.MediaFrame
}

func (m *mockHandler) OnEvent(event common.ProtocolEvent) error {
	m.events = append(m.events, event)
	return nil
}

func (m *mockHandler) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	m.mediaFrames = append(m.mediaFrames, frame)
	return nil
}

func (m *mockHandler) OnData(sessionID string, msg common.DataMessage) error {
	return nil
}

func TestCreateSession(t *testing.T) {
	srv := NewServer(DefaultConfig(), &mockHandler{}, nil)
	sess := srv.CreateSession()
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if sess.id == "" {
		t.Error("session ID should not be empty")
	}
}

func TestSubscribe(t *testing.T) {
	h := &mockHandler{}
	srv := NewServer(DefaultConfig(), h, nil)
	sess := srv.CreateSession()

	sub, err := srv.HandleSubscribe(sess.id, "video/track1", 1)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if sub.TrackName != "video/track1" {
		t.Errorf("expected 'video/track1', got '%s'", sub.TrackName)
	}

	// Should have triggered EventTrackAdded
	if len(h.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(h.events))
	}
	if h.events[0].Type != common.EventTrackAdded {
		t.Errorf("expected EventTrackAdded, got %d", h.events[0].Type)
	}
}

func TestUnsubscribe(t *testing.T) {
	srv := NewServer(DefaultConfig(), &mockHandler{}, nil)
	sess := srv.CreateSession()

	sub, _ := srv.HandleSubscribe(sess.id, "track1", 1)
	err := srv.HandleUnsubscribe(sess.id, sub.ID)
	if err != nil {
		t.Fatalf("unsubscribe failed: %v", err)
	}
}

func TestAnnounce(t *testing.T) {
	h := &mockHandler{}
	srv := NewServer(DefaultConfig(), h, nil)
	sess := srv.CreateSession()

	err := srv.HandleAnnounce(sess.id, "namespace/video")
	if err != nil {
		t.Fatalf("announce failed: %v", err)
	}

	// Should trigger EventIncomingCall
	if len(h.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(h.events))
	}
	if h.events[0].Type != common.EventIncomingCall {
		t.Errorf("expected EventIncomingCall, got %d", h.events[0].Type)
	}
}

func TestHandleObject(t *testing.T) {
	h := &mockHandler{}
	srv := NewServer(DefaultConfig(), h, nil)
	sess := srv.CreateSession()

	srv.HandleSubscribe(sess.id, "track1", 1)
	err := srv.HandleObject(sess.id, 0, []byte{1, 2, 3, 4}, 1000)
	if err != nil {
		t.Fatalf("handle object failed: %v", err)
	}

	if len(h.mediaFrames) != 1 {
		t.Fatalf("expected 1 media frame, got %d", len(h.mediaFrames))
	}
}

func TestGoAway(t *testing.T) {
	h := &mockHandler{}
	srv := NewServer(DefaultConfig(), h, nil)
	sess := srv.CreateSession()

	srv.HandleGoAway(sess.id)

	// Session should be removed
	if _, ok := srv.GetSession(sess.id); ok {
		t.Error("session should be removed after GOAWAY")
	}

	// Should trigger EventHangup
	found := false
	for _, e := range h.events {
		if e.Type == common.EventHangup {
			found = true
		}
	}
	if !found {
		t.Error("expected EventHangup")
	}
}

func TestSessionStats(t *testing.T) {
	srv := NewServer(DefaultConfig(), &mockHandler{}, nil)
	sess := srv.CreateSession()
	srv.HandleSubscribe(sess.id, "track1", 1)
	srv.HandleSubscribe(sess.id, "track2", 2)
	srv.HandleAnnounce(sess.id, "ns1")

	stats, err := srv.GetSessionStats(sess.id)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if stats.Subscriptions != 2 {
		t.Errorf("expected 2 subs, got %d", stats.Subscriptions)
	}
	if stats.Announcements != 1 {
		t.Errorf("expected 1 announcement, got %d", stats.Announcements)
	}
}

func TestListSessions(t *testing.T) {
	srv := NewServer(DefaultConfig(), &mockHandler{}, nil)
	srv.CreateSession()
	srv.CreateSession()

	list := srv.ListSessions()
	if len(list) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(list))
	}
}

func TestSendCommand(t *testing.T) {
	h := &mockHandler{}
	srv := NewServer(DefaultConfig(), h, nil)
	sess := srv.CreateSession()

	err := srv.SendCommand(sess.id, common.ProtocolCommand{Type: common.CmdHangup})
	if err != nil {
		t.Fatalf("send command failed: %v", err)
	}

	if _, ok := srv.GetSession(sess.id); ok {
		t.Error("session should be gone after hangup")
	}
}

func TestMessageTypeString(t *testing.T) {
	tests := []struct {
		msg  MessageType
		name string
	}{
		{MsgClientSetup, "CLIENT_SETUP"},
		{MsgSubscribe, "SUBSCRIBE"},
		{MsgAnnounce, "ANNOUNCE"},
		{MsgObject, "OBJECT"},
	}
	for _, tt := range tests {
		if got := tt.msg.String(); got != tt.name {
			t.Errorf("expected '%s', got '%s'", tt.name, got)
		}
	}
}

func TestStartClose(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Addr = "127.0.0.1:0" // 使用随机端口避免冲突
	srv := NewServer(cfg, &mockHandler{}, nil)
	if err := srv.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}
