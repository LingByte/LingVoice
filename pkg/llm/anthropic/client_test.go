package anthropic_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/anthropic"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestChatModel_Generate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "sk-ant-test" {
			t.Fatalf("api key header missing")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Fatal("anthropic-version required")
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req["model"] != "claude-3-5-haiku-latest" {
			t.Fatalf("model=%v", req["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "Bonjour"},
			},
			"stop_reason": "end_turn",
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 4,
			},
		})
	}))
	defer srv.Close()

	m, err := anthropic.NewChatModel(anthropic.Config{
		APIKey:  "sk-ant-test",
		BaseURL: srv.URL + "/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.Generate(context.Background(), []*schema.Message{
		schema.SystemMessage("reply briefly"),
		schema.UserMessage("Hello"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "Bonjour" {
		t.Fatalf("content=%q", out.Content)
	}
}

func TestChatModel_Stream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n"))
	}))
	defer srv.Close()

	m, err := anthropic.NewChatModel(anthropic.Config{
		APIKey:  "sk-ant-test",
		BaseURL: srv.URL + "/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := m.Stream(context.Background(), []*schema.Message{schema.UserMessage("hey")})
	if err != nil {
		t.Fatal(err)
	}
	full, err := schema.CollectMessages(sr)
	if err != nil {
		t.Fatal(err)
	}
	if full.Content != "Hi" {
		t.Fatalf("content=%q", full.Content)
	}
}

func TestToAPIMessages_toolResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		msgs := req["messages"].([]any)
		if len(msgs) < 2 {
			t.Fatalf("messages=%v", msgs)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content":     []any{map[string]any{"type": "text", "text": "ok"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer srv.Close()

	m, _ := anthropic.NewChatModel(anthropic.Config{APIKey: "k", BaseURL: srv.URL + "/v1"})
	_, err := m.Generate(context.Background(), []*schema.Message{
		schema.UserMessage("weather?"),
		schema.AssistantMessage("", []schema.ToolCall{{
			ID: "t1", Type: "tool_use",
			Function: schema.FunctionCall{Name: "weather", Arguments: `{"city":"Paris"}`},
		}}),
		schema.ToolMessage(`{"temp":20}`, "t1", schema.WithToolName("weather")),
	})
	if err != nil {
		t.Fatal(err)
	}
}
