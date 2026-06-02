// Command react-demo: ReAct agent with calculator tools (Eino react.Agent subset).
//
//	go run ./cmd/react-demo -verbose -force-tools \
//	  -model qwen-plus -base-url https://dashscope.aliyuncs.com/compatible-mode/v1 \
//	  -prompt "计算 (17+25)*3，每一步都必须调用工具"
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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
	fs := flag.NewFlagSet("react-demo", flag.ExitOnError)
	verbose := fs.Bool("verbose", true, "print ReAct step trace to stderr")
	forceTools := fs.Bool("force-tools", false, "require tool call on first model turn")
	useGraphTrace := fs.Bool("graph-trace", false, "also print raw graph node trace")
	f := demo.RegisterFlags(fs)
	if f.Prompt == "" {
		f.Prompt = "Compute (17+25)*3. You MUST call add and multiply tools for each arithmetic step, then state the final answer."
	}
	system := "You are a calculator assistant. Never do mental arithmetic — always call add/multiply/sqrt tools for every numeric step. Reply in the user's language."
	_ = fs.Parse(os.Args[1:])

	store := metrics.NewMemoryStore()
	defer store.Close()
	ctx, cancel, slot := demo.Context(3 * time.Minute)
	defer cancel()

	inner, name, err := demo.BuildModel(f.Provider, f.Model, f.BaseURL)
	demo.Fatal(err)
	fmt.Fprintf(os.Stderr, "demo: react-demo | provider: %s | verbose=%v force-tools=%v\n",
		name, *verbose, *forceTools)

	tc, ok := inner.(llm.ToolCallingChatModel)
	if !ok {
		demo.Fatal(fmt.Errorf("provider %s does not support tool calling", name))
	}
	if f.Metrics {
		wrapped := demo.WrapMetrics(tc, store, true)
		tc, ok = wrapped.(llm.ToolCallingChatModel)
		if !ok {
			demo.Fatal(fmt.Errorf("metrics wrap lost ToolCallingChatModel"))
		}
	}

	tools, err := demo.CalculatorTools()
	demo.Fatal(err)

	toolHandler := metrics.NewToolHandler(store)
	agentCfg := agent.ReActConfig{
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
	}

	msgs := demo.UserMessages("", f.Prompt)

	if f.Stream {
		var react *agent.ReActAgent
		react, err = agent.NewReActAgent(ctx, agentCfg)
		demo.Fatal(err)
		sr, err := react.Stream(ctx, msgs)
		demo.Fatal(err)
		defer sr.Close()
		for {
			chunk, err := sr.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				demo.Fatal(err)
			}
			if chunk != nil && chunk.Content != "" {
				fmt.Print(chunk.Content)
			}
		}
		fmt.Println()
		fmt.Fprintln(os.Stderr, "hint: use -stream=false -verbose for full ReAct step trace")
	} else {
		react, err := agent.NewReActAgent(ctx, agentCfg)
		demo.Fatal(err)
		result, err := react.GenerateWithTrace(ctx, msgs)
		demo.Fatal(err)
		if *verbose {
			demo.PrintLoopTrace(result.Trace)
			demo.PrintLoopTraceWarning(result.Trace)
		}
		if *useGraphTrace && len(result.GraphTrace) > 0 {
			fmt.Fprintf(os.Stderr, "--- graph nodes (%d) ---\n", len(result.GraphTrace))
			for i, s := range result.GraphTrace {
				fmt.Fprintf(os.Stderr, "  [%d] %s (%s)\n", i, s.Node, s.Duration)
			}
		}
		demo.PrintMessage(result.Message)
	}
	if f.Metrics {
		demo.PrintRunMetrics(store, slot.ID)
		demo.PrintSnapshot(store)
		if tr := store.ListTools(10); len(tr) > 0 {
			fmt.Fprintf(os.Stderr, "--- tool runs: %d ---\n", store.ToolTotal())
		}
	}
}
