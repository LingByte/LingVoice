// Command host-demo: Multi-agent host routing (Eino multi-agent subset).
//
//	go run ./cmd/host-demo
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
	return schema.AssistantMessage(fmt.Sprintf("[%s] handling: %s", e.label, text), nil), nil
}

func main() {
	fmt.Fprintln(os.Stderr, "demo: host-demo | local routing, no API key")

	host, err := agent.NewHostAgent(agent.HostConfig{
		Name: "ling-host",
		Agents: map[string]agent.Agent{
			"calculator": echoAgent{label: "CalculatorAgent"},
			"translator": echoAgent{label: "TranslatorAgent"},
		},
		Default: "calculator",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	for _, prompt := range []string{
		"计算 17+25",
		"translate hello to Chinese",
		"tell me a joke",
	} {
		out, err := host.Generate(context.Background(), []*schema.Message{schema.UserMessage(prompt)})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		fmt.Println(out.PlainText())
	}
}
