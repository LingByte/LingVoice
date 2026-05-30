// Command parallel-demo: Chain parallel steps (Eino compose.NewParallel subset).
//
//	go run ./cmd/parallel-demo
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
)

func main() {
	fmt.Fprintln(os.Stderr, "demo: parallel-demo | local, no API key")

	chain := compose.NewChain("parallel-demo",
		compose.ParallelStep{Parallel: compose.NewParallel(map[string]compose.Step{
			"task_a": compose.FuncStep{Name: "task_a", Fn: func(_ context.Context, st *compose.State) error {
				st.Vars["result"] = "built LLM protocol layer"
				return nil
			}},
			"task_b": compose.FuncStep{Name: "task_b", Fn: func(_ context.Context, st *compose.State) error {
				st.Vars["result"] = "built tool orchestration layer"
				return nil
			}},
		})},
	)

	st, err := chain.Invoke(context.Background(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	b, _ := json.MarshalIndent(st.Vars, "", "  ")
	fmt.Printf("parallel results:\n%s\n", b)
}
