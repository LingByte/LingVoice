package tool_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type addInput struct {
	A int `json:"a"`
	B int `json:"b"`
}

func TestInferTool(t *testing.T) {
	add, err := tool.InferTool("add", "add numbers", func(_ context.Context, in addInput) (map[string]int, error) {
		return map[string]int{"sum": in.A + in.B}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := add.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "add" || info.Params == nil {
		t.Fatalf("info=%+v", info)
	}
	out, err := add.InvokableRun(context.Background(), `{"a":2,"b":3}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"sum":5}` {
		t.Fatalf("out=%q", out)
	}
}

func TestWrapInvokableToolWithErrorHandler(t *testing.T) {
	raw := tool.NewFuncTool(&schema.ToolInfo{Name: "fail", Desc: "fail"}, func(_ context.Context, _ string) (string, error) {
		return "", context.Canceled
	})
	wrapped := tool.WrapInvokableToolWithErrorHandler(raw, tool.DefaultErrorHandler)
	out, err := wrapped.InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("expected error string result")
	}
}
