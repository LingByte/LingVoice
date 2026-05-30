// Command chain-builder-demo: Eino-style ChainBuilder → CompileGraph (local mock).
//
//	go run ./cmd/chain-builder-demo
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func main() {
	fmt.Fprintln(os.Stderr, "demo: chain-builder-demo | ChainBuilder → CompileGraph")

	model := llm.NewFuncModel("mock/chain-builder", func(_ context.Context, msgs []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage(fmt.Sprintf("reply to %d msgs", len(msgs)), nil), nil
	}, nil)

	tpl := prompt.FromMessages(schema.FormatFString, schema.SystemMessage("builder mode: demo"))
	g, err := compose.NewChainBuilder("builder").
		AppendTemplate(tpl).
		AppendPrompt(schema.User, "remember to be concise").
		AppendChatModel(model).
		CompileGraph(context.Background())
	if err != nil {
		fail(err)
	}
	st, trace, err := g.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hello")}, compose.WithGraphChatModelOption(llm.WithMaxTokens(128)))
	if err != nil {
		fail(err)
	}
	fmt.Printf("output: %s\n", st.LastOutput.PlainText())
	fmt.Fprintf(os.Stderr, "graph nodes: %d runs: %d\n", len(trace), len(compose.GraphRunsFromState(st)))
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
