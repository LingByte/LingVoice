package anthropic

import (
	"context"
	"encoding/json"
	"fmt"

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
	return "anthropic/" + m.model
}

// WithTools returns a copy with default tools attached.
func (m *ChatModel) WithTools(tools []*schema.ToolInfo) (llm.ToolCallingChatModel, error) {
	c := m.clone()
	c.tools = append([]*schema.ToolInfo(nil), tools...)
	return c, nil
}

// Generate calls the Messages API without streaming.
func (m *ChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...llm.Option) (*schema.Message, error) {
	if m == nil {
		return nil, fmt.Errorf("anthropic: nil model")
	}
	o := llm.ApplyOptions(opts...)
	req, err := m.buildRequest(input, o, false)
	if err != nil {
		return nil, err
	}
	var resp messagesResponse
	err = httputil.DoJSON(ctx, m.client, "POST", m.baseURL+"/messages", m.headers(), req, &resp)
	if err != nil {
		return nil, err
	}
	return fromResponse(resp)
}

// Stream calls the Messages API with streaming enabled.
func (m *ChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
	if m == nil {
		return nil, fmt.Errorf("anthropic: nil model")
	}
	o := llm.ApplyOptions(opts...)
	req, err := m.buildRequest(input, o, true)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](16)
	go func() {
		defer sw.Close()
		err := httputil.PostSSE(ctx, m.client, m.baseURL+"/messages", m.headers(), req, func(data string) error {
			var ev streamEvent
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				return err
			}
			switch ev.Type {
			case "message_start":
				if u := anthropicUsage(ev.Message.Usage.InputTokens, ev.Message.Usage.OutputTokens); u != nil {
					sw.Send(&schema.Message{Role: schema.Assistant, ResponseMeta: &schema.ResponseMeta{Usage: u}}, nil)
				}
			case "content_block_delta":
				if msg := deltaToMessage(ev.Delta); msg != nil {
					sw.Send(msg, nil)
				}
			case "message_delta":
				if ev.Delta != nil {
					if reason, ok := ev.Delta["stop_reason"].(string); ok && reason != "" {
						sw.Send(&schema.Message{
							Role: schema.Assistant,
							ResponseMeta: &schema.ResponseMeta{FinishReason: reason},
						}, nil)
					}
				}
				if u := anthropicUsage(ev.Usage.InputTokens, ev.Usage.OutputTokens); u != nil {
					sw.Send(&schema.Message{Role: schema.Assistant, ResponseMeta: &schema.ResponseMeta{Usage: u}}, nil)
				}
			case "content_block_start":
				if typ, _ := ev.ContentBlock["type"].(string); typ == "tool_use" {
					idx := ev.Index
					id, _ := ev.ContentBlock["id"].(string)
					name, _ := ev.ContentBlock["name"].(string)
					sw.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							Index: &idx,
							ID:    id,
							Type:  "tool_use",
							Function: schema.FunctionCall{Name: name},
						}},
					}, nil)
				}
			}
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
		"x-api-key":         m.apiKey,
		"anthropic-version": m.apiVersion,
	}
}

func (m *ChatModel) buildRequest(input []*schema.Message, o llm.Options, stream bool) (*messagesRequest, error) {
	system, msgs, err := toAPIMessages(input)
	if err != nil {
		return nil, err
	}
	model := m.model
	if o.Model != "" {
		model = o.Model
	}
	maxTok := m.maxTokens
	if o.MaxTokens != nil {
		maxTok = *o.MaxTokens
	}
	req := &messagesRequest{
		Model:         model,
		MaxTokens:     maxTok,
		System:        system,
		Messages:      msgs,
		Temperature:   o.Temperature,
		TopP:          o.TopP,
		StopSequences: o.Stop,
		Stream:        stream,
	}
	tools := toolconv.MergeTools(m.tools, o.Tools)
	if len(tools) > 0 {
		req.Tools = make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			at, err := toolconv.AnthropicTool(t)
			if err != nil {
				return nil, err
			}
			req.Tools = append(req.Tools, at)
		}
		choice := o.ToolChoice
		if choice == "" {
			choice = schema.ToolChoiceAllowed
		}
		req.ToolChoice = toolconv.AnthropicToolChoice(choice, true)
	}
	return req, nil
}

var (
	_ llm.ChatModel            = (*ChatModel)(nil)
	_ llm.ToolCallingChatModel = (*ChatModel)(nil)
)
