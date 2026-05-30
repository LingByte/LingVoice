// Command orchestrate-demo: SessionOrchestrator — multi-turn + template + ReAct + checkpoint.
//
//	go run ./cmd/orchestrate-demo \
//	  -model qwen-plus -base-url https://dashscope.aliyuncs.com/compatible-mode/v1 \
//	  -prompt "计算 (3+4)*2" -prompt2 "刚才结果是多少？"
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/LingByte/LingVoice/cmd/internal/demo"
	"github.com/LingByte/LingVoice/pkg/llm/agent"
	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/metrics"
	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/llm/session"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func main() {
	fs := flag.NewFlagSet("orchestrate-demo", flag.ExitOnError)
	prompt2 := fs.String("prompt2", "", "optional second turn")
	stream := fs.Bool("stream", false, "stream second turn with frame metadata")
	verbose := fs.Bool("verbose", true, "print trace")
	f := demo.RegisterFlags(fs)
	if f.Prompt == "" {
		f.Prompt = "Compute (3+4)*2 using add and multiply tools."
	}
	_ = fs.Parse(os.Args[1:])

	mstore := metrics.NewMemoryStore()
	defer mstore.Close()
	sessStore := session.NewMemoryStore()
	cpStore := compose.NewMemoryCheckPointStore()
	ctx, cancel, slot := demo.Context(3 * time.Minute)
	defer cancel()

	inner, name, err := demo.BuildModel(f.Provider, f.Model, f.BaseURL)
	demo.Fatal(err)
	fmt.Fprintf(os.Stderr, "demo: orchestrate-demo | provider: %s | SessionOrchestrator\n", name)

	tc, ok := inner.(llm.ToolCallingChatModel)
	if !ok {
		demo.Fatal(fmt.Errorf("provider %s does not support tool calling", name))
	}
	if f.Metrics {
		wrapped := demo.WrapMetrics(tc, mstore, true)
		tc, _ = wrapped.(llm.ToolCallingChatModel)
	}

	tools, err := demo.CalculatorTools()
	demo.Fatal(err)

	sess := sessStore.GetOrCreate(ctx, "demo-session")
	react, err := agent.NewReActAgent(ctx, agent.ReActConfig{
		Model:           tc,
		Tools:           tools,
		CheckPointStore: cpStore,
		ToolNodeConfig: &compose.ToolNodeConfig{
			Tools:        tools,
			ErrorHandler: tool.DefaultErrorHandler,
		},
	})
	demo.Fatal(err)

	tpl := prompt.FromMessages(schema.FormatFString,
		schema.SystemMessage(f.System),
		schema.Placeholder("history", true),
	)

	orch, err := agent.NewSessionOrchestrator(agent.SessionOrchestratorConfig{
		Name:         "demo-orch",
		Agent:        react,
		Template:     tpl,
		Session:      sess,
		SessionStore: sessStore,
		CheckPointID: "demo-session-cp",
	})
	demo.Fatal(err)

	runTurn := func(label, text string, useStream bool) {
		fmt.Fprintf(os.Stderr, "\n--- turn: %s ---\n", label)
		if useStream {
			sr, err := orch.StreamTurn(ctx, text)
			demo.Fatal(err)
			defer sr.Close()
			for {
				frame, err := sr.Recv()
				if err != nil {
					break
				}
				if frame != nil && frame.Chunk != nil && frame.Chunk.Content != "" {
					fmt.Print(frame.Chunk.Content)
				}
				if frame != nil && frame.Done {
					fmt.Println()
					break
				}
			}
			_ = sessStore.Save(ctx, orch.Session())
			return
		}
		result, err := orch.RunTurn(ctx, text)
		if *verbose && result.Trace != nil {
			demo.PrintLoopTrace(result.Trace)
		}
		demo.Fatal(err)
		demo.PrintMessage(result.Message)
	}

	runTurn("1", f.Prompt, false)
	if *prompt2 != "" {
		runTurn("2", *prompt2, *stream)
	} else {
		fmt.Fprintln(os.Stderr, "hint: -prompt2 for multi-turn; -stream for framed streaming on turn 2")
	}

	fmt.Fprintf(os.Stderr, "\n--- session (%d messages) pending=%v ---\n",
		len(orch.Session().Messages), orch.Session().HasPending())
	if f.Metrics {
		demo.PrintRunMetrics(mstore, slot.ID)
	}
}
