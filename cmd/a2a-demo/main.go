// Command a2a-demo: Agent-to-Agent HTTP with auth (local, no API key).
//
//	go run ./cmd/a2a-demo
//	go run ./cmd/a2a-demo -extended   # tasks/list, resubscribe, push + dead-letter redrive
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/a2a"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func main() {
	extended := flag.Bool("extended", false, "also demo tasks/list, resubscribe, push dead-letter redrive")
	flag.Parse()

	fmt.Fprintln(os.Stderr, "demo: a2a-demo | A2A server + bearer auth + streaming")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	baseURL := "http://" + ln.Addr().String()
	token := "demo-secret-token"

	var pushHook *httptest.Server
	pushHookURL := "http://127.0.0.1:1"
	if *extended {
		pushHook = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		}))
		defer pushHook.Close()
		pushHookURL = pushHook.URL
	}

	h := a2a.NewHandler(a2a.HandlerConfig{
		Card: a2a.AgentCard{
			Name:               "echo-agent",
			Description:        "echoes user text with streaming",
			URL:                "/",
			Version:            "1",
			PreferredTransport: a2a.TransportJSONRPC,
			Capabilities: a2a.AgentCapabilities{
				Streaming: true, Tasks: true, PushNotifications: *extended,
			},
		},
		Auth: a2a.ServerAuth{BearerTokens: []string{token}},
		PushHTTPClient: func() *http.Client {
			if pushHook != nil {
				return pushHook.Client()
			}
			return nil
		}(),
		PushRetry: a2a.PushRetryConfig{MaxRetries: 1, Backoff: 20 * time.Millisecond, DeadLetterMax: 10},
		Handler: func(_ context.Context, req *a2a.MessageRequest) (*schema.Message, error) {
			text := lastUser(req.Messages)
			return schema.AssistantMessage("A2A echo: "+text, nil), nil
		},
		StreamHandler: func(_ context.Context, req *a2a.MessageRequest) (*schema.StreamReader[*schema.Message], error) {
			text := lastUser(req.Messages)
			sr, sw := schema.Pipe[*schema.Message](2)
			go func() {
				defer sw.Close()
				sw.Send(schema.AssistantMessage("stream:"+text, nil), nil)
			}()
			return sr, nil
		},
	})
	srv := &http.Server{Handler: h.HTTPHandler()}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	client := a2a.NewClient(a2a.ClientConfig{Auth: a2a.AuthConfig{BearerToken: token}})
	card, err := client.FetchCard(context.Background(), baseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "agent:", card.Name, "streaming=", card.Capabilities.Streaming)

	ctx := context.Background()
	msg, err := client.SendMessages(ctx, baseURL, []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("sync:", msg.PlainText())

	sr, err := client.StreamMessages(ctx, baseURL, []*schema.Message{schema.UserMessage("stream me")})
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream error: %v\n", err)
		os.Exit(1)
	}
	defer sr.Close()
	chunk, err := sr.Recv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "recv error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("stream:", chunk.PlainText())

	if !*extended {
		return
	}

	result, err := client.SendMessage(ctx, baseURL, a2a.MessageSendParams{
		Message: a2a.A2AMessage{
			Kind: "message", Role: "user", MessageID: "ext1",
			Parts: []a2a.Part{{Kind: "text", Text: "extended"}},
		},
	})
	if err != nil {
		fail(err)
	}
	taskID := taskIDFromResult(result)
	if taskID == "" {
		fail(fmt.Errorf("expected task id"))
	}

	tasks, err := client.ListTasks(ctx, baseURL, a2a.TaskListParams{Limit: 5})
	if err != nil {
		fail(err)
	}
	fmt.Println("tasks:", len(tasks.Tasks))

	sub, err := client.SubscribeToTask(ctx, baseURL, taskID)
	if err != nil {
		fail(err)
	}
	defer sub.Close()
	ev, err := sub.Recv()
	if err != nil {
		fail(err)
	}
	if ev.Status == nil || !ev.Status.Final {
		fail(fmt.Errorf("expected final status event"))
	}
	fmt.Println("resubscribe:", ev.Status.Status.State)

	if _, err := client.SetPushNotificationConfig(ctx, baseURL, a2a.PushNotificationSetParams{
		TaskID: taskID,
		Config: a2a.PushNotificationConfig{URL: pushHookURL},
	}); err != nil {
		fail(err)
	}
	// Trigger another completion via sync send on same task is blocked; push already configured for future tasks.
	result2, err := client.SendMessage(ctx, baseURL, a2a.MessageSendParams{
		Message: a2a.A2AMessage{
			Kind: "message", Role: "user", MessageID: "ext2",
			Parts: []a2a.Part{{Kind: "text", Text: "push me"}},
		},
	})
	if err != nil {
		fail(err)
	}
	_ = taskIDFromResult(result2)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		dlq, err := client.ListPushDeadLetters(ctx, baseURL)
		if err == nil && len(dlq.Entries) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	fmt.Println("push hook configured for task", taskID)
}

func taskIDFromResult(result any) string {
	if m, ok := result.(map[string]any); ok {
		if id, ok := m["id"].(string); ok {
			return id
		}
	}
	b, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	var task a2a.Task
	if json.Unmarshal(b, &task) == nil {
		return task.ID
	}
	return ""
}

func lastUser(msgs []*schema.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] != nil && msgs[i].Role == schema.User {
			return msgs[i].PlainText()
		}
	}
	return ""
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
