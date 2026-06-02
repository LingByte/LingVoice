package a2a

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRedrivePushDeadLetters(t *testing.T) {
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	h := NewHandler(HandlerConfig{
		Card:           AgentCard{Capabilities: AgentCapabilities{PushNotifications: true, Tasks: true}},
		PushHTTPClient: hook.Client(),
		PushRetry:      PushRetryConfig{MaxRetries: 0, Backoff: time.Millisecond, DeadLetterMax: 10},
	})
	h.push.deadLetter.add(PushDeadLetter{
		TaskID: "t1",
		URL:    hook.URL,
		Event:  TaskStatusUpdateEvent{Kind: "status-update", TaskID: "t1", Final: true},
	})
	result := h.RedrivePushDeadLetters("t1")
	if result.Redriven != 1 || result.Failed != 0 {
		t.Fatalf("result=%+v", result)
	}
	if len(h.ListPushDeadLetters()) != 0 {
		t.Fatalf("dead letters=%+v", h.ListPushDeadLetters())
	}
}

func TestRedrivePushDeadLettersRequeuesOnFailure(t *testing.T) {
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer hook.Close()

	h := NewHandler(HandlerConfig{
		Card:           AgentCard{Capabilities: AgentCapabilities{PushNotifications: true}},
		PushHTTPClient: hook.Client(),
		PushRetry:      PushRetryConfig{MaxRetries: 0, Backoff: time.Millisecond, DeadLetterMax: 10},
	})
	h.push.deadLetter.add(PushDeadLetter{
		TaskID: "t2",
		URL:    hook.URL,
		Event:  TaskStatusUpdateEvent{Kind: "status-update", TaskID: "t2", Final: true},
	})
	result := h.RedrivePushDeadLetters("t2")
	if result.Redriven != 0 || result.Failed != 1 {
		t.Fatalf("result=%+v", result)
	}
	if len(h.ListPushDeadLetters()) != 1 {
		t.Fatalf("dead letters=%+v", h.ListPushDeadLetters())
	}
}
