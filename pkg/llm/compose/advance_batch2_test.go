package compose_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestGenericChain_IO(t *testing.T) {
	chain := compose.NewGenericChainIO[string]("upper").
		Append(func(_ context.Context, in string) (string, error) {
			return strings.TrimSpace(in), nil
		}).
		Append(func(_ context.Context, in string) (string, error) {
			return strings.ToUpper(in), nil
		})
	out, err := chain.Invoke(context.Background(), "  hello  ")
	if err != nil {
		t.Fatal(err)
	}
	if out != "HELLO" {
		t.Fatalf("out=%q", out)
	}
}

func TestStringMessageChain(t *testing.T) {
	model := llm.NewFuncModel("mock", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("typed-ok", nil), nil
	}, nil)
	chain := compose.NewStringMessageChain("typed", &compose.ChatModelStep{Model: model})
	out, err := chain.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if out != "typed-ok" {
		t.Fatalf("out=%q", out)
	}
}

func TestGraphDescribe_NodeKinds(t *testing.T) {
	g, err := compose.CompileReActGraph(context.Background(), compose.ReActCompileConfig{
		Model: &mockStreamModel{},
		Tools: []tool.InvokableTool{},
	})
	if err != nil {
		t.Fatal(err)
	}
	info := g.Describe()
	if !info.HasReActRuntime {
		t.Fatal("expected react runtime")
	}
	if info.NodeKinds[compose.NodeChatModel] != compose.NodeKindChatModel {
		t.Fatalf("kinds=%v", info.NodeKinds)
	}
	if len(info.ReActNodes) != 2 {
		t.Fatalf("react nodes=%v", info.ReActNodes)
	}
}

func TestStreamChainMultiBranch_Pipeline(t *testing.T) {
	fast := llm.NewFuncModel("fast", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("fast-stream", nil), nil
	}, nil)
	slow := llm.NewFuncModel("slow", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("slow-stream", nil), nil
	}, nil)
	ms := compose.NewStreamChainMultiBranch(func(_ context.Context, _ *compose.State) ([]string, error) {
		return []string{"fast", "slow"}, nil
	}).AddChatModel("fast", fast).AddChatModel("slow", slow).Step()
	pipe, err := compose.NewPipeline(compose.PipelineConfig{
		Name:  "multi-stream",
		Steps: []compose.Step{ms},
	})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := pipe.StreamFrames(context.Background(), []*schema.Message{schema.UserMessage("hi")}, nil)
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

func TestWorkflow_AddReActNode(t *testing.T) {
	add, _ := tool.InferTool("add", "add", func(_ context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (map[string]int, error) {
		return map[string]int{"sum": in.A + in.B}, nil
	})
	wf := compose.NewWorkflow("wf-react")
	_ = wf.AddInputNode("input")
	if err := wf.AddReActNode(context.Background(), "react", compose.ReActCompileConfig{
		Model: &mockStreamModel{},
		Tools: []tool.InvokableTool{add},
	}); err != nil {
		t.Fatal(err)
	}
	_ = wf.AddEdge(compose.START, "input")
	_ = wf.AddEdge("input", "react")
	_ = wf.AddEdge("react", compose.END)
	cg, err := wf.Compile()
	if err != nil {
		t.Fatal(err)
	}
	info := cg.Describe()
	if info.NodeKinds["react"] != compose.NodeKindReactEmbed {
		t.Fatalf("kinds=%v", info.NodeKinds)
	}
	st, _, err := cg.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if st.LastOutput == nil {
		t.Fatal("nil output")
	}
}
