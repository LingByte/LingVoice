package compose

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/llm/prompt"
)

// TemplateStep renders a ChatTemplate into state messages.
type TemplateStep struct {
	Template *prompt.ChatTemplate
}

func (s TemplateStep) Run(ctx context.Context, st *State) error {
	if s.Template == nil {
		return nil
	}
	vars := st.Vars
	if vars == nil {
		vars = map[string]any{}
	}
	msgs, err := s.Template.Format(ctx, vars)
	if err != nil {
		return fmt.Errorf("compose: template step: %w", err)
	}
	st.Messages = append(st.Messages, msgs...)
	return nil
}
