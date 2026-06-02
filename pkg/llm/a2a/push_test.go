package a2a_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/a2a"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestPushNotificationDelivery(t *testing.T) {
	var mu sync.Mutex
	var deliveries []a2a.TaskStatusUpdateEvent
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev a2a.TaskStatusUpdateEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			t.Errorf("push body: %v", err)
		}
		mu.Lock()
		deliveries = append(deliveries, ev)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	gate := make(chan struct{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	agent := a2a.NewHandler(a2a.HandlerConfig{
		Card: a2a.AgentCard{
			Name:               "push-agent",
			URL:                "/",
			PreferredTransport: a2a.TransportJSONRPC,
			Capabilities:       a2a.AgentCapabilities{Tasks: true, PushNotifications: true},
		},
		Handler: func(_ context.Context, _ *a2a.MessageRequest) (*schema.Message, error) {
			<-gate
			return schema.AssistantMessage("done", nil), nil
		},
		PushHTTPClient: hook.Client(),
	})
	srv := &http.Server{Handler: agent.HTTPHandler()}
	go func() { _ = srv.Serve(ln) }()
	defer shutdown(srv)

	baseURL := "http://" + ln.Addr().String()
	blocking := false
	body, _ := json.Marshal(a2a.JSONRPCRequest{
		JSONRPC: a2a.JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  a2a.MethodMessageSend,
		Params: mustRaw(a2a.MessageSendParams{
			Message: a2a.A2AMessage{
				Kind: "message", Role: "user", MessageID: "m1",
				Parts: []a2a.Part{{Kind: "text", Text: "ping"}},
			},
			Configuration: &a2a.MessageSendConfiguration{Blocking: &blocking},
		}),
	})
	resp, err := http.Post(baseURL+"/", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var out a2a.JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	b, _ := json.Marshal(out.Result)
	var task a2a.Task
	if err := json.Unmarshal(b, &task); err != nil || task.ID == "" {
		t.Fatalf("task=%+v err=%v", task, err)
	}

	client := a2a.NewClient()
	if _, err := client.SetPushNotificationConfig(context.Background(), baseURL, a2a.PushNotificationSetParams{
		TaskID: task.ID,
		Config: a2a.PushNotificationConfig{URL: hook.URL, Token: "secret"},
	}); err != nil {
		t.Fatal(err)
	}
	close(gate)

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(deliveries)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expected push delivery")
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if deliveries[0].TaskID != task.ID || !deliveries[0].Final {
		t.Fatalf("delivery=%+v", deliveries[0])
	}
}
