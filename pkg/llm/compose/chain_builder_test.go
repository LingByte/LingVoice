package compose_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestChainBuilder_CompileGraph(t *testing.T) {
	model := llm.NewFuncModel("cb/m", func(ctx context.Context, input []*schema.Message, opts llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("built", nil), nil
	}, nil)
	g, err := compose.NewChainBuilder("cb").
		AppendPrompt(schema.System, "be brief").
		AppendChatModel(model).
		CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := g.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil || st.LastOutput.Content != "built" {
		t.Fatalf("err=%v out=%+v", err, st.LastOutput)
	}
	runs := compose.GraphRunsFromState(st)
	if len(runs) != 1 {
		t.Fatalf("runs=%d", len(runs))
	}
}

func TestGraphNodes_ChatModelAndTemplate(t *testing.T) {
	model := llm.NewFuncModel("gn/m", func(ctx context.Context, input []*schema.Message, opts llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("node", nil), nil
	}, nil)
	g := compose.NewGraph("nodes")
	tpl := prompt.FromMessages(schema.FormatFString, schema.SystemMessage("mode=ok"))
	if err := g.AddTemplateNode("tpl", tpl); err != nil {
		t.Fatal(err)
	}
	if err := g.AddChatModelNode("model", model); err != nil {
		t.Fatal(err)
	}
	_ = g.AddEdge(compose.START, "tpl")
	_ = g.AddEdge("tpl", "model")
	_ = g.AddEdge("model", compose.END)
	cg, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), nil, compose.WithGraphChatModelOption(llm.WithMaxTokens(64)))
	if err != nil || st.LastOutput.Content != "node" {
		t.Fatalf("err=%v out=%+v", err, st.LastOutput)
	}
}

func TestGraphNodes_ToolsNode(t *testing.T) {
	add := tool.NewFuncTool(&schema.ToolInfo{Name: "add", Desc: "add"}, func(_ context.Context, _ string) (string, error) {
		return `{"sum":3}`, nil
	})
	model := llm.NewFuncModel("gn/tool", func(ctx context.Context, input []*schema.Message, opts llm.Options) (*schema.Message, error) {
		if len(input) == 1 {
			return schema.AssistantMessage("", []schema.ToolCall{{
				ID: "1", Type: "function",
				Function: schema.FunctionCall{Name: "add", Arguments: `{}`},
			}}), nil
		}
		return schema.AssistantMessage("done", nil), nil
	}, nil)
	g := compose.NewGraph("tool-node")
	_ = g.AddChatModelNode("model", model)
	_ = g.AddToolsNode(context.Background(), "tools", &compose.ToolNodeConfig{Tools: []tool.InvokableTool{add}})
	_ = g.AddEdge(compose.START, "model")
	_ = g.AddBranch("model", func(_ context.Context, st *compose.GraphState) (string, error) {
		if st.LastOutput != nil && len(st.LastOutput.ToolCalls) > 0 {
			return "tools", nil
		}
		return compose.END, nil
	}, map[string]bool{"tools": true, compose.END: true})
	_ = g.AddEdge("tools", "model")
	cg, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), []*schema.Message{schema.UserMessage("go")})
	if err != nil || st.LastOutput.Content != "done" {
		t.Fatalf("err=%v out=%+v msgs=%d", err, st.LastOutput, len(st.Messages))
	}
}

func TestCheckpoint_DeleteOnComplete(t *testing.T) {
	store := compose.NewMemoryCheckPointStore()
	g := compose.NewGraph("del")
	_ = g.AddLambdaNode("n", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["ok"] = true
		return nil
	})
	_ = g.AddEdge(compose.START, "n")
	_ = g.AddEdge("n", compose.END)
	cg, err := g.Compile(compose.WithCheckPointStore(store), compose.WithClearCheckpointOnCompleteCompile())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cg.Invoke(context.Background(), nil, compose.WithCheckPointID("cp-del")); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get(context.Background(), "cp-del"); ok {
		t.Fatal("expected checkpoint deleted")
	}
}

func TestCollectMessageStream(t *testing.T) {
	sr, sw := schema.Pipe[*schema.Message](2)
	go func() {
		defer sw.Close()
		sw.Send(schema.AssistantMessage("hel", nil), nil)
		sw.Send(schema.AssistantMessage("lo", nil), nil)
	}()
	msg, err := compose.CollectMessageStream(sr)
	if err != nil || msg.Content != "hello" {
		t.Fatalf("err=%v msg=%+v", err, msg)
	}
}

func TestMergeParallelStates(t *testing.T) {
	parent := &compose.State{Vars: map[string]any{}}
	child := &compose.State{
		Messages:   []*schema.Message{schema.UserMessage("a"), schema.AssistantMessage("b", nil)},
		LastOutput: schema.AssistantMessage("b", nil),
		Vars:       map[string]any{"k": 1},
	}
	compose.AppendRun(child, compose.RunEntry{Step: "m"})
	parent.Vars["branch"] = child
	compose.MergeParallelStates(parent, "branch")
	if parent.LastOutput == nil || parent.Vars["k"] != 1 {
		t.Fatalf("parent=%+v", parent)
	}
}
