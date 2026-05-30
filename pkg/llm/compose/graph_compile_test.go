package compose_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestCompiledGraph_ReAct(t *testing.T) {
	add, err := tool.InferTool("add", "add", func(_ context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (map[string]int, error) {
		return map[string]int{"sum": in.A + in.B}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model: &mockToolModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	st, trace, err := g.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	out := compose.FinalMessage(st)
	if out == nil || out.Content != "done" {
		t.Fatalf("out=%+v", out)
	}
	if len(trace) < 2 {
		t.Fatalf("trace=%v", trace)
	}
}

func TestParallel_Run(t *testing.T) {
	p := compose.NewParallel(map[string]compose.Step{
		"a": compose.FuncStep{Name: "a", Fn: func(_ context.Context, st *compose.State) error {
			st.Vars["x"] = 1
			return nil
		}},
		"b": compose.FuncStep{Name: "b", Fn: func(_ context.Context, st *compose.State) error {
			st.Vars["y"] = 2
			return nil
		}},
	})
	st := &compose.State{Vars: map[string]any{}}
	if err := p.Run(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if len(st.Vars) != 2 {
		t.Fatalf("vars=%v", st.Vars)
	}
}
