package openai

import (
	"encoding/json"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type apiMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCalls  []apiToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type apiToolCall struct {
	Index    *int             `json:"index,omitempty"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function apiFunctionCall  `json:"function"`
}

type apiFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatRequest struct {
	Model         string           `json:"model"`
	Messages      []apiMessage     `json:"messages"`
	Temperature   *float64         `json:"temperature,omitempty"`
	MaxTokens     *int             `json:"max_tokens,omitempty"`
	TopP          *float64         `json:"top_p,omitempty"`
	Stop          []string         `json:"stop,omitempty"`
	Tools         []map[string]any `json:"tools,omitempty"`
	ToolChoice    any              `json:"tool_choice,omitempty"`
	Stream        bool             `json:"stream,omitempty"`
	StreamOptions *streamOptions   `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatResponse struct {
	Choices []struct {
		Message      apiMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
		Delta        apiMessage `json:"delta"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func toAPIMessages(msgs []*schema.Message) ([]apiMessage, error) {
	out := make([]apiMessage, 0, len(msgs))
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		am := apiMessage{
			Role:       string(msg.Role),
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
		}
		text := msg.PlainText()
		if text != "" {
			am.Content = text
		}
		if len(msg.ToolCalls) > 0 {
			am.ToolCalls = make([]apiToolCall, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				typ := tc.Type
				if typ == "" {
					typ = "function"
				}
				am.ToolCalls[i] = apiToolCall{
					Index: tc.Index,
					ID:    tc.ID,
					Type:  typ,
					Function: apiFunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}
		if msg.Role == schema.Tool && am.Content == nil {
			am.Content = ""
		}
		out = append(out, am)
	}
	return out, nil
}

func fromAPIMessage(am apiMessage, finish string, usage *schema.TokenUsage) *schema.Message {
	msg := &schema.Message{
		Role:    schema.RoleType(am.Role),
		Content: contentString(am.Content),
		Name:    am.Name,
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: finish,
			Usage:        usage,
		},
	}
	if len(am.ToolCalls) > 0 {
		msg.ToolCalls = make([]schema.ToolCall, len(am.ToolCalls))
		for i, tc := range am.ToolCalls {
			msg.ToolCalls[i] = schema.ToolCall{
				Index: tc.Index,
				ID:    tc.ID,
				Type:  tc.Type,
				Function: schema.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}
		}
	}
	return msg
}

func usageFromResponse(u struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}) *schema.TokenUsage {
	if u.PromptTokens == 0 && u.CompletionTokens == 0 && u.TotalTokens == 0 {
		return nil
	}
	total := u.TotalTokens
	if total == 0 {
		total = u.PromptTokens + u.CompletionTokens
	}
	return &schema.TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      total,
	}
}

func contentString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func fromDelta(am apiMessage, finish string) *schema.Message {
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: contentString(am.Content),
	}
	if finish != "" {
		msg.ResponseMeta = &schema.ResponseMeta{FinishReason: finish}
	}
	if len(am.ToolCalls) > 0 {
		msg.ToolCalls = make([]schema.ToolCall, len(am.ToolCalls))
		for i, tc := range am.ToolCalls {
			msg.ToolCalls[i] = schema.ToolCall{
				Index: tc.Index,
				ID:    tc.ID,
				Type:  tc.Type,
				Function: schema.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}
		}
	}
	return msg
}
