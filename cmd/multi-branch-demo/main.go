// Command multi-branch-demo: ChainMultiBranch fan-out (local mock).
//
//	go run ./cmd/multi-branch-demo
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func main() {
	fmt.Fprintln(os.Stderr, "demo: multi-branch-demo | ChainMultiBranch → CompileGraph")

	fast := llm.NewFuncModel("fast", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("fast lane", nil), nil
	}, nil)
	slow := llm.NewFuncModel("slow", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("slow lane", nil), nil
	}, nil)

	g, err := compose.NewChainBuilder("multi").
		AppendChainMultiBranch(compose.NewChainMultiBranch(func(_ context.Context, _ *compose.State) ([]string, error) {
			return []string{"fast", "slow"}, nil
		}).AddChatModel("fast", fast).AddChatModel("slow", slow)).
		CompileGraph(context.Background(), compose.WithCompileCallback(func(_ context.Context, info compose.GraphInfo) error {
			fmt.Printf("compiled: name=%s mode=%s nodes=%d fan_out=%v\n",
				info.Name, info.RunMode, len(info.Nodes), info.FanOut)
			return nil
		}))
	if err != nil {
		fail(err)
	}

	st, trace, err := g.Invoke(context.Background(), []*schema.Message{schema.UserMessage("run both")})
	if err != nil {
		fail(err)
	}
	fmt.Printf("trace: %d steps\n", len(trace))
	if st.LastOutput != nil {
		fmt.Printf("last output: %s\n", st.LastOutput.PlainText())
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
