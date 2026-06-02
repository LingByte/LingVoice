package a2a

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandlerPushOnTerminal(t *testing.T) {
	var hits atomic.Int32
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	h := NewHandler(HandlerConfig{
		Card: AgentCard{
			Capabilities: AgentCapabilities{PushNotifications: true, Tasks: true},
		},
		PushHTTPClient: hook.Client(),
	})
	taskID := "task-1"
	h.tasks.put(&Task{
		ID: taskID, ContextID: "ctx-1",
		Status: TaskStatus{State: TaskWorking, Timestamp: nowRFC3339()},
	})
	if err := h.tasks.setPush(PushNotificationConfig{TaskID: taskID, URL: hook.URL}); err != nil {
		t.Fatal(err)
	}
	h.tasks.put(&Task{
		ID: taskID, ContextID: "ctx-1",
		Status: TaskStatus{State: TaskCompleted, Timestamp: nowRFC3339()},
	})

	deadline := time.Now().Add(2 * time.Second)
	for hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hits.Load() == 0 {
		t.Fatal("expected webhook delivery")
	}
}

func TestDeliverSync(t *testing.T) {
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()
	d := newPushDelivery(hook.Client(), true, PushRetryConfig{})
	if err := d.deliverSync(context.Background(), PushNotificationConfig{URL: hook.URL, Token: "t"}, TaskStatusUpdateEvent{
		Kind: "status-update", TaskID: "1", Final: true,
	}); err != nil {
		t.Fatal(err)
	}
}
