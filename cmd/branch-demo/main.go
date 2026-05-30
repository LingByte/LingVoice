// Command branch-demo: ChainBranch → CompileGraph with conditional graph edges (local mock).
//
//	go run ./cmd/branch-demo
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

type branchModel struct {
	name string
}

func (m *branchModel) Name() string { return m.name }

func (m *branchModel) Generate(_ context.Context, _ []*schema.Message, _ ...llm.Option) (*schema.Message, error) {
	return schema.AssistantMessage("path:" + m.name, nil), nil
}

func (m *branchModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
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

func (m *branchModel) WithTools(_ []*schema.ToolInfo) (llm.ToolCallingChatModel, error) {
	return m, nil
}

func main() {
	fmt.Fprintln(os.Stderr, "demo: branch-demo | ChainBranch → CompileGraph (conditional edges)")

	add, _ := tool.InferTool("add", "add", func(_ context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (map[string]int, error) {
		return map[string]int{"sum": in.A + in.B}, nil
	})
	loop, err := compose.NewToolLoop(context.Background(), compose.ToolLoopConfig{
		Model: &branchModel{name: "tool-path"},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		fail(err)
	}

	branch := compose.NewChainBranch(func(_ context.Context, st *compose.State) (string, error) {
		if st.Vars["use_tool"].(bool) {
			return "tool", nil
		}
		return "chat", nil
	}).
		AddToolLoop("tool", loop).
		AddChatModel("chat", &branchModel{name: "chat-path"})

	g, err := compose.NewChainBuilder("branch-demo").
		AppendChainBranch(branch).
		CompileGraph(context.Background())
	if err != nil {
		fail(err)
	}

	for _, useTool := range []bool{true, false} {
		use := useTool
		st, _, err := g.Invoke(context.Background(), []*schema.Message{schema.UserMessage("compute")},
			compose.WithStateModifier(func(_ context.Context, gs *compose.GraphState) error {
				if gs.Vars == nil {
					gs.Vars = map[string]any{}
				}
				gs.Vars["use_tool"] = use
				return nil
			}),
		)
		if err != nil {
			fail(err)
		}
		fmt.Printf("use_tool=%v → %s\n", useTool, st.LastOutput.PlainText())
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
