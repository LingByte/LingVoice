package compose_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/rag"
	"github.com/LingByte/LingVoice/pkg/llm/retriever"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestMessageRunnableFourModes(t *testing.T) {
	streamModel := llm.NewFuncModel("s", nil, func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.StreamReader[*schema.Message], error) {
		sr, sw := schema.Pipe[*schema.Message](2)
		go func() {
			defer sw.Close()
			sw.Send(schema.AssistantMessage("tok", nil), nil)
		}()
		return sr, nil
	})
	g, err := compose.NewChainBuilder("r").AppendChatModel(streamModel).CompileGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mr := compose.AsMessageRunnable(g)
	if mr == nil || !g.HasStreamCapability() {
		t.Fatal("expected stream capability")
	}
	sr, err := mr.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := schema.CollectMessages(sr)
	if err != nil || msg.Content != "tok" {
		t.Fatalf("stream err=%v msg=%q", err, msg.Content)
	}
}

func TestGraphRunChannelReducer(t *testing.T) {
	g := compose.NewGraph("reduce")
	_ = g.AddLambdaNode("a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["v"] = 1
		return nil
	})
	_ = g.AddLambdaNode("b", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["v"] = 2
		return nil
	})
	_ = g.AddLambdaNode("join", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["joined"] = true
		return nil
	})
	_ = g.AddFanOutEdges(compose.START, "a", "b")
	_ = g.AddEdge("a", "join")
	_ = g.AddEdge("b", "join")
	_ = g.AddEdge("join", compose.END)

	reducer := func(vals []any) (any, error) {
		sum := 0
		for _, v := range vals {
			if n, ok := v.(map[string]any)["v"]; ok {
				if i, ok := n.(int); ok {
					sum += i
				}
			}
		}
		return sum, nil
	}
	cg, err := g.Compile(
		compose.WithNodeTriggerMode(compose.AnyPredecessor, "join"),
		compose.WithPregelChannelReducer(reducer, "join"),
	)
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), nil)
	if err != nil || st.Vars["joined"] != true {
		t.Fatalf("err=%v vars=%v", err, st.Vars)
	}
}

func TestRAGNode(t *testing.T) {
	chain, _ := rag.New(rag.Config{
		Retriever: &retriever.InMemoryRetriever{Docs: []*schema.Document{
			{Content: "Runnable supports Stream Transform Collect."},
		}},
	})
	g := compose.NewGraph("rag")
	_ = g.AddRAGNode("rag", chain, "query")
	_ = g.AddLambdaNode("done", func(_ context.Context, st *compose.GraphState) error {
		st.LastOutput = schema.AssistantMessage("ok", nil)
		return nil
	})
	_ = g.AddEdge(compose.START, "rag")
	_ = g.AddEdge("rag", "done")
	_ = g.AddEdge("done", compose.END)
	cg, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), nil, compose.WithStateModifier(func(_ context.Context, st *compose.GraphState) error {
		if st.Vars == nil {
			st.Vars = map[string]any{}
		}
		st.Vars["query"] = "Runnable Stream"
		return nil
	}))
	if err != nil || st.Vars["rag_query"] != "Runnable Stream" {
		t.Fatalf("err=%v vars=%v", err, st.Vars)
	}
}
