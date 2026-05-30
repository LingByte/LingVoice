package a2a_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/a2a"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestJSONRPCTasksListAndPushConfig(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := a2a.NewHandler(a2a.HandlerConfig{
		Card: a2a.AgentCard{
			Name:               "rpc",
			URL:                "/",
			PreferredTransport: a2a.TransportJSONRPC,
			Capabilities:       a2a.AgentCapabilities{Tasks: true, PushNotifications: true},
		},
		Handler: func(_ context.Context, req *a2a.MessageRequest) (*schema.Message, error) {
			return schema.AssistantMessage("ok", nil), nil
		},
	})
	srv := &http.Server{Handler: h.HTTPHandler()}
	go func() { _ = srv.Serve(ln) }()
	defer shutdown(srv)

	taskID := sendTask(t, ln.Addr().String())

	setBody, _ := json.Marshal(a2a.JSONRPCRequest{
		JSONRPC: a2a.JSONRPCVersion,
		ID:      json.RawMessage(`"push"`),
		Method:  a2a.MethodPushNotificationSet,
		Params: mustRaw(a2a.PushNotificationSetParams{
			TaskID: taskID,
			Config: a2a.PushNotificationConfig{URL: "https://example.com/hook", Token: "t"},
		}),
	})
	resp, err := http.Post("http://"+ln.Addr().String()+"/", "application/json", bytes.NewReader(setBody))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	listBody, _ := json.Marshal(a2a.JSONRPCRequest{
		JSONRPC: a2a.JSONRPCVersion,
		ID:      json.RawMessage(`"list"`),
		Method:  a2a.MethodTasksList,
		Params:  mustRaw(a2a.TaskListParams{Limit: 10}),
	})
	resp, err = http.Post("http://"+ln.Addr().String()+"/", "application/json", bytes.NewReader(listBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out a2a.JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error != nil {
		t.Fatalf("list error: %+v", out.Error)
	}
	b, _ := json.Marshal(out.Result)
	var result a2a.TaskListResult
	if err := json.Unmarshal(b, &result); err != nil || len(result.Tasks) == 0 {
		t.Fatalf("tasks=%+v err=%v", result, err)
	}
}

func sendTask(t *testing.T, addr string) string {
	t.Helper()
	body, _ := json.Marshal(a2a.JSONRPCRequest{
		JSONRPC: a2a.JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  a2a.MethodMessageSend,
		Params: mustRaw(a2a.MessageSendParams{
			Message: a2a.A2AMessage{
				Kind: "message", Role: "user", MessageID: "m1",
				Parts: []a2a.Part{{Kind: "text", Text: "ping"}},
			},
		}),
	})
	resp, err := http.Post("http://"+addr+"/", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out a2a.JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out.Result)
	var task a2a.Task
	if err := json.Unmarshal(b, &task); err != nil || task.ID == "" {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	return task.ID
}
