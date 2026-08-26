package auth

import (
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTokenAuth(t *testing.T) {
	cfg := Config{
		Mode:   ModeToken,
		Tokens: map[string]bool{"secret123": true},
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := Middleware(cfg, next)

	// 无 token → 401
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	// 正确 token → 200
	req = httptest.NewRequest("GET", "/api/sessions?token=secret123", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !called {
		t.Fatal("handler not called")
	}

	// Bearer header
	called = false
	req = httptest.NewRequest("GET", "/api/sessions", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with Bearer, got %d", w.Code)
	}
	if !called {
		t.Fatal("handler not called with Bearer")
	}

	// 错误 token → 401
	req = httptest.NewRequest("GET", "/api/sessions?token=wrong", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", w.Code)
	}
}

func TestSignAuth(t *testing.T) {
	cfg := Config{
		Mode:       ModeSign,
		Secret:     "mysecret",
		MaxSkewSec: 300,
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := Middleware(cfg, next)

	// 无签名 → 401
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	// 正确签名
	ts := "1700000000"
	// md5("/api/sessions" + ts + "mysecret")
	sign := md5hex("/api/sessions" + ts + "mysecret")
	req = httptest.NewRequest("GET", "/api/sessions?sign="+sign+"&ts="+ts, nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	// 时间戳太旧 → 401
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for old timestamp, got %d", w.Code)
	}
}

func TestDisabledAuth(t *testing.T) {
	cfg := Config{Mode: ModeDisabled}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	handler := Middleware(cfg, next)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if !called {
		t.Fatal("handler should be called when auth disabled")
	}
}

func md5hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}
