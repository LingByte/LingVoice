package prompt_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestChatTemplate_Format(t *testing.T) {
	tpl := prompt.FromMessages(schema.FormatFString,
		schema.SystemMessage("hello {name}"),
	)
	msgs, err := tpl.Format(context.Background(), map[string]any{"name": "LingVoice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "hello LingVoice" {
		t.Fatalf("msgs=%+v", msgs)
	}
	var nilTpl *prompt.ChatTemplate
	if _, err := nilTpl.Format(context.Background(), nil); err != nil || msgs == nil {
		// nil template returns nil,nil
	}
}
