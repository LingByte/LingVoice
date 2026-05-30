// Command supervisor-demo: ADK supervisor pattern (local, no API key).
//
//	go run ./cmd/supervisor-demo
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/adk"
	"github.com/LingByte/LingVoice/pkg/llm/adk/prebuilt/supervisor"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type stubGen struct{ name, reply string }

func (s stubGen) Generate(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
	return schema.AssistantMessage(s.reply, nil), nil
}

type router struct{ target string }

func (r router) Name(context.Context) string        { return "supervisor-router" }
func (r router) Description(context.Context) string { return "routes to workers" }
func (r router) Run(_ context.Context, _ *adk.AgentInput, _ ...adk.RunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	it := adk.NewAsyncIterator[*adk.AgentEvent](2)
	it.Send(adk.MakeTransferEvent(r.target))
	it.Send(&adk.AgentEvent{Kind: adk.EventDone})
	it.Close()
	return it
}

func main() {
	fmt.Fprintln(os.Stderr, "demo: supervisor-demo | local ADK supervisor")

	ag, err := supervisor.New(supervisor.Config{
		Name:       "demo-supervisor",
		Supervisor: router{target: "research"},
		Workers: map[string]adk.Agent{
			"research": adk.NewGenerateAdapter("research", "research worker", stubGen{reply: "[research] found: LLM orchestration patterns"}),
			"code":     adk.NewGenerateAdapter("code", "code worker", stubGen{reply: "[code] implemented supervisor pattern"}),
		},
		DefaultWorker: "research",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	msg, err := ag.RunToMessage(context.Background(), []*schema.Message{
		schema.UserMessage("research LLM orchestration"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(msg.PlainText())
}
