// Command plan-execute-demo: ADK plan-execute pattern (local, no API key).
//
//	go run ./cmd/plan-execute-demo
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/adk"
	"github.com/LingByte/LingVoice/pkg/llm/adk/prebuilt/planexecute"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type stepExec struct{}

func (stepExec) Execute(_ context.Context, step string, _ []*schema.Message) (*schema.Message, error) {
	return schema.AssistantMessage("done: "+step, nil), nil
}

func main() {
	fmt.Fprintln(os.Stderr, "demo: plan-execute-demo | local ADK plan-execute")

	ag, err := planexecute.New(planexecute.Config{
		Name:     "demo-plan-execute",
		Executor: stepExec{},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	it := ag.Run(context.Background(), &adk.AgentInput{
		Messages: []*schema.Message{
			schema.UserMessage("gather requirements; design API; write tests"),
		},
	})
	for {
		ev, ok := it.Next()
		if !ok {
			break
		}
		if ev == nil {
			continue
		}
		switch ev.Kind {
		case adk.EventAction:
			if ev.Action == "plan" {
				fmt.Fprintln(os.Stderr, "plan:", ev.Data["steps"])
			}
		case adk.EventMessage:
			fmt.Println(ev.Message.PlainText())
		case adk.EventError:
			fmt.Fprintf(os.Stderr, "error: %v\n", ev.Err)
			os.Exit(1)
		}
	}
}
