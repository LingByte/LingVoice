package openai_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/openai"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestChatModel_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_api_key","message":"bad key"}}`))
	}))
	defer srv.Close()

	m, err := openai.NewChatModel(openai.Config{APIKey: "bad", BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewChatModel_Validation(t *testing.T) {
	if _, err := openai.NewChatModel(openai.Config{}); err == nil {
		t.Fatal("expected missing api key")
	}
}

func TestChatModel_WithToolsAndName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	m, err := openai.NewChatModel(openai.Config{APIKey: "k", BaseURL: srv.URL + "/v1", Model: "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Name() != "openai/gpt-test" {
		t.Fatalf("name=%q", m.Name())
	}
	bound, err := m.WithTools([]*schema.ToolInfo{{Name: "add"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := bound.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil || out.Content != "ok" {
		t.Fatalf("err=%v out=%v", err, out)
	}
}

func TestChatModel_EmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`))
	}))
	defer srv.Close()

	m, err := openai.NewChatModel(openai.Config{APIKey: "k", BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err == nil {
		t.Fatal("expected empty choices error")
	}
}

func TestChatModel_StreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid","message":"bad"}}`))
	}))
	defer srv.Close()

	m, err := openai.NewChatModel(openai.Config{APIKey: "k", BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := m.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sr.Recv()
	if err == nil {
		t.Fatal("expected stream recv error")
	}
}
