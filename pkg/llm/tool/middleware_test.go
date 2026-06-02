package tool_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestMiddleware_Chain(t *testing.T) {
	base := tool.NewFuncTool(&schema.ToolInfo{Name: "echo", Desc: "echo"}, func(_ context.Context, args string) (string, error) {
		if strings.EqualFold(strings.Trim(args, `"`), "fail") {
			return "", context.Canceled
		}
		return args, nil
	})
	wrapped := tool.Chain(base,
		tool.WithInvokableWrapper(func(ctx context.Context, inner tool.InvokableTool, args string) (string, error) {
			return inner.InvokableRun(ctx, strings.ToUpper(args))
		}),
		tool.WithErrorHandlerMiddleware(tool.DefaultErrorHandler),
	)
	out, err := wrapped.InvokableRun(context.Background(), `"hi"`)
	if err != nil {
		t.Fatal(err)
	}
	if out != `"HI"` {
		t.Fatalf("out=%q", out)
	}
	out, err = wrapped.InvokableRun(context.Background(), `"fail"`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tool execution failed") {
		t.Fatalf("out=%q", out)
	}
}
