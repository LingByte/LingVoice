package compose_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestProcessStateAndSubgraph(t *testing.T) {
	type counter struct{ N int }

	parent := compose.NewGraphWithOptions("parent", compose.WithGenLocalState(func(_ context.Context) *counter {
		return &counter{}
	}))
	child := compose.NewGraphWithOptions("child", compose.WithGenLocalState(func(_ context.Context) *counter {
		return &counter{N: 10}
	}))

	_ = child.AddLambdaNode("inc", func(ctx context.Context, st *compose.GraphState) error {
		return compose.ProcessState(ctx, func(_ context.Context, c *counter) error {
			c.N++
			st.Vars["child_n"] = c.N
			return nil
		})
	})
	_ = child.AddEdge(compose.START, "inc")
	_ = child.AddEdge("inc", compose.END)

	_ = parent.AddGraphNode("sub", child)
	_ = parent.AddLambdaNode("read", func(ctx context.Context, st *compose.GraphState) error {
		return compose.ProcessState(ctx, func(_ context.Context, c *counter) error {
			st.Vars["parent_n"] = c.N
			return nil
		})
	})
	_ = parent.AddEdge(compose.START, "sub")
	_ = parent.AddEdge("sub", "read")
	_ = parent.AddEdge("read", compose.END)

	cg, err := parent.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Vars["child_n"] != 11 {
		t.Fatalf("child_n=%v", st.Vars["child_n"])
	}
	if st.Vars["parent_n"] != 0 {
		t.Fatalf("parent_n=%v", st.Vars["parent_n"])
	}
}

func TestGenericGraphTypedIO(t *testing.T) {
	g := compose.NewGenericGraph("typed", func(_ context.Context, in string) ([]*schema.Message, map[string]any, error) {
		return []*schema.Message{schema.UserMessage(in)}, map[string]any{"in": in}, nil
	}, func(st *compose.GraphState) (string, error) {
		if st.LastOutput != nil {
			return st.LastOutput.Content, nil
		}
		return "", nil
	})
	_ = g.Graph().AddLambdaNode("echo", func(_ context.Context, st *compose.GraphState) error {
		st.LastOutput = schema.AssistantMessage("ok:"+st.Messages[0].Content, nil)
		return nil
	})
	_ = g.Graph().AddEdge(compose.START, "echo")
	_ = g.Graph().AddEdge("echo", compose.END)

	cg, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	out, st, _, err := cg.Invoke(context.Background(), "hello")
	if err != nil || out != "ok:hello" || st.Vars["in"] != "hello" {
		t.Fatalf("out=%q err=%v vars=%v", out, err, st.Vars)
	}
}

func TestNodeTriggerModeAnyPredecessor(t *testing.T) {
	g := compose.NewGraph("join-any")
	_ = g.AddLambdaNode("a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["a"] = true
		return nil
	})
	_ = g.AddLambdaNode("b", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["b"] = true
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

	cg, err := g.Compile(compose.WithNodeTriggerMode(compose.AnyPredecessor, "join"))
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Vars["joined"] != true {
		t.Fatalf("vars=%v", st.Vars)
	}
}

func TestPregelMailboxAppend(t *testing.T) {
	g := compose.NewGraph("mb")
	_ = g.AddLambdaNode("a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["from"] = "a"
		return nil
	})
	_ = g.AddLambdaNode("b", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["from"] = "b"
		return nil
	})
	_ = g.AddLambdaNode("join", func(_ context.Context, st *compose.GraphState) error {
		mb, _ := st.Vars["mailbox:join"].(map[string]any)
		st.Vars["mailbox_len"] = len(mb)
		return nil
	})
	_ = g.AddFanOutEdges(compose.START, "a", "b")
	_ = g.AddEdge("a", "join")
	_ = g.AddEdge("b", "join")
	_ = g.AddEdge("join", compose.END)

	cg, err := g.Compile(
		compose.WithNodeTriggerMode(compose.AnyPredecessor, "join"),
		compose.WithPregelChannelMerge(compose.ChannelAppend, "join"),
	)
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Vars["mailbox_len"] != 2 {
		t.Fatalf("mailbox_len=%v vars=%v", st.Vars["mailbox_len"], st.Vars)
	}
}
