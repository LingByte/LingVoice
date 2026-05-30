package openai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/openai"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestChatModel_Generate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer sk-test") {
			t.Fatalf("auth=%q", r.Header.Get("Authorization"))
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req["model"] != "gpt-4o-mini" {
			t.Fatalf("model=%v", req["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{
					"message":       map[string]any{"role": "assistant", "content": "Hi there"},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     5,
				"completion_tokens": 3,
				"total_tokens":      8,
			},
		})
	}))
	defer srv.Close()

	m, err := openai.NewChatModel(openai.Config{
		APIKey:  "sk-test",
		BaseURL: srv.URL + "/v1",
		Model:   "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.Generate(context.Background(), []*schema.Message{
		schema.UserMessage("Hello"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "Hi there" {
		t.Fatalf("content=%q", out.Content)
	}
	if out.ResponseMeta == nil || out.ResponseMeta.Usage.TotalTokens != 8 {
		t.Fatalf("usage=%+v", out.ResponseMeta)
	}
}

func TestChatModel_Stream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if opts, ok := req["stream_options"].(map[string]any); !ok || opts["include_usage"] != true {
			t.Fatalf("stream_options=%v", req["stream_options"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	m, err := openai.NewChatModel(openai.Config{
		APIKey:  "sk-test",
		BaseURL: srv.URL + "/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := m.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	full, err := schema.CollectMessages(sr)
	if err != nil {
		t.Fatal(err)
	}
	if full.Content != "Hello" {
		t.Fatalf("content=%q", full.Content)
	}
	if full.ResponseMeta == nil || full.ResponseMeta.Usage == nil || full.ResponseMeta.Usage.TotalTokens != 7 {
		t.Fatalf("usage=%+v", full.ResponseMeta)
	}
}
