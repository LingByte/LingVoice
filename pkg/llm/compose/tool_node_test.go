package compose_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestToolNode_Invoke(t *testing.T) {
	add, err := tool.InferTool("add", "add two integers", func(_ context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (map[string]int, error) {
		return map[string]int{"sum": in.A + in.B}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	node, err := compose.NewToolNode(context.Background(), &compose.ToolNodeConfig{
		Tools:        []tool.InvokableTool{add},
		ErrorHandler: tool.DefaultErrorHandler,
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant := schema.AssistantMessage("", []schema.ToolCall{{
		ID: "call-1", Type: "function",
		Function: schema.FunctionCall{Name: "add", Arguments: `{"a":2,"b":3}`},
	}})
	msgs, err := node.Invoke(context.Background(), assistant)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Role != schema.Tool {
		t.Fatalf("msgs=%+v", msgs)
	}
	if msgs[0].Content != `{"sum":5}` {
		t.Fatalf("content=%q", msgs[0].Content)
	}
}

func TestToolNode_ErrorHandlerSoftFail(t *testing.T) {
	boom := tool.NewFuncTool(&schema.ToolInfo{Name: "boom", Desc: "boom"}, func(_ context.Context, _ string) (string, error) {
		return "", errors.New("kaboom")
	})
	node, err := compose.NewToolNode(context.Background(), &compose.ToolNodeConfig{
		Tools:        []tool.InvokableTool{boom},
		ErrorHandler: tool.DefaultErrorHandler,
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant := schema.AssistantMessage("", []schema.ToolCall{{
		ID: "c1", Type: "function",
		Function: schema.FunctionCall{Name: "boom", Arguments: `{}`},
	}})
	msgs, err := node.Invoke(context.Background(), assistant)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content == "" {
		t.Fatalf("msgs=%+v", msgs)
	}
}

type mockToolModel struct {
	round int
	tools []*schema.ToolInfo
}

func (m *mockToolModel) Name() string { return "mock/tool" }

func (m *mockToolModel) Generate(_ context.Context, input []*schema.Message, _ ...llm.Option) (*schema.Message, error) {
	m.round++
	if m.round == 1 {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "c1", Type: "function",
			Function: schema.FunctionCall{Name: "add", Arguments: `{"a":1,"b":1}`},
		}}), nil
	}
	return schema.AssistantMessage("done", nil), nil
}

func (m *mockToolModel) Stream(context.Context, []*schema.Message, ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, llm.ErrNotImplemented
}

func (m *mockToolModel) WithTools(tools []*schema.ToolInfo) (llm.ToolCallingChatModel, error) {
	c := *m
	c.tools = tools
	return &c, nil
}

func TestToolLoop_Run(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{"sum":2}`, nil
	})
	loop, err := compose.NewToolLoop(context.Background(), compose.ToolLoopConfig{
		Model: &mockToolModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, delta, err := loop.Run(context.Background(), []*schema.Message{schema.UserMessage("compute")})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || out.Content != "done" {
		t.Fatalf("out=%+v", out)
	}
	if len(delta) < 3 {
		t.Fatalf("delta len=%d", len(delta))
	}
}
