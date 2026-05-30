// Command host-compose-demo: Multi-agent host via compose graph + ProcessState.
//
//	go run ./cmd/host-compose-demo
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/agent"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type echoAgent struct{ label string }

func (e echoAgent) Generate(_ context.Context, msgs []*schema.Message) (*schema.Message, error) {
	text := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] != nil && msgs[i].Role == schema.User {
			text = msgs[i].PlainText()
			break
		}
	}
	return schema.AssistantMessage(fmt.Sprintf("[%s] %s", e.label, text), nil), nil
}

func main() {
	fmt.Fprintln(os.Stderr, "demo: host-compose-demo | compose graph routing")

	host, err := agent.NewHostComposeAgent(agent.HostComposeConfig{
		Name: "ling-host-compose",
		Agents: map[string]agent.Agent{
			"calculator": echoAgent{label: "Calc"},
			"translator": echoAgent{label: "Translate"},
		},
		Default: "calculator",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	for _, prompt := range []string{"计算 1+1", "translate hi", "hello"} {
		out, err := host.Generate(context.Background(), []*schema.Message{schema.UserMessage(prompt)})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		fmt.Println(out.PlainText())
	}
}
