// Command llm-demo: basic ChatModel generate/stream with metrics.
//
//	go run ./cmd/llm-demo -prompt "用一句话介绍 LLM 编排"
//	DASHSCOPE_API_KEY=... go run ./cmd/llm-demo -model qwen-plus -base-url https://dashscope.aliyuncs.com/compatible-mode/v1 -stream
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/LingByte/LingVoice/cmd/internal/demo"
	"github.com/LingByte/LingVoice/pkg/llm/metrics"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
)

func main() {
	fs := flag.NewFlagSet("llm-demo", flag.ExitOnError)
	f := demo.RegisterFlags(fs)
	if f.Prompt == "" {
		f.Prompt = "Explain what an LLM orchestration kernel does in one short paragraph."
	}
	_ = fs.Parse(os.Args[1:])

	store := metrics.NewMemoryStore()
	defer store.Close()

	ctx, cancel, slot := demo.Context(2 * time.Minute)
	defer cancel()

	inner, name, err := demo.BuildModel(f.Provider, f.Model, f.BaseURL)
	demo.Fatal(err)
	fmt.Fprintf(os.Stderr, "demo: llm-demo | provider: %s\n", name)

	chat := demo.WrapMetrics(inner, store, f.Metrics)
	msgs := demo.UserMessages(f.System, f.Prompt)
	opts := []llm.Option{}
	if f.Model != "" {
		opts = append(opts, llm.WithModel(f.Model))
	}

	if f.Stream {
		demo.Fatal(demo.RunStream(ctx, chat, msgs, opts...))
		fmt.Println()
	} else {
		out, err := chat.Generate(ctx, msgs, opts...)
		demo.Fatal(err)
		demo.PrintMessage(out)
	}
	if f.Metrics {
		demo.PrintRunMetrics(store, slot.ID)
		demo.PrintSnapshot(store)
	}
}
