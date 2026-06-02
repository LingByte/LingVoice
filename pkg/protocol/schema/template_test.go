package schema_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestFormatFString(t *testing.T) {
	msg := schema.SystemMessage("Hello, {name}!")
	msgs, err := msg.Format(context.Background(), map[string]any{"name": "LingVoice"}, schema.FormatFString)
	if err != nil {
		t.Fatal(err)
	}
	if msgs[0].Content != "Hello, LingVoice!" {
		t.Fatalf("content=%q", msgs[0].Content)
	}
}

func TestPlaceholder(t *testing.T) {
	history := []*schema.Message{schema.UserMessage("prior")}
	p := schema.Placeholder("history", false)
	msgs, err := p.Format(context.Background(), map[string]any{"history": history}, schema.FormatFString)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "prior" {
		t.Fatalf("msgs=%+v", msgs)
	}
}
