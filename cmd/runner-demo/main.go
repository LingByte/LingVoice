// Command runner-demo: Runner Invoke / StreamFrames on ReAct graph (local mock).
//
//	go run ./cmd/runner-demo
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/agent"
	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type mockModel struct{ round int }

func (m *mockModel) Name() string { return "mock/runner" }

func (m *mockModel) Generate(_ context.Context, _ []*schema.Message, _ ...llm.Option) (*schema.Message, error) {
	m.round++
	if m.round == 1 {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "c1", Type: "function",
			Function: schema.FunctionCall{Name: "add", Arguments: `{"a":2,"b":3}`},
		}}), nil
	}
	return schema.AssistantMessage("runner done", nil), nil
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
	fmt.Fprintln(os.Stderr, "demo: runner-demo | Runner StreamFrames + checkpoint")

	add, _ := tool.InferTool("add", "add", func(_ context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (map[string]int, error) {
		return map[string]int{"sum": in.A + in.B}, nil
	})
	store := compose.NewMemoryCheckPointStore()
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model:           &mockModel{},
		Tools:           []tool.InvokableTool{add},
		CheckPointStore: store,
	})
	if err != nil {
		fail(err)
	}
	runner, err := agent.NewRunner(context.Background(), agent.RunnerConfig{Graph: g, CheckPointStore: store})
	if err != nil {
		fail(err)
	}

	sr, err := runner.StreamFrames(context.Background(), []*schema.Message{schema.UserMessage("compute")},
		"runner-cp-1",
		compose.WithStreamCheckpoint(),
	)
	if err != nil {
		fail(err)
	}
	defer sr.Close()
	for {
		f, err := sr.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			fail(err)
		}
		if f != nil {
			fmt.Printf("frame: stage=%s phase=%s round=%d done=%v\n", f.Stage, f.Phase, f.Round, f.Done)
			if f.Done {
				break
			}
		}
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
