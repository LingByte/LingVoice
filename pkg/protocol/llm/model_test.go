package llm

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestFuncModel_Generate(t *testing.T) {
	m := NewFuncModel("test/model", func(ctx context.Context, input []*schema.Message, opts Options) (*schema.Message, error) {
		return schema.AssistantMessage("pong", nil), nil
	}, nil)
	out, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("ping")})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "pong" {
		t.Fatalf("content=%q", out.Content)
	}
}

func TestApplyOptions(t *testing.T) {
	o := ApplyOptions(WithTemperature(0.2), WithMaxTokens(100), WithToolChoice(ToolChoiceForced))
	if o.Temperature == nil || *o.Temperature != 0.2 {
		t.Fatal("temperature")
	}
	if o.MaxTokens == nil || *o.MaxTokens != 100 {
		t.Fatal("max tokens")
	}
	if o.ToolChoice != ToolChoiceForced {
		t.Fatal("tool choice")
	}
}
