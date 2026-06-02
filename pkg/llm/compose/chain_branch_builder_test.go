package compose_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestChainBranch_Builder(t *testing.T) {
	fast := llm.NewFuncModel("fast", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("fast path", nil), nil
	}, nil)
	slow := llm.NewFuncModel("slow", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("slow path", nil), nil
	}, nil)

	branch := compose.NewChainBranch(func(_ context.Context, st *compose.State) (string, error) {
		if m, ok := st.Vars["mode"].(string); ok && m == "fast" {
			return "fast", nil
		}
		return "unknown", nil
	}).AddChatModel("fast", fast).AddChatModel("slow", slow).Default("slow")

	g, err := compose.NewChainBuilder("branch").
		AppendChainBranch(branch).
		CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := g.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")}, compose.WithGraphChatModelOption(llm.WithMaxTokens(32)))
	if err != nil {
		t.Fatal(err)
	}
	// no mode in graph vars - uses default slow
	if st.LastOutput.Content != "slow path" {
		t.Fatalf("out=%q", st.LastOutput.Content)
	}
}

func TestGraphParallelFanIn(t *testing.T) {
	g := compose.NewGraph("fanin")
	_ = g.AddParallelFanInNode("parallel", map[string]compose.NodeFunc{
		"a": func(_ context.Context, st *compose.GraphState) error {
			st.Vars["a"] = 1
			st.LastOutput = schema.AssistantMessage("from-a", nil)
			return nil
		},
		"b": func(_ context.Context, st *compose.GraphState) error {
			st.Vars["b"] = 2
			return nil
		},
	})
	_ = g.AddEdge(compose.START, "parallel")
	_ = g.AddEdge("parallel", compose.END)
	cg, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	par, ok := st.Vars["parallel"].(map[string]*compose.GraphState)
	if !ok || len(par) != 2 {
		t.Fatalf("parallel=%T %v", st.Vars["parallel"], st.Vars["parallel"])
	}
	aSnap, ok := st.Vars["a"].(*compose.GraphState)
	if !ok || aSnap.Vars["a"] != 1 {
		t.Fatalf("a=%v", st.Vars["a"])
	}
	bSnap, ok := st.Vars["b"].(*compose.GraphState)
	if !ok || bSnap.Vars["b"] != 2 {
		t.Fatalf("b=%v", st.Vars["b"])
	}
	if st.LastOutput == nil || st.LastOutput.Content != "from-a" {
		t.Fatalf("last=%v", st.LastOutput)
	}
}

func TestStreamCheckpoint(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{"sum":2}`, nil
	})
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model: &mockStreamModel{},
		Tools: []tool.InvokableTool{add},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := compose.NewMemoryCheckPointStore()
	// attach store via recompile - simpler: use graph with checkpoint on react compile
	g2, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model:           &mockStreamModel{},
		Tools:           []tool.InvokableTool{add},
		CheckPointStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = g
	sr, err := g2.StreamFrames(context.Background(), []*schema.Message{schema.UserMessage("hi")},
		compose.WithCheckPointID("scp-1"),
		compose.WithStreamCheckpoint(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	for {
		f, err := sr.Recv()
		if err != nil {
			break
		}
		if f != nil && f.Done {
			break
		}
	}
	b, ok, err := store.Get(context.Background(), "scp-1")
	if err != nil || !ok || len(b) == 0 {
		t.Fatalf("checkpoint missing ok=%v err=%v", ok, err)
	}
}
