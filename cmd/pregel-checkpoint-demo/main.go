// Command pregel-checkpoint-demo: Pregel interrupt + checkpoint resume (local).
//
//	go run ./cmd/pregel-checkpoint-demo
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
)

func main() {
	fmt.Fprintln(os.Stderr, "demo: pregel-checkpoint-demo | interrupt before join → resume")

	store := compose.NewMemoryCheckPointStore()
	g := compose.NewGraph("pregel-cp")
	_ = g.AddLambdaNode("a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["a"] = "alpha"
		return nil
	})
	_ = g.AddLambdaNode("b", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["b"] = "beta"
		return nil
	})
	_ = g.AddLambdaNode("join", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["done"] = fmt.Sprintf("%v+%v", st.Vars["a"], st.Vars["b"])
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
		fail(err)
	}

	_, _, err = cg.Invoke(context.Background(), nil, compose.WithCheckPointID("pg-cp-1"))
	if !compose.IsInterrupt(err) {
		fail(fmt.Errorf("expected interrupt, got %v", err))
	}
	fmt.Println("phase 1: interrupted before join (checkpoint saved)")

	st, trace, err := cg.Invoke(context.Background(), nil, compose.WithCheckPointID("pg-cp-1"))
	if err != nil {
		fail(err)
	}
	fmt.Printf("phase 2: resumed trace=%d done=%v\n", len(trace), st.Vars["done"])
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
