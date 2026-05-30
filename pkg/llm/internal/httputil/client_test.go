package httputil_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/internal/httputil"
)

func TestDoJSON_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		if r.Header.Get("X-Test") != "1" {
			t.Fatal("missing header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var out struct {
		OK bool `json:"ok"`
	}
	err := httputil.DoJSON(context.Background(), nil, http.MethodPost, srv.URL, map[string]string{"X-Test": "1"}, map[string]string{"q": "1"}, &out)
	if err != nil || !out.OK {
		t.Fatalf("err=%v out=%+v", err, out)
	}
}

func TestDoJSON_Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"bad"}}`))
	}))
	defer srv.Close()

	var out map[string]any
	err := httputil.DoJSON(context.Background(), nil, http.MethodGet, srv.URL, nil, nil, &out)
	if err == nil {
		t.Fatal("expected error")
	}
	he, ok := err.(*httputil.HTTPError)
	if !ok || he.StatusCode != 400 || he.Code != "invalid_request" {
		t.Fatalf("err=%v", err)
	}
	if he.Error() == "" {
		t.Fatal("error string")
	}

	badJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer badJSON.Close()
	err = httputil.DoJSON(context.Background(), nil, http.MethodGet, badJSON.URL, nil, nil, &out)
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestReadSSE_AndPostSSE(t *testing.T) {
	var chunks []string
	body := strings.NewReader(`: comment
event: ping
data: {"a":1}

data: {"b":2}

data:

data: [DONE]
`)
	if err := httputil.ReadSSE(body, func(data string) error {
		chunks = append(chunks, data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 3 || chunks[0] != `{"a":1}` || chunks[2] != "[DONE]" {
		t.Fatalf("chunks=%v", chunks)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("accept=%q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"x\":1}\n\n")
	}))
	defer srv.Close()
	var got []string
	if err := httputil.PostSSE(context.Background(), nil, srv.URL, nil, map[string]any{"stream": true}, func(data string) error {
		got = append(got, data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got=%v", got)
	}

	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"server_error"}`))
	}))
	defer failSrv.Close()
	if err := httputil.PostSSE(context.Background(), nil, failSrv.URL, nil, map[string]any{}, func(string) error { return nil }); err == nil {
		t.Fatal("expected post sse error")
	}
}

func TestNewHTTPError_ParseCodes(t *testing.T) {
	he := httputil.NewHTTPError(503, `{"error":{"type":"overloaded"}}`)
	if he.Code != "overloaded" {
		t.Fatalf("code=%q", he.Code)
	}
	he2 := httputil.NewHTTPError(500, `{"type":"api_error"}`)
	if he2.Code != "api_error" {
		t.Fatalf("code=%q", he2.Code)
	}
}
