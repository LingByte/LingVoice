package agent_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/agent"
	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/llm/session"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type orchMockModel struct {
	n int
}

func (m *orchMockModel) Name() string { return "mock/orch" }

func (m *orchMockModel) Generate(_ context.Context, _ []*schema.Message, _ ...llm.Option) (*schema.Message, error) {
	m.n++
	if m.n == 1 {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "1", Type: "function",
			Function: schema.FunctionCall{Name: "add", Arguments: `{"a":1,"b":1}`},
		}}), nil
	}
	return schema.AssistantMessage("ok", nil), nil
}

func (m *orchMockModel) Stream(context.Context, []*schema.Message, ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, llm.ErrNotImplemented
}

func (m *orchMockModel) WithTools(_ []*schema.ToolInfo) (llm.ToolCallingChatModel, error) {
	return m, nil
}

func TestSessionOrchestrator_MultiTurn(t *testing.T) {
	add, err := tool.InferTool("add", "add", func(_ context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (map[string]int, error) {
		return map[string]int{"sum": in.A + in.B}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	react, err := agent.NewReActAgent(context.Background(), agent.ReActConfig{
		Model: &orchMockModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := session.NewMemoryStore()
	sess := &session.Conversation{ID: "s1", Vars: map[string]any{}}
	orch, err := agent.NewSessionOrchestrator(agent.SessionOrchestratorConfig{
		Agent:        react,
		Session:      sess,
		SessionStore: store,
		CheckPointID: "cp-s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orch.RunTurn(context.Background(), "calc 1+1"); err != nil {
		t.Fatal(err)
	}
	if _, err := orch.RunTurn(context.Background(), "thanks"); err != nil {
		t.Fatal(err)
	}
	if len(orch.Session().Messages) < 4 {
		t.Fatalf("msgs=%d", len(orch.Session().Messages))
	}
}

func TestSessionOrchestrator_WithTemplate(t *testing.T) {
	add, err := tool.InferTool("add", "add", func(_ context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (map[string]int, error) {
		return map[string]int{"sum": in.A + in.B}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	react, err := agent.NewReActAgent(context.Background(), agent.ReActConfig{
		Model: &orchMockModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	tpl := prompt.FromMessages(schema.FormatFString, schema.SystemMessage("mode={mode}"))
	orch, err := agent.NewSessionOrchestrator(agent.SessionOrchestratorConfig{
		Agent:    react,
		Template: tpl,
		Session:  &session.Conversation{ID: "s2", Vars: map[string]any{"mode": "calc"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if orch.CheckPointID() == "" {
		t.Fatal("expected default checkpoint id")
	}
	sr, err := orch.StreamTurn(context.Background(), "1+1")
	if err != nil {
		t.Fatal(err)
	}
	sr.Close()
}
