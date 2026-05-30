package compose_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestChainRunnable_FourModes(t *testing.T) {
	model := llm.NewFuncModel("m", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("ok", nil), nil
	}, nil)
	cr, err := compose.NewChainBuilder("r").
		AppendPrompt(schema.User, "hi").
		AppendChatModel(model).
		CompileGraphRunnable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out, err := cr.Invoke(context.Background(), nil)
	if err != nil || out.Content != "ok" {
		t.Fatalf("invoke err=%v out=%v", err, out)
	}
	sr, err := cr.Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sr.Close()
}

func TestCompiledGraph_Describe(t *testing.T) {
	g := compose.NewGraph("d")
	_ = g.AddLambdaNode("a", func(_ context.Context, _ *compose.GraphState) error { return nil })
	_ = g.AddFanOutEdges(compose.START, "a")
	_ = g.AddEdge("a", compose.END)
	cg, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	info := cg.Describe()
	if info.Name != "d" || len(info.Nodes) != 1 || info.FanOut[compose.START][0] != "a" {
		t.Fatalf("info=%+v", info)
	}
}

func TestChainBuilder_SubGraphAndPassthrough(t *testing.T) {
	inner := compose.NewGraph("inner")
	_ = inner.AddLambdaNode("n", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["inner"] = true
		st.LastOutput = schema.AssistantMessage("inner", nil)
		return nil
	})
	_ = inner.AddEdge(compose.START, "n")
	_ = inner.AddEdge("n", compose.END)

	g, err := compose.NewChainBuilder("outer").
		AppendPassthrough().
		AppendGraph("sub", inner).
		CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := g.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Vars["inner"] != true || st.LastOutput.Content != "inner" {
		t.Fatalf("st=%+v", st)
	}
}

func TestWorkflow_PregelJoinAny(t *testing.T) {
	wf := compose.NewWorkflow("wf-pregel")
	_ = wf.AddInputNode("input")
	_ = wf.AddLambdaStep("split", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["mode"] = "a"
		return nil
	})
	branch := compose.BranchStep{
		Select: func(_ context.Context, st *compose.State) (string, error) {
			return st.Vars["mode"].(string), nil
		},
		Steps: map[string]compose.Step{
			"a": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
				st.LastOutput = schema.AssistantMessage("a", nil)
				return nil
			}},
			"b": compose.FuncStep{Fn: func(_ context.Context, st *compose.State) error {
				st.LastOutput = schema.AssistantMessage("b", nil)
				return nil
			}},
		},
	}
	_ = wf.AddBranch("split", branch)
	_ = wf.AddLambdaStep("merge", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["merged"] = true
		return nil
	})
	wf.Node("merge").JoinAnyPredecessor()
	_ = wf.AddEdge(compose.START, "input")
	_ = wf.AddEdge("input", "split")
	_ = wf.AddEdge("split_branch_a", "merge")
	_ = wf.AddEdge("split_branch_b", "merge")
	_ = wf.AddEdge("merge", compose.END)
	cg, err := wf.Compile(compose.WithGraphRunMode(compose.RunModePregel))
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), []*schema.Message{schema.UserMessage("x")})
	if err != nil || st.Vars["merged"] != true {
		t.Fatalf("err=%v vars=%v", err, st.Vars)
	}
}

func TestCompiledGraph_Collect(t *testing.T) {
	model := llm.NewFuncModel("m", func(_ context.Context, msgs []*schema.Message, _ llm.Options) (*schema.Message, error) {
		if len(msgs) > 0 {
			return schema.AssistantMessage("got:"+msgs[len(msgs)-1].Content, nil), nil
		}
		return schema.AssistantMessage("empty", nil), nil
	}, nil)
	g, err := compose.NewChainBuilder("c").AppendChatModel(model).CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sr := schema.StreamReaderFromSlice([]*schema.Message{schema.UserMessage("stream")})
	out, err := g.Collect(context.Background(), sr)
	if err != nil || out.Content != "got:stream" {
		t.Fatalf("err=%v out=%v", err, out)
	}
}
