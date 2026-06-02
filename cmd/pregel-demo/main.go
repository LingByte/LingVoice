// Command pregel-demo: Pregel fan-out / fan-in graph (local mock).
//
//	go run ./cmd/pregel-demo
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func main() {
	fmt.Fprintln(os.Stderr, "demo: pregel-demo | fan-out START → a,b → join")

	g := compose.NewGraph("pregel-demo")
	_ = g.AddLambdaNode("task_a", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["task_a"] = "built chain"
		st.LastOutput = schema.AssistantMessage("from-a", nil)
		return nil
	})
	_ = g.AddLambdaNode("task_b", func(_ context.Context, st *compose.GraphState) error {
		st.Vars["task_b"] = "built graph"
		return nil
	})
	_ = g.AddLambdaNode("join", func(_ context.Context, st *compose.GraphState) error {
		st.LastOutput = schema.AssistantMessage(
			fmt.Sprintf("join:%v+%v", st.Vars["task_a"], st.Vars["task_b"]), nil)
		return nil
	})
	_ = g.AddFanOutEdges(compose.START, "task_a", "task_b")
	_ = g.AddEdge("task_a", "join")
	_ = g.AddEdge("task_b", "join")
	_ = g.AddEdge("join", compose.END)

	cg, err := g.Compile(compose.WithGraphRunMode(compose.RunModePregel))
	if err != nil {
		fail(err)
	}
	fmt.Printf("topology: run_mode=%s nodes=%d fan_out=%v\n",
		cg.Describe().RunMode, len(cg.Describe().Nodes), cg.Describe().FanOut)

	st, trace, err := cg.Invoke(context.Background(), nil)
	if err != nil {
		fail(err)
	}
	fmt.Printf("result: %s\n", st.LastOutput.PlainText())
	fmt.Printf("trace: %d steps\n", len(trace))
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
