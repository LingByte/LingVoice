// Command session-hitl-demo: SessionOrchestrator multi-turn + approvable tool HITL (local mock).
//
//	go run ./cmd/session-hitl-demo
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/agent"
	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/session"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type mockSessionHITLModel struct {
	step int
}

func (m *mockSessionHITLModel) Name() string { return "mock/session-hitl" }

func (m *mockSessionHITLModel) Generate(_ context.Context, _ []*schema.Message, _ ...llm.Option) (*schema.Message, error) {
	m.step++
	switch m.step {
	case 1:
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "call-book", Type: "function",
			Function: schema.FunctionCall{Name: "book", Arguments: `{"dest":"NYC"}`},
		}}), nil
	case 2:
		return schema.AssistantMessage("Your NYC booking is confirmed.", nil), nil
	default:
		return schema.AssistantMessage("Happy to help with anything else.", nil), nil
	}
}

func (m *mockSessionHITLModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		defer sw.Close()
		sw.Send(out, nil)
	}()
	return sr, nil
}

func (m *mockSessionHITLModel) WithTools(_ []*schema.ToolInfo) (llm.ToolCallingChatModel, error) {
	return m, nil
}

func main() {
	fmt.Fprintln(os.Stderr, "demo: session-hitl-demo | SessionOrchestrator + multi-turn + approvable tool")

	book, err := tool.InferTool("book", "book a ticket", func(_ context.Context, in struct {
		Dest string `json:"dest"`
	}) (map[string]string, error) {
		return map[string]string{"status": "booked", "dest": in.Dest}, nil
	})
	if err != nil {
		fail(err)
	}

	ctx := context.Background()
	cpStore := compose.NewMemoryCheckPointStore()
	sessStore := session.NewMemoryStore()
	sess := &session.Conversation{ID: "travel-1", Vars: map[string]any{}}

	react, err := agent.NewReActAgent(ctx, agent.ReActConfig{
		Model:           &mockSessionHITLModel{},
		Tools:           []tool.InvokableTool{tool.WrapApprovable(book)},
		CheckPointStore: cpStore,
	})
	if err != nil {
		fail(err)
	}

	orch, err := agent.NewSessionOrchestrator(agent.SessionOrchestratorConfig{
		Name:         "travel-orch",
		Agent:        react,
		Session:      sess,
		SessionStore: sessStore,
		CheckPointID: "cp-travel-1",
	})
	if err != nil {
		fail(err)
	}

	fmt.Fprintln(os.Stderr, "--- turn 1: book ticket → HITL interrupt ---")
	out, err := orch.RunTurn(ctx, "book a ticket to NYC")
	if err == nil {
		fail(fmt.Errorf("expected HITL interrupt on turn 1"))
	}
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok {
		fail(fmt.Errorf("expected interrupt info, err=%v", err))
	}
	b, _ := json.MarshalIndent(info, "", "  ")
	fmt.Fprintf(os.Stderr, "interrupt:\n%s\n", b)
	fmt.Fprintf(os.Stderr, "session pending: %v\n", orch.Session().HasPending())

	intrID := info.InterruptContexts[0].ID
	fmt.Fprintln(os.Stderr, "--- resume: human approves book tool ---")
	out, err = orch.ResumeTurn(ctx, map[string]any{
		intrID: &tool.ApprovalResult{Approved: true},
	})
	if err != nil {
		fail(err)
	}
	fmt.Printf("turn 1 final: %s\n", out.Message.PlainText())
	fmt.Fprintf(os.Stderr, "session messages after turn 1: %d\n", len(orch.Session().Messages))

	fmt.Fprintln(os.Stderr, "--- turn 2: follow-up (no tool) ---")
	out, err = orch.RunTurn(ctx, "thanks, anything else?")
	if err != nil {
		fail(err)
	}
	fmt.Printf("turn 2 final: %s\n", out.Message.PlainText())
	fmt.Fprintf(os.Stderr, "session messages after turn 2: %d\n", len(orch.Session().Messages))
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
