package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
)

type mockSessionManager struct {
	sessions map[string]common.ProtocolSession
}

type mockSession struct {
	id       string
	protocol common.ProtocolType
}

func (m *mockSession) ID() string                       { return m.id }
func (m *mockSession) Protocol() common.ProtocolType    { return m.protocol }
func (m *mockSession) SendCommand(cmd common.ProtocolCommand) error { return nil }
func (m *mockSession) Close() error                     { return nil }

func (mm *mockSessionManager) GetSession(id string) (common.ProtocolSession, bool) {
	s, ok := mm.sessions[id]
	return s, ok
}

func (mm *mockSessionManager) ListSessions() []common.ProtocolSession {
	list := make([]common.ProtocolSession, 0, len(mm.sessions))
	for _, s := range mm.sessions {
		list = append(list, s)
	}
	return list
}

func (mm *mockSessionManager) SendCommand(sessionID string, cmd common.ProtocolCommand) error {
	return nil
}

func (mm *mockSessionManager) SendMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	return nil
}

func TestDashboard(t *testing.T) {
	mgr := &mockSessionManager{
		sessions: map[string]common.ProtocolSession{
			"s1": &mockSession{id: "s1", protocol: common.ProtocolRTMP},
			"s2": &mockSession{id: "s2", protocol: common.ProtocolWebRTC},
		},
	}
	srv := NewServer(DefaultConfig(), mgr, nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/dashboard", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var data dashboardData
	if err := json.NewDecoder(rec.Body).Decode(&data); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if data.TotalSessions != 2 {
		t.Errorf("expected 2 sessions, got %d", data.TotalSessions)
	}
}

func TestSessionsList(t *testing.T) {
	mgr := &mockSessionManager{
		sessions: map[string]common.ProtocolSession{
			"s1": &mockSession{id: "s1", protocol: common.ProtocolRTMP},
		},
	}
	srv := NewServer(DefaultConfig(), mgr, nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/sessions", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestSessionHangup(t *testing.T) {
	mgr := &mockSessionManager{
		sessions: map[string]common.ProtocolSession{
			"s1": &mockSession{id: "s1", protocol: common.ProtocolRTMP},
		},
	}
	srv := NewServer(DefaultConfig(), mgr, nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/api/sessions/s1/hangup", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestSessionDelete(t *testing.T) {
	mgr := &mockSessionManager{
		sessions: map[string]common.ProtocolSession{
			"s1": &mockSession{id: "s1", protocol: common.ProtocolRTMP},
		},
	}
	srv := NewServer(DefaultConfig(), mgr, nil)

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/sessions/s1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestSessionNotFound(t *testing.T) {
	mgr := &mockSessionManager{sessions: map[string]common.ProtocolSession{}}
	srv := NewServer(DefaultConfig(), mgr, nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/sessions/nonexistent", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestUIPage(t *testing.T) {
	mgr := &mockSessionManager{sessions: map[string]common.ProtocolSession{}}
	srv := NewServer(DefaultConfig(), mgr, nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("expected text/html, got '%s'", rec.Header().Get("Content-Type"))
	}
}

func TestAuth(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Username = "admin"
	cfg.Password = "secret"
	mgr := &mockSessionManager{sessions: map[string]common.ProtocolSession{}}
	srv := NewServer(cfg, mgr, nil)

	// Without auth
	req := httptest.NewRequest(http.MethodGet, "/admin/api/dashboard", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", rec.Code)
	}

	// With auth
	req = httptest.NewRequest(http.MethodGet, "/admin/api/dashboard", nil)
	req.SetBasicAuth("admin", "secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with auth, got %d", rec.Code)
	}
}
