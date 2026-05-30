package schema

// ResponseMeta collects metadata returned with a model completion.
type ResponseMeta struct {
	// FinishReason is provider-specific, e.g. "stop", "length", "tool_calls".
	FinishReason string `json:"finish_reason,omitempty"`
	Usage        *TokenUsage `json:"usage,omitempty"`
}

// TokenUsage is token accounting for a single model call.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// CachedTokens counts prompt tokens served from cache when supported.
	CachedTokens int `json:"cached_tokens,omitempty"`
	// ReasoningTokens counts tokens used for model reasoning when supported.
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// Add accumulates u into dst. Either pointer may be nil.
func (u *TokenUsage) Add(other *TokenUsage) {
	if u == nil || other == nil {
		return
	}
	if other.PromptTokens > u.PromptTokens {
		u.PromptTokens = other.PromptTokens
	}
	if other.CompletionTokens > u.CompletionTokens {
		u.CompletionTokens = other.CompletionTokens
	}
	if other.TotalTokens > u.TotalTokens {
		u.TotalTokens = other.TotalTokens
	}
	if other.CachedTokens > u.CachedTokens {
		u.CachedTokens = other.CachedTokens
	}
	if other.ReasoningTokens > u.ReasoningTokens {
		u.ReasoningTokens = other.ReasoningTokens
	}
}
