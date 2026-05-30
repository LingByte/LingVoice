package compose_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/agent"
	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestChainMultiBranch_CompileGraph(t *testing.T) {
	fast := llm.NewFuncModel("fast", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("fast", nil), nil
	}, nil)
	slow := llm.NewFuncModel("slow", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("slow", nil), nil
	}, nil)

	g, err := compose.NewChainBuilder("multi").
		AppendChainMultiBranch(compose.NewChainMultiBranch(func(_ context.Context, _ *compose.State) ([]string, error) {
			return []string{"fast", "slow"}, nil
		}).AddChatModel("fast", fast).AddChatModel("slow", slow)).
		CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st, trace, err := g.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if len(trace) < 3 {
		t.Fatalf("trace=%v", trace)
	}
	_ = st
}

func TestPregelInterruptResume(t *testing.T) {
	store := compose.NewMemoryCheckPointStore()
	g := compose.NewGraph("pregel-int")
	_ = g.AddLambdaNode("a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["a"] = 1
		return nil
	})
	_ = g.AddLambdaNode("b", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["b"] = 2
		return nil
	})
	_ = g.AddLambdaNode("join", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["done"] = true
		return nil
	})
	_ = g.AddFanOutEdges(compose.START, "a", "b")
	_ = g.AddEdge("a", "join")
	_ = g.AddEdge("b", "join")
	_ = g.AddEdge("join", compose.END)
	cg, err := g.Compile(
		compose.WithGraphRunMode(compose.RunModePregel),
		compose.WithCheckPointStore(store),
		compose.WithInterruptBeforeNodes("join"),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = cg.Invoke(context.Background(), nil, compose.WithCheckPointID("pg-int-1"))
	if err == nil {
		t.Fatal("expected interrupt")
	}
	st, _, err := cg.Invoke(context.Background(), nil, compose.WithCheckPointID("pg-int-1"))
	if err != nil {
		t.Fatal(err)
	}
	if done, _ := st.Vars["done"].(bool); !done {
		t.Fatalf("vars=%v", st.Vars)
	}
}

func TestCompileCallback(t *testing.T) {
	var called bool
	g := compose.NewGraph("cb")
	_ = g.AddLambdaNode("n", func(_ context.Context, _ *compose.GraphState) error { return nil })
	_ = g.AddEdge(compose.START, "n")
	_ = g.AddEdge("n", compose.END)
	_, err := g.Compile(compose.WithCompileCallback(func(_ context.Context, info compose.GraphInfo) error {
		called = true
		if info.Name != "cb" {
			t.Fatalf("name=%s", info.Name)
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("callback not invoked")
	}
}

func TestStreamChainBranch_Pipeline(t *testing.T) {
	fast := llm.NewFuncModel("fast", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("stream-fast", nil), nil
	}, nil)
	slow := llm.NewFuncModel("slow", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("stream-slow", nil), nil
	}, nil)
	branch := compose.NewStreamChainBranch(func(_ context.Context, st *compose.State) (string, error) {
		if m, _ := st.Vars["mode"].(string); m == "fast" {
			return "fast", nil
		}
		return "slow", nil
	}).AddChatModel("fast", fast).AddChatModel("slow", slow)
	bs := branch.Step()
	pipe, err := compose.NewPipeline(compose.PipelineConfig{
		Name:   "stream-branch",
		Branch: &bs,
	})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := pipe.StreamFrames(context.Background(), []*schema.Message{schema.UserMessage("hi")}, map[string]any{"mode": "fast"})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	var got bool
	for {
		f, err := sr.Recv()
		if err != nil {
			break
		}
		if f != nil && f.Done {
			got = true
		}
	}
	if !got {
		t.Fatal("expected done frame")
	}
}

func TestPregelCheckpointResume(t *testing.T) {
	store := compose.NewMemoryCheckPointStore()
	g := compose.NewGraph("cp-pregel")
	_ = g.AddLambdaNode("a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["a"] = 1
		return nil
	})
	_ = g.AddLambdaNode("b", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["b"] = 2
		return nil
	})
	_ = g.AddLambdaNode("join", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["done"] = true
		return nil
	})
	_ = g.AddFanOutEdges(compose.START, "a", "b")
	_ = g.AddEdge("a", "join")
	_ = g.AddEdge("b", "join")
	_ = g.AddEdge("join", compose.END)
	cg, err := g.Compile(compose.WithGraphRunMode(compose.RunModePregel), compose.WithCheckPointStore(store))
	if err != nil {
		t.Fatal(err)
	}
	// partial run with checkpoint after first superstep isn't easy to interrupt mid-flight;
	// save checkpoint manually then resume.
	_ = store.Set(context.Background(), "pg-1", mustMarshalCheckpoint(t, &compose.Checkpoint{
		State: &compose.GraphState{
			Vars: map[string]any{"a": 1},
		},
		Pregel: &compose.PregelCheckpoint{
			Completed: []string{"a"},
			Scheduled: []string{"b", "join"},
			Superstep: 1,
		},
	}))
	st, _, err := cg.Invoke(context.Background(), nil,
		compose.WithCheckPointID("pg-1"),
		compose.WithPregelCheckpoint(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if varAsInt(st.Vars["a"]) != 1 || varAsInt(st.Vars["b"]) != 2 {
		t.Fatalf("vars=%v", st.Vars)
	}
	if done, _ := st.Vars["done"].(bool); !done {
		t.Fatalf("done not set vars=%v", st.Vars)
	}
}

func mustMarshalCheckpoint(t *testing.T, cp *compose.Checkpoint) []byte {
	t.Helper()
	b, err := compose.MarshalCheckpointForTest(cp)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func varAsInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	default:
		return -1
	}
}

func TestRunner_StreamFrames(t *testing.T) {
	add, _ := tool.InferTool("add", "add", func(_ context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (map[string]int, error) {
		return map[string]int{"sum": in.A + in.B}, nil
	})
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model: &mockStreamModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := agent.NewRunner(context.Background(), agent.RunnerConfig{Graph: g})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := runner.StreamFrames(context.Background(), []*schema.Message{schema.UserMessage("hi")}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	n := 0
	for {
		f, err := sr.Recv()
		if err != nil {
			break
		}
		if f != nil {
			n++
		}
	}
	if n == 0 {
		t.Fatal("expected frames")
	}
}

func TestWorkflow_AddToolsNode(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{"sum":3}`, nil
	})
	wf := compose.NewWorkflow("wf-tools")
	_ = wf.AddInputNode("input")
	_ = wf.AddLambdaStep("model", func(_ context.Context, st *compose.GraphState) error {
		st.LastOutput = schema.AssistantMessage("", []schema.ToolCall{{
			ID: "c1", Type: "function",
			Function: schema.FunctionCall{Name: "add", Arguments: `{"a":1,"b":2}`},
		}})
		return nil
	})
	if err := wf.AddToolsNode(context.Background(), "tools", &compose.ToolNodeConfig{Tools: []tool.InvokableTool{add}}); err != nil {
		t.Fatal(err)
	}
	_ = wf.AddEdge(compose.START, "input")
	_ = wf.AddEdge("input", "model")
	_ = wf.AddEdge("model", "tools")
	_ = wf.AddEdge("tools", compose.END)
	cg, err := wf.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Messages) < 2 {
		t.Fatalf("msgs=%d", len(st.Messages))
	}
}
