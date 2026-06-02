package a2a_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/a2a"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestClientSubscribeToTask(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := a2a.NewHandler(a2a.HandlerConfig{
		Card: a2a.AgentCard{
			Name:               "sub",
			URL:                "/",
			PreferredTransport: a2a.TransportJSONRPC,
			Capabilities:       a2a.AgentCapabilities{Tasks: true, Streaming: true},
		},
		Handler: func(_ context.Context, _ *a2a.MessageRequest) (*schema.Message, error) {
			return schema.AssistantMessage("done", nil), nil
		},
	})
	srv := &http.Server{Handler: h.HTTPHandler()}
	go func() { _ = srv.Serve(ln) }()
	defer shutdown(srv)

	baseURL := "http://" + ln.Addr().String()
	taskID := sendTask(t, ln.Addr().String())

	client := a2a.NewClient()
	sr, err := client.ResubscribeToTask(context.Background(), baseURL, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for resubscribe event")
		default:
			ev, recvErr := sr.Recv()
			if recvErr != nil {
				t.Fatal(recvErr)
			}
			if ev.Status != nil && ev.Status.TaskID == taskID && ev.Status.Final {
				return
			}
		}
	}
}

func TestClientListPushDeadLetters(t *testing.T) {
	failURL := "http://127.0.0.1:1"
	gate := make(chan struct{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := a2a.NewHandler(a2a.HandlerConfig{
		Card: a2a.AgentCard{
			Name:               "dlq",
			URL:                "/",
			PreferredTransport: a2a.TransportJSONRPC,
			Capabilities:       a2a.AgentCapabilities{Tasks: true, PushNotifications: true},
		},
		Handler: func(_ context.Context, _ *a2a.MessageRequest) (*schema.Message, error) {
			<-gate
			return schema.AssistantMessage("ok", nil), nil
		},
		PushRetry: a2a.PushRetryConfig{MaxRetries: 0, Backoff: time.Millisecond, DeadLetterMax: 5},
	})
	srv := &http.Server{Handler: h.HTTPHandler()}
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
		Config: a2a.PushNotificationConfig{URL: failURL},
	}); err != nil {
		t.Fatal(err)
	}
	close(gate)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := client.ListPushDeadLetters(context.Background(), baseURL)
		if err == nil && len(entries.Entries) > 0 {
			if entries.Entries[0].TaskID == task.ID {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected dead letter entries")
}
