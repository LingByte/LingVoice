// Package prompt provides chat templates (Eino components/prompt subset).
package prompt

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// ChatTemplate renders message templates with variables.
type ChatTemplate struct {
	templates  []schema.MessagesTemplate
	formatType schema.FormatType
	name       string
}

// FromMessages builds a ChatTemplate (Eino prompt.FromMessages).
func FromMessages(format schema.FormatType, templates ...schema.MessagesTemplate) *ChatTemplate {
	return &ChatTemplate{
		templates:  templates,
		formatType: format,
		name:       "Default",
	}
}

// Format renders all templates into messages.
func (t *ChatTemplate) Format(ctx context.Context, vars map[string]any) ([]*schema.Message, error) {
	if t == nil {
		return nil, nil
	}
	ctx = callback.InitRun(ctx, &callback.RunInfo{Name: t.name, Component: callback.ComponentPrompt})
	out := make([]*schema.Message, 0, len(t.templates))
	for _, tmpl := range t.templates {
		msgs, err := tmpl.Format(ctx, vars, t.formatType)
		if err != nil {
			callback.OnError(ctx, err)
			return nil, err
		}
		out = append(out, msgs...)
	}
	return out, nil
}
