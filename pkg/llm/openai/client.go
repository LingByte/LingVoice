package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LingByte/LingVoice/pkg/llm/internal/httputil"
	toolconv "github.com/LingByte/LingVoice/pkg/llm/internal/tools"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// Name returns provider/model identifier.
func (m *ChatModel) Name() string {
	if m == nil {
		return ""
	}
	return "openai/" + m.model
}

// WithTools returns a copy with default tools attached.
func (m *ChatModel) WithTools(tools []*schema.ToolInfo) (llm.ToolCallingChatModel, error) {
	c := m.clone()
	c.tools = append([]*schema.ToolInfo(nil), tools...)
	return c, nil
}

// Generate calls Chat Completions without streaming.
func (m *ChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...llm.Option) (*schema.Message, error) {
	if m == nil {
		return nil, fmt.Errorf("openai: nil model")
	}
	o := llm.ApplyOptions(opts...)
	req, err := m.buildRequest(input, o, false)
	if err != nil {
		return nil, err
	}
	var resp chatResponse
	err = httputil.DoJSON(ctx, m.client, "POST", m.baseURL+"/chat/completions", m.headers(), req, &resp)
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai: empty choices")
	}
	choice := resp.Choices[0]
	usage := &schema.TokenUsage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}
	return fromAPIMessage(choice.Message, choice.FinishReason, usage), nil
}

// Stream calls Chat Completions with streaming enabled.
func (m *ChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
	if m == nil {
		return nil, fmt.Errorf("openai: nil model")
	}
	o := llm.ApplyOptions(opts...)
	req, err := m.buildRequest(input, o, true)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](16)
	go func() {
		defer sw.Close()
		err := httputil.PostSSE(ctx, m.client, m.baseURL+"/chat/completions", m.headers(), req, func(data string) error {
			if data == "[DONE]" {
				return nil
			}
			var chunk chatResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				return err
			}
			if usage := usageFromResponse(chunk.Usage); usage != nil {
				sw.Send(&schema.Message{
					Role:         schema.Assistant,
					ResponseMeta: &schema.ResponseMeta{Usage: usage},
				}, nil)
			}
			if len(chunk.Choices) == 0 {
				return nil
			}
			choice := chunk.Choices[0]
			delta := fromDelta(choice.Delta, choice.FinishReason)
			if delta.Content == "" && len(delta.ToolCalls) == 0 && choice.FinishReason == "" {
				return nil
			}
			sw.Send(delta, nil)
			return nil
		})
		if err != nil {
			sw.Send(nil, err)
		}
	}()
	return sr, nil
}

func (m *ChatModel) headers() map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + m.apiKey,
	}
}

func (m *ChatModel) buildRequest(input []*schema.Message, o llm.Options, stream bool) (*chatRequest, error) {
	msgs, err := toAPIMessages(input)
	if err != nil {
		return nil, err
	}
	model := m.model
	if o.Model != "" {
		model = o.Model
	}
	req := &chatRequest{
		Model:       model,
		Messages:    msgs,
		Temperature: o.Temperature,
		MaxTokens:   o.MaxTokens,
		TopP:        o.TopP,
		Stop:        o.Stop,
		Stream:      stream,
	}
	if stream {
		req.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	tools := toolconv.MergeTools(m.tools, o.Tools)
	if len(tools) > 0 {
		req.Tools = make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			ot, err := toolconv.OpenAITool(t)
			if err != nil {
				return nil, err
			}
			req.Tools = append(req.Tools, ot)
		}
		choice := o.ToolChoice
		if choice == "" {
			choice = schema.ToolChoiceAllowed
		}
		req.ToolChoice = toolconv.OpenAIToolChoice(choice, true)
	}
	return req, nil
}

// Ensure ChatModel satisfies interfaces at compile time.
var (
	_ llm.ChatModel            = (*ChatModel)(nil)
	_ llm.ToolCallingChatModel = (*ChatModel)(nil)
)

// NormalizeBaseURL trims trailing slashes for tests embedding httptest URLs.
func NormalizeBaseURL(u string) string {
	return strings.TrimRight(u, "/")
}
