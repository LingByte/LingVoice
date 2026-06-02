// Command pipeline-demo: Template → Branch → ReAct pipeline (local mock).
//
//	go run ./cmd/pipeline-demo
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/LingByte/LingVoice/pkg/llm/agent"
	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type mockPipeModel struct {
	step int
}

func (m *mockPipeModel) Name() string { return "mock/pipeline" }

func (m *mockPipeModel) Generate(_ context.Context, _ []*schema.Message, _ ...llm.Option) (*schema.Message, error) {
	m.step++
	if m.step == 1 {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "c1", Type: "function",
			Function: schema.FunctionCall{Name: "add", Arguments: `{"a":3,"b":4}`},
		}}), nil
	}
	return schema.AssistantMessage("pipeline result: 7", nil), nil
}

func (m *mockPipeModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
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

func (m *mockPipeModel) WithTools(_ []*schema.ToolInfo) (llm.ToolCallingChatModel, error) {
	return m, nil
}

func main() {
	stream := flag.Bool("stream", false, "emit frame-level pipeline trace")
	flag.Parse()

	fmt.Fprintln(os.Stderr, "demo: pipeline-demo | Template → Branch → ReAct (local)")

	add, err := tool.InferTool("add", "add", func(_ context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (map[string]int, error) {
		return map[string]int{"sum": in.A + in.B}, nil
	})
	if err != nil {
		fail(err)
	}

	react, err := agent.NewReActAgent(context.Background(), agent.ReActConfig{
		Model: &mockPipeModel{},
		Tools: []tool.InvokableTool{add},
		MessageModifier: func(_ context.Context, msgs []*schema.Message) []*schema.Message {
			return append([]*schema.Message{schema.SystemMessage("Use tools for math.")}, msgs...)
		},
	})
	if err != nil {
		fail(err)
	}

	tpl := prompt.FromMessages(schema.FormatFString,
		schema.SystemMessage("Pipeline mode: {mode}"),
	)

	pipe, err := compose.NewPipeline(compose.PipelineConfig{
		Name:     "calc-pipeline",
		Template: tpl,
		Branch: &compose.BranchStep{
			Select: func(_ context.Context, st *compose.State) (string, error) {
				if strings.Contains(strings.ToLower(st.Vars["task"].(string)), "calc") {
					return "react", nil
				}
				return "noop", nil
			},
			Steps: map[string]compose.Step{
				"react": agent.ReActStep{Agent: react},
				"noop": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
					st.LastOutput = schema.AssistantMessage("no calc task", nil)
					return nil
				}},
			},
		},
	})
	if err != nil {
		fail(err)
	}

	vars := map[string]any{
		"mode": "calculator",
		"task": "calc",
	}
	input := []*schema.Message{schema.UserMessage("please calc 3+4")}

	if *stream {
		sr, err := pipe.StreamFrames(context.Background(), input, vars)
		if err != nil {
			fail(err)
		}
		defer sr.Close()
		fmt.Fprintln(os.Stderr, "--- pipeline frame trace ---")
		for {
			f, err := sr.Recv()
			if err != nil {
				if err == io.EOF {
					break
				}
				fail(err)
			}
			if f == nil {
				continue
			}
			chunk := ""
			if f.Chunk != nil {
				chunk = f.Chunk.PlainText()
			}
			fmt.Fprintf(os.Stderr, "frame stage=%q node=%q round=%d phase=%s done=%v chunk=%q\n",
				f.Stage, f.Node, f.Round, f.Phase, f.Done, truncate(chunk, 40))
			if f.Done && f.Stage == "calc-pipeline" {
				break
			}
		}
		return
	}

	st, err := pipe.Run(context.Background(), input, vars)
	if err != nil {
		fail(err)
	}
	fmt.Printf("output: %s\n", st.LastOutput.PlainText())
	fmt.Fprintf(os.Stderr, "messages: %d vars: %v\n", len(st.Messages), st.Vars)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
