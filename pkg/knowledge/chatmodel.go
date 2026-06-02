package knowledge

import (
	"context"
	"fmt"
	"strings"

	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// TextCompleter generates plain text from a prompt (used by LLMChunker).
type TextCompleter interface {
	Complete(ctx context.Context, prompt string, model string) (string, error)
}

// ChatModelCompleter adapts protocol ChatModel to TextCompleter.
type ChatModelCompleter struct {
	Model llm.ChatModel
	Name  string
}

func (c *ChatModelCompleter) Complete(ctx context.Context, prompt string, model string) (string, error) {
	if c == nil || c.Model == nil {
		return "", fmt.Errorf("knowledge: nil chat model")
	}
	opts := []llm.Option{}
	if m := strings.TrimSpace(model); m != "" {
		opts = append(opts, llm.WithModel(m))
	} else if n := strings.TrimSpace(c.Name); n != "" {
		opts = append(opts, llm.WithModel(n))
	}
	msg, err := c.Model.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)}, opts...)
	if err != nil {
		return "", err
	}
	if msg == nil {
		return "", fmt.Errorf("knowledge: empty chat model response")
	}
	return strings.TrimSpace(msg.PlainText()), nil
}
