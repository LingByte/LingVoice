package tool_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/internal/core"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type stubTool struct{}

func (stubTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "book", Desc: "book ticket"}, nil
}

func (stubTool) InvokableRun(_ context.Context, _ string) (string, error) {
	return `{"ok":true}`, nil
}

func TestApprovableTool_InterruptAndResume(t *testing.T) {
	approved := tool.WrapApprovable(stubTool{})
	ctx := context.Background()

	_, err := approved.InvokableRun(ctx, `{"dest":"NYC"}`)
	if !core.IsInterrupt(err) {
		t.Fatalf("expected interrupt, got %v", err)
	}
	ie, _ := core.AsInterrupt(err)
	ctx = core.WithResumeData(ctx, map[string]any{
		ie.ID: &tool.ApprovalResult{Approved: true},
	})
	ctx = tool.WithToolResume(ctx, ie.ID, ie.Info, `{"dest":"NYC"}`)

	out, err := approved.InvokableRun(ctx, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("empty output")
	}
}

func TestInferEnhancedTool(t *testing.T) {
	et, err := tool.InferEnhancedTool("echo", "echo", func(_ context.Context, in struct {
		Text string `json:"text"`
	}) (*schema.ToolResult, error) {
		return &schema.ToolResult{Text: in.Text}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := et.EnhancedInvokableRun(context.Background(), `{"text":"hi"}`)
	if err != nil || r.Text != "hi" {
		t.Fatalf("r=%+v err=%v", r, err)
	}
	inv := tool.AsInvokable(et)
	s, err := inv.InvokableRun(context.Background(), `{"text":"yo"}`)
	if err != nil || s != "yo" {
		t.Fatalf("s=%q err=%v", s, err)
	}
}
