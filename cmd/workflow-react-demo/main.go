// Command workflow-react-demo: Workflow input → ReAct node (local mock).
//
//	go run ./cmd/workflow-react-demo
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type mockModel struct{ round int }

func (m *mockModel) Name() string { return "mock/wf-react" }

func (m *mockModel) Generate(_ context.Context, _ []*schema.Message, _ ...llm.Option) (*schema.Message, error) {
	m.round++
	if m.round == 1 {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "c1", Type: "function",
			Function: schema.FunctionCall{Name: "add", Arguments: `{"a":4,"b":6}`},
		}}), nil
	}
	return schema.AssistantMessage("workflow react done", nil), nil
}

func (m *mockModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
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

func (m *mockModel) WithTools(_ []*schema.ToolInfo) (llm.ToolCallingChatModel, error) { return m, nil }

func main() {
	fmt.Fprintln(os.Stderr, "demo: workflow-react-demo | Workflow AddReActNode")

	add, _ := tool.InferTool("add", "add", func(_ context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (map[string]int, error) {
		return map[string]int{"sum": in.A + in.B}, nil
	})

	wf := compose.NewWorkflow("wf-react")
	_ = wf.AddInputNode("input")
	if err := wf.AddReActNode(context.Background(), "react", compose.ReActCompileConfig{
		Model: &mockModel{},
		Tools: []tool.InvokableTool{add},
	}); err != nil {
		fail(err)
	}
	_ = wf.AddEdge(compose.START, "input")
	_ = wf.AddEdge("input", "react")
	_ = wf.AddEdge("react", compose.END)

	cg, err := wf.Compile(compose.WithCompileCallback(func(_ context.Context, info compose.GraphInfo) error {
		fmt.Printf("topology: nodes=%d kinds=%v react=%v\n", len(info.Nodes), info.NodeKinds, info.HasReActRuntime)
		return nil
	}))
	if err != nil {
		fail(err)
	}

	st, trace, err := cg.Invoke(context.Background(), []*schema.Message{schema.UserMessage("compute 4+6")})
	if err != nil {
		fail(err)
	}
	fmt.Printf("result: %s\n", st.LastOutput.PlainText())
	fmt.Printf("trace: %d steps\n", len(trace))
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
