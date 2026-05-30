package metrics

import (
	"unicode/utf8"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// EstimateUsageFromMessage heuristically fills token usage when the provider
// omits it (e.g. streaming without include_usage). Prefer real provider counts.
func EstimateUsageFromMessage(msg *schema.Message, inputMessages int) *schema.TokenUsage {
	if msg == nil {
		return nil
	}
	text := msg.PlainText()
	if text == "" && len(msg.ToolCalls) == 0 {
		return nil
	}
	completion := estimateTokens(text)
	if len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			completion += estimateTokens(tc.Function.Name + tc.Function.Arguments)
		}
	}
	prompt := inputMessages * 8
	if prompt == 0 {
		prompt = 1
	}
	return &schema.TokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}
}

func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	n := utf8.RuneCountInString(s)
	if n <= 0 {
		return 1
	}
	// Rough CJK/Latin mix: ~1.5 chars per token.
	t := int(float64(n) / 1.5)
	if t < 1 {
		return 1
	}
	return t
}
