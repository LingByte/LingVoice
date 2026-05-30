package compose_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestCompileGraph_ParallelFanOut(t *testing.T) {
	fast := llm.NewFuncModel("fast", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("fast", nil), nil
	}, nil)
	slow := llm.NewFuncModel("slow", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("slow", nil), nil
	}, nil)

	g, err := compose.NewChainBuilder("par-graph").
		AppendChainParallel(compose.NewChainParallel().
			AddChatModel("fast", fast).
			AddChatModel("slow", slow)).
		CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if g == nil {
		t.Fatal("nil graph")
	}
	st, trace, err := g.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if st.LastOutput == nil {
		t.Fatal("missing output")
	}
	if len(trace) < 3 {
		t.Fatalf("trace=%v want fan-out branches + join", trace)
	}
}

func TestPregel_FanOutJoin(t *testing.T) {
	g := compose.NewGraph("pregel-par")
	_ = g.AddLambdaNode("a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["a"] = 1
		st.LastOutput = schema.AssistantMessage("a", nil)
		return nil
	})
	_ = g.AddLambdaNode("b", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["b"] = 2
		return nil
	})
	_ = g.AddLambdaNode("join", func(_ context.Context, _ *compose.GraphState) error { return nil })
	_ = g.AddFanOutEdges(compose.START, "a", "b")
	_ = g.AddEdge("a", "join")
	_ = g.AddEdge("b", "join")
	_ = g.AddEdge("join", compose.END)
	cg, err := g.Compile(compose.WithGraphRunMode(compose.RunModePregel))
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Vars["a"] != 1 || st.Vars["b"] != 2 {
		t.Fatalf("vars=%v", st.Vars)
	}
}

func TestWorkflow_AddBranchAndMapping(t *testing.T) {
	wf := compose.NewWorkflow("wf-branch")
	_ = wf.AddInputNode("input")
	_ = wf.AddLambdaStep("route", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["mode"] = "b"
		return nil
	})
	branch := compose.BranchStep{
		Select: func(_ context.Context, st *compose.State) (string, error) {
			return st.Vars["mode"].(string), nil
		},
		Steps: map[string]compose.Step{
			"a": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
				st.LastOutput = schema.AssistantMessage("path-a", nil)
				return nil
			}},
			"b": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
				st.LastOutput = schema.AssistantMessage("path-b", nil)
				return nil
			}},
		},
	}
	if err := wf.AddBranch("route", branch); err != nil {
		t.Fatal(err)
	}
	_ = wf.AddLambdaStep("after", func(ctx context.Context, st *compose.GraphState) error {
		in, _ := st.Vars["input:after"].(map[string]any)
		if in == nil {
			return nil
		}
		st.Vars["merged"] = in["last_output"]
		return nil
	}, compose.FromNodeField("route_branch_b", "last_output").ToField("payload"))
	_ = wf.AddEdge(compose.START, "input")
	_ = wf.AddEdge("input", "route")
	_ = wf.AddEdge("route_branch_a", "after")
	_ = wf.AddEdge("route_branch_b", "after")
	_ = wf.AddEdge("after", compose.END)
	cg, err := wf.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), []*schema.Message{schema.UserMessage("x")})
	if err != nil {
		t.Fatal(err)
	}
	if st.LastOutput == nil || st.LastOutput.Content != "path-b" {
		t.Fatalf("out=%v", st.LastOutput)
	}
}

func TestConcatMessageStream(t *testing.T) {
	sr := schema.StreamReaderFromSlice([]*schema.Message{
		schema.AssistantMessage("hel", nil),
		schema.AssistantMessage("lo", nil),
	})
	out, err := compose.ConcatMessageStream(sr)
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "hello" {
		t.Fatalf("content=%q", out.Content)
	}
}

func TestRegisterStreamChunkConcatFunc(t *testing.T) {
	compose.RegisterStreamChunkConcatFunc(func(items []string) (string, error) {
		out := ""
		for _, s := range items {
			out += s
		}
		return out, nil
	})
	sr := schema.StreamReaderFromSlice([]string{"a", "b", "c"})
	out, err := compose.ConcatStreamReader(sr)
	if err != nil {
		t.Fatal(err)
	}
	if out != "abc" {
		t.Fatalf("out=%q", out)
	}
}
