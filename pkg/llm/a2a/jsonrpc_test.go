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

func TestJSONRPCMessageSend(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := a2a.NewHandler(a2a.HandlerConfig{
		Card: a2a.AgentCard{
			Name:               "rpc",
			URL:                "/",
			PreferredTransport: a2a.TransportJSONRPC,
			Capabilities:       a2a.AgentCapabilities{Tasks: true},
		},
		Handler: func(_ context.Context, req *a2a.MessageRequest) (*schema.Message, error) {
			return schema.AssistantMessage("pong", nil), nil
		},
	})
	srv := &http.Server{Handler: h.HTTPHandler()}
	go func() { _ = srv.Serve(ln) }()
	defer shutdown(srv)

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
	resp, err := http.Post("http://"+ln.Addr().String()+"/", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out a2a.JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error != nil {
		t.Fatalf("rpc error: %+v", out.Error)
	}
	b, _ := json.Marshal(out.Result)
	var task a2a.Task
	if err := json.Unmarshal(b, &task); err != nil || task.ID == "" {
		t.Fatalf("expected task result, got %s err=%v", string(b), err)
	}
	if task.Status.State != a2a.TaskCompleted {
		t.Fatalf("state=%q", task.Status.State)
	}
}

func mustRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
