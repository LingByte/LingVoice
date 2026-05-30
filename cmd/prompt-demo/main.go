// Command prompt-demo: ChatTemplate with FString (no API key).
//
//	go run ./cmd/prompt-demo -name LingVoice
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func main() {
	name := flag.String("name", "LingVoice", "project name")
	flag.Parse()
	fmt.Fprintln(os.Stderr, "demo: prompt-demo | local template, no API key")

	tpl := prompt.FromMessages(schema.FormatFString,
		schema.SystemMessage("You are an assistant for project {name}."),
		schema.Placeholder("history", true),
		schema.UserMessage("Summarize {topic} in one sentence."),
	)

	st := &compose.State{
		Vars: map[string]any{
			"name":    *name,
			"topic":   "LLM orchestration",
			"history": []*schema.Message{schema.UserMessage("We are building an AI voice platform.")},
		},
	}
	if err := (compose.TemplateStep{Template: tpl}).Run(context.Background(), st); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	for i, m := range st.Messages {
		fmt.Printf("[%d] %s: %s\n", i, m.Role, m.Content)
	}
}
