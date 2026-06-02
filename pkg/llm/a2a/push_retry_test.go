package a2a

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPushRetryThenSuccess(t *testing.T) {
	var hits atomic.Int32
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	d := newPushDelivery(hook.Client(), true, PushRetryConfig{
		MaxRetries: 3,
		Backoff:    10 * time.Millisecond,
	})
	d.deliverWithRetry(PushNotificationConfig{TaskID: "t1", URL: hook.URL}, TaskStatusUpdateEvent{
		Kind: "status-update", TaskID: "t1", Final: true,
	})

	deadline := time.Now().Add(2 * time.Second)
	for hits.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hits.Load() != 3 {
		t.Fatalf("attempts=%d", hits.Load())
	}
	if len(d.deadLetter.list()) != 0 {
		t.Fatalf("unexpected dead letters: %+v", d.deadLetter.list())
	}
}

func TestPushDeadLetterAfterRetries(t *testing.T) {
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer hook.Close()

	d := newPushDelivery(hook.Client(), true, PushRetryConfig{
		MaxRetries:    2,
		Backoff:       5 * time.Millisecond,
		DeadLetterMax: 5,
	})
	d.deliverWithRetry(PushNotificationConfig{TaskID: "t2", URL: hook.URL}, TaskStatusUpdateEvent{
		Kind: "status-update", TaskID: "t2", Final: true,
	})

	deadline := time.Now().Add(2 * time.Second)
	for len(d.deadLetter.list()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	entries := d.deadLetter.list()
	if len(entries) != 1 {
		t.Fatalf("entries=%+v", entries)
	}
	if entries[0].TaskID != "t2" || entries[0].Attempts != 3 {
		t.Fatalf("entry=%+v", entries[0])
	}
}

func TestHandlerListPushDeadLetters(t *testing.T) {
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer hook.Close()

	h := NewHandler(HandlerConfig{
		Card: AgentCard{Capabilities: AgentCapabilities{PushNotifications: true, Tasks: true}},
		PushHTTPClient: hook.Client(),
		PushRetry: PushRetryConfig{MaxRetries: 0, Backoff: time.Millisecond, DeadLetterMax: 10},
	})
	h.tasks.put(&Task{ID: "t3", ContextID: "c", Status: TaskStatus{State: TaskWorking}})
	_ = h.tasks.setPush(PushNotificationConfig{TaskID: "t3", URL: hook.URL})
	h.tasks.put(&Task{ID: "t3", ContextID: "c", Status: TaskStatus{State: TaskCompleted}})

	deadline := time.Now().Add(2 * time.Second)
	for len(h.ListPushDeadLetters()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(h.ListPushDeadLetters()) != 1 {
		t.Fatalf("dead letters=%+v", h.ListPushDeadLetters())
	}
}
