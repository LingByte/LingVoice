// Command graph-demo: compiled ReAct Graph (Eino compose.Graph + react.Agent).
//
//	go run ./cmd/graph-demo -verbose -force-tools \
//	  -model qwen-plus -base-url https://dashscope.aliyuncs.com/compatible-mode/v1 \
//	  -prompt "用 add 和 multiply 计算 (8+7)*6"
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/LingByte/LingVoice/cmd/internal/demo"
	"github.com/LingByte/LingVoice/pkg/llm/agent"
	"github.com/LingByte/LingVoice/pkg/llm/callback"
	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/metrics"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func main() {
	fs := flag.NewFlagSet("graph-demo", flag.ExitOnError)
	verbose := fs.Bool("verbose", true, "print graph node trace")
	forceTools := fs.Bool("force-tools", false, "force tool call on first turn")
	f := demo.RegisterFlags(fs)
	if f.Prompt == "" {
		f.Prompt = "Use add and multiply tools to compute (8+7)*6, then answer."
	}
	system := "You are a calculator. Use tools for every arithmetic step."
	_ = fs.Parse(os.Args[1:])

	store := metrics.NewMemoryStore()
	defer store.Close()
	ctx, cancel, slot := demo.Context(3 * time.Minute)
	defer cancel()

	inner, name, err := demo.BuildModel(f.Provider, f.Model, f.BaseURL)
	demo.Fatal(err)
	fmt.Fprintf(os.Stderr, "demo: graph-demo (compiled) | provider: %s\n", name)

	tc, ok := inner.(llm.ToolCallingChatModel)
	if !ok {
		demo.Fatal(fmt.Errorf("provider %s does not support tool calling", name))
	}
	if f.Metrics {
		wrapped := demo.WrapMetrics(tc, store, true)
		tc, _ = wrapped.(llm.ToolCallingChatModel)
	}

	tools, err := demo.CalculatorTools()
	demo.Fatal(err)

	toolHandler := metrics.NewToolHandler(store)
	graphAgent, err := agent.NewGraphAgent(ctx, agent.ReActConfig{
		Model:        tc,
		Tools:        tools,
		ForceToolUse: *forceTools,
		ToolNodeConfig: &compose.ToolNodeConfig{
			Tools:        tools,
			ErrorHandler: tool.DefaultErrorHandler,
			Handlers:     []callback.Handler{toolHandler},
		},
		MessageModifier: func(_ context.Context, msgs []*schema.Message) []*schema.Message {
			return append([]*schema.Message{schema.SystemMessage(system)}, msgs...)
		},
	})
	demo.Fatal(err)

	result, err := graphAgent.GenerateWithTrace(ctx, demo.UserMessages("", f.Prompt))
	demo.Fatal(err)

	if *verbose && result != nil {
		fmt.Fprintf(os.Stderr, "--- compiled graph trace (%d nodes) ---\n", len(result.Trace))
		for i, step := range result.Trace {
			fmt.Fprintf(os.Stderr, "  [%d] %s (%s)\n", i, step.Node, step.Duration)
		}
		if b, err := json.MarshalIndent(result.Trace, "", "  "); err == nil {
			fmt.Fprintf(os.Stderr, "%s\n", b)
		}
	}
	demo.PrintMessage(result.Message)

	if f.Metrics {
		demo.PrintRunMetrics(store, slot.ID)
		if tools := store.ListTools(10); len(tools) > 0 {
			b, _ := json.MarshalIndent(tools, "", "  ")
			fmt.Fprintf(os.Stderr, "--- tool metrics (%d) ---\n%s\n", store.ToolTotal(), b)
		}
	}
}
