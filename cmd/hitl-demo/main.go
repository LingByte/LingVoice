// Command hitl-demo: ReAct + approvable tool with checkpoint resume (local mock model).
//
//	go run ./cmd/hitl-demo
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/agent"
	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type mockHITLModel struct {
	step int
}

func (m *mockHITLModel) Name() string { return "mock/hitl" }

func (m *mockHITLModel) Generate(_ context.Context, _ []*schema.Message, _ ...llm.Option) (*schema.Message, error) {
	m.step++
	if m.step == 1 {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "call-1", Type: "function",
			Function: schema.FunctionCall{Name: "book", Arguments: `{"dest":"NYC"}`},
		}}), nil
	}
	return schema.AssistantMessage("booking confirmed", nil), nil
}

func (m *mockHITLModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
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

func (m *mockHITLModel) WithTools(_ []*schema.ToolInfo) (llm.ToolCallingChatModel, error) {
	return m, nil
}

func main() {
	fmt.Fprintln(os.Stderr, "demo: hitl-demo | local mock model + approvable tool")

	book, _ := tool.InferTool("book", "book a ticket", func(_ context.Context, in struct {
		Dest string `json:"dest"`
	}) (map[string]string, error) {
		return map[string]string{"status": "booked", "dest": in.Dest}, nil
	})

	store := compose.NewMemoryCheckPointStore()
	cpID := "hitl-cp-1"
	ctx := context.Background()

	ga, err := agent.NewGraphAgent(ctx, agent.ReActConfig{
		Model:           &mockHITLModel{},
		Tools:           []tool.InvokableTool{tool.WrapApprovable(book)},
		CheckPointStore: store,
	})
	if err != nil {
		fail(err)
	}

	fmt.Fprintln(os.Stderr, "--- phase 1: model calls book → HITL interrupt ---")
	result, err := ga.GenerateWithTrace(ctx, []*schema.Message{schema.UserMessage("book ticket to NYC")}, compose.WithCheckPointID(cpID))
	info, interrupted := compose.ExtractInterruptInfo(err)
	if !interrupted {
		fail(fmt.Errorf("expected tool HITL interrupt, err=%v", err))
	}
	b, _ := json.MarshalIndent(info, "", "  ")
	fmt.Fprintf(os.Stderr, "interrupt:\n%s\n", b)

	intrID := info.InterruptContexts[0].ID
	fmt.Fprintln(os.Stderr, "--- phase 2: human approves ---")
	result, err = ga.Resume(ctx, cpID, map[string]any{
		intrID: &tool.ApprovalResult{Approved: true},
	})
	if err != nil {
		fail(err)
	}
	fmt.Printf("final: %s\n", result.Message.PlainText())
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
