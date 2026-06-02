// Command chain-demo: sequential Chain — system prompt + user message + ChatModel.
//
//	go run ./cmd/chain-demo -prompt "用一句话介绍 Chain 编排"
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/LingByte/LingVoice/cmd/internal/demo"
	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/metrics"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func main() {
	fs := flag.NewFlagSet("chain-demo", flag.ExitOnError)
	f := demo.RegisterFlags(fs)
	if f.Prompt == "" {
		f.Prompt = "Say hello in one sentence."
	}
	_ = fs.Parse(os.Args[1:])

	store := metrics.NewMemoryStore()
	defer store.Close()
	ctx, cancel, slot := demo.Context(2 * time.Minute)
	defer cancel()

	inner, name, err := demo.BuildModel(f.Provider, f.Model, f.BaseURL)
	demo.Fatal(err)
	fmt.Fprintf(os.Stderr, "demo: chain-demo | provider: %s\n", name)

	chat := demo.WrapMetrics(inner, store, f.Metrics)
	opts := modelOpts(f.Model)

	chain := compose.NewChain("chain-demo",
		compose.PromptStep{Role: schema.System, Content: f.System},
		&compose.ChatModelStep{Model: chat, Opts: opts},
	)

	st, err := chain.Invoke(ctx, []*schema.Message{schema.UserMessage(f.Prompt)})
	demo.Fatal(err)
	demo.PrintMessage(st.LastOutput)
	if f.Metrics {
		demo.PrintRunMetrics(store, slot.ID)
	}
}

func modelOpts(model string) []llm.Option {
	if model == "" {
		return nil
	}
	return []llm.Option{llm.WithModel(model)}
}
