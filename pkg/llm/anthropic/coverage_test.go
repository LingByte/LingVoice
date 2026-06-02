package anthropic_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/anthropic"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestNewChatModel_Validation(t *testing.T) {
	if _, err := anthropic.NewChatModel(anthropic.Config{}); err == nil {
		t.Fatal("expected missing api key")
	}
}

func TestChatModel_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"bad key"}}`))
	}))
	defer srv.Close()

	m, err := anthropic.NewChatModel(anthropic.Config{APIKey: "bad", BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestChatModel_WithToolsAndName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	m, err := anthropic.NewChatModel(anthropic.Config{APIKey: "k", BaseURL: srv.URL + "/v1", Model: "claude-test"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Name() != "anthropic/claude-test" {
		t.Fatalf("name=%q", m.Name())
	}
	bound, err := m.WithTools([]*schema.ToolInfo{{Name: "weather", Desc: "forecast"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := bound.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil || out.Content != "ok" {
		t.Fatalf("err=%v out=%v", err, out)
	}
}

func TestChatModel_StreamEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":3,"output_tokens":0}}}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"lookup"}}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{}"}}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":3,"output_tokens":2}}` + "\n\n"))
	}))
	defer srv.Close()

	m, err := anthropic.NewChatModel(anthropic.Config{APIKey: "k", BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := m.Stream(context.Background(), []*schema.Message{schema.UserMessage("tool")})
	if err != nil {
		t.Fatal(err)
	}
	full, err := schema.CollectMessages(sr)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.ToolCalls) == 0 {
		t.Fatalf("tool calls=%v", full.ToolCalls)
	}
}

func TestChatModel_NilReceiver(t *testing.T) {
	var m *anthropic.ChatModel
	if m.Name() != "" {
		t.Fatal("nil name")
	}
	if _, err := m.Generate(context.Background(), nil); err == nil {
		t.Fatal("expected nil model error")
	}
	if _, err := m.Stream(context.Background(), nil); err == nil {
		t.Fatal("expected nil model stream error")
	}
}
