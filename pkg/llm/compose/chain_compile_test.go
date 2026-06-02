package compose_test

import (
	"context"
	"io"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestCompileGraph_BranchAsConditionalEdges(t *testing.T) {
	fast := llm.NewFuncModel("fast", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("fast", nil), nil
	}, nil)
	slow := llm.NewFuncModel("slow", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("slow", nil), nil
	}, nil)

	g, err := compose.NewChainBuilder("branch-graph").
		AppendChainBranch(compose.NewChainBranch(func(_ context.Context, st *compose.State) (string, error) {
			if m, ok := st.Vars["mode"].(string); ok && m == "fast" {
				return "fast", nil
			}
			return "slow", nil
		}).AddChatModel("fast", fast).AddChatModel("slow", slow)).
		CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	st, trace, err := g.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")}, compose.WithStateModifier(func(_ context.Context, st *compose.GraphState) error {
		if st.Vars == nil {
			st.Vars = map[string]any{}
		}
		st.Vars["mode"] = "fast"
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if st.LastOutput.Content != "fast" {
		t.Fatalf("out=%q", st.LastOutput.Content)
	}
	if len(trace) != 2 {
		t.Fatalf("trace=%v want entry+branch target", trace)
	}
}

func TestMergeValues_Maps(t *testing.T) {
	out, err := compose.MergeValues([]any{
		map[string]any{"a": 1},
		map[string]any{"b": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["a"] != 1 || m["b"] != 2 {
		t.Fatalf("m=%v", m)
	}
	_, err = compose.MergeValues([]any{
		map[string]any{"a": 1},
		map[string]any{"a": 2},
	})
	if err == nil {
		t.Fatal("expected duplicate key error")
	}
}

func TestMergeMessageStreams(t *testing.T) {
	sr := compose.MergeMessageStreams(
		schema.StreamReaderFromSlice([]*schema.Message{schema.AssistantMessage("a", nil)}),
		schema.StreamReaderFromSlice([]*schema.Message{schema.AssistantMessage("b", nil)}),
	)
	defer sr.Close()
	var texts []string
	for {
		m, err := sr.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		texts = append(texts, m.Content)
	}
	if len(texts) != 2 {
		t.Fatalf("texts=%v", texts)
	}
}

func TestChainParallel_Builder(t *testing.T) {
	p := compose.NewChainParallel().
		AddLambda("a", func(_ context.Context, st *compose.State) error {
			st.Vars["a"] = 1
			return nil
		}).
		AddLambda("b", func(_ context.Context, st *compose.State) error {
			st.Vars["b"] = 2
			return nil
		})
	chain := compose.NewChainBuilder("par").AppendChainParallel(p).BuildChain()
	st, err := chain.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Vars["a"] == nil || st.Vars["b"] == nil {
		t.Fatalf("vars=%v", st.Vars)
	}
}

func TestStreamCheckpointResume(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{"sum":3}`, nil
	})
	store := compose.NewMemoryCheckPointStore()
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model:           &mockStreamModel{},
		Tools:           []tool.InvokableTool{add},
		CheckPointStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Stop first stream after model checkpoint (before tools run).
	sr1, err := g.StreamFrames(context.Background(), []*schema.Message{schema.UserMessage("hi")},
		compose.WithCheckPointID("resume-1"),
		compose.WithStreamCheckpoint(),
	)
	if err != nil {
		t.Fatal(err)
	}
	for {
		f, err := sr1.Recv()
		if err != nil {
			break
		}
		if f != nil && f.Phase == compose.PhaseTools && len(f.ToolCalls) > 0 {
			sr1.Close()
			break
		}
	}

	// resume should complete without re-running from scratch error
	sr2, err := g.StreamFrames(context.Background(), nil,
		compose.WithCheckPointID("resume-1"),
		compose.WithStreamCheckpoint(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer sr2.Close()
	var gotDone bool
	for {
		f, err := sr2.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if f != nil && f.Done {
			gotDone = true
			break
		}
	}
	if !gotDone {
		t.Fatal("expected done frame on resume")
	}
}
