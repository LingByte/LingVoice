package compose_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/session"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestOrchestrator_MultiTurn(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{"sum":2}`, nil
	})
	sess := &session.Conversation{ID: "s1", Vars: map[string]any{}}
	loop, err := compose.NewToolLoop(context.Background(), compose.ToolLoopConfig{
		Model: &mockToolModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	orch, err := compose.NewOrchestrator(compose.OrchestratorConfig{
		Name:    "test",
		Loop:    loop,
		Session: sess,
	})
	if err != nil {
		t.Fatal(err)
	}
	r1, err := orch.Run(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if r1.Message == nil {
		t.Fatal("nil message")
	}
	if len(orch.Messages()) != 2 {
		t.Fatalf("messages=%d", len(orch.Messages()))
	}
}

func TestLoopTraceFromGraph(t *testing.T) {
	st := &compose.GraphState{
		Messages: []*schema.Message{
			schema.UserMessage("q"),
			{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "1", Function: schema.FunctionCall{Name: "add"}}}},
			schema.ToolMessage(`{"sum":3}`, "1"),
			schema.AssistantMessage("done", nil),
		},
	}
	steps := []compose.GraphStep{
		{Node: compose.NodeChatModel},
		{Node: compose.NodeTools},
		{Node: compose.NodeChatModel},
	}
	trace := compose.LoopTraceFromGraph(steps, st, 1)
	if !trace.UsedTools || len(trace.Steps) != 3 {
		t.Fatalf("trace=%+v", trace)
	}
}
