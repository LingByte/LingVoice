package compose_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/internal/core"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestCompositeInterrupt_Multiple(t *testing.T) {
	e1 := core.Interrupt(context.Background(), "a")
	e2 := core.Interrupt(context.Background(), "b")
	err := compose.CompositeInterrupt(context.Background(), nil, nil, e1, e2)
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok || len(info.InterruptContexts) != 2 {
		t.Fatalf("info=%+v ok=%v", info, ok)
	}
}

func TestBatch_CompositeInterrupt(t *testing.T) {
	b := &compose.Batch{
		Items: []compose.BatchItem{
			{ID: "a", Run: func(ctx context.Context) error {
				return compose.Interrupt(ctx, "need-a")
			}},
			{ID: "b", Run: func(ctx context.Context) error {
				return compose.Interrupt(ctx, "need-b")
			}},
		},
	}
	err := b.Run(context.Background())
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok || len(info.InterruptContexts) != 2 {
		t.Fatalf("info=%+v", info)
	}
}

func TestWorkflow_Compile(t *testing.T) {
	wf := compose.NewWorkflow("wf")
	_ = wf.AddInputNode("input")
	_ = wf.AddLambdaStep("work", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["out"] = st.Vars["input"]
		return nil
	})
	_ = wf.AddEdge(compose.START, "input")
	_ = wf.AddEdge("input", "work")
	_ = wf.AddEdge("work", compose.END)
	g, err := wf.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := g.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}
	if st.Vars["out"] != "hello" {
		t.Fatalf("vars=%v", st.Vars)
	}
}

func TestSubGraph_Nested(t *testing.T) {
	inner := compose.NewGraph("inner")
	_ = inner.AddLambdaNode("n", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["inner"] = true
		return nil
	})
	_ = inner.AddEdge(compose.START, "n")
	_ = inner.AddEdge("n", compose.END)

	outer := compose.NewGraph("outer")
	_ = outer.AddGraphNode("sub", inner)
	_ = outer.AddEdge(compose.START, "sub")
	_ = outer.AddEdge("sub", compose.END)

	g, err := outer.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := g.Invoke(context.Background(), nil)
	if err != nil || st.Vars["inner"] != true {
		t.Fatalf("err=%v vars=%v", err, st.Vars)
	}
}
