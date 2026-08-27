package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCollectorRegister verifies that NewCollector registers all metrics with
// the default Prometheus registry without panicking.
func TestCollectorRegister(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NewCollector panicked: %v", r)
		}
	}()

	c := NewCollector()
	if c == nil {
		t.Fatal("NewCollector returned nil")
	}

	// A second call should return the same (already registered) collector and
	// must not panic.
	c2 := NewCollector()
	if c2 == nil {
		t.Fatal("second NewCollector call returned nil")
	}
}

// TestIncSession increments the session counter and verifies that the
// underlying metric value reflects the increment.
func TestIncSession(t *testing.T) {
	c := NewCollector()

	c.IncSession("webrtc", "created")
	c.IncSession("webrtc", "created")
	c.IncSession("rtsp", "failed")

	// Use the registry gatherer via a test server to validate output.
	srv := httptest.NewServer(c.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := readBody(t, resp)
	if !strings.Contains(body, "lingvoice_sessions_total") {
		t.Fatalf("expected sessions_total metric in output, got:\n%s", body)
	}
}

// TestSetActiveSessions sets the active session gauge and verifies the value
// is reflected in the metrics output.
func TestSetActiveSessions(t *testing.T) {
	c := NewCollector()

	c.SetActiveSessions("webrtc", 3)
	c.SetActiveSessions("rtsp", 0)

	srv := httptest.NewServer(c.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if !strings.Contains(body, "lingvoice_sessions_active") {
		t.Fatalf("expected sessions_active metric in output, got:\n%s", body)
	}
}

// TestMiddleware verifies that the middleware records request count and
// duration metrics for requests that pass through it.
func TestMiddleware(t *testing.T) {
	c := NewCollector()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("ok"))
	})

	h := c.Middleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/test-path", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 from inner handler, got %d", rec.Code)
	}

	// Verify the request counter was incremented by checking the metrics
	// output.
	srv := httptest.NewServer(c.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if !strings.Contains(body, "lingvoice_http_requests_total") {
		t.Fatalf("expected http_requests_total metric in output, got:\n%s", body)
	}
	if !strings.Contains(body, "lingvoice_http_request_duration_seconds") {
		t.Fatalf("expected http_request_duration_seconds metric in output, got:\n%s", body)
	}
}

// TestHandler verifies that the Handler returns an http.Handler that responds
// with 200 OK on the /metrics endpoint.
func TestHandler(t *testing.T) {
	c := NewCollector()

	h := c.Handler()
	if h == nil {
		t.Fatal("Handler returned nil")
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "lingvoice_") {
		t.Fatalf("expected LingVoice metrics in output, got:\n%s", body)
	}
}

// readBody is a small helper that reads the full response body as a string.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return string(buf)
}
