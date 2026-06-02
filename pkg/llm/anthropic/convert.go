package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type contentBlock map[string]any

type apiMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type messagesRequest struct {
	Model         string           `json:"model"`
	MaxTokens     int              `json:"max_tokens"`
	System        string           `json:"system,omitempty"`
	Messages      []apiMessage     `json:"messages"`
	Temperature   *float64         `json:"temperature,omitempty"`
	TopP          *float64         `json:"top_p,omitempty"`
	StopSequences []string         `json:"stop_sequences,omitempty"`
	Tools         []map[string]any `json:"tools,omitempty"`
	ToolChoice    map[string]any   `json:"tool_choice,omitempty"`
	Stream        bool             `json:"stream,omitempty"`
}

type messagesResponse struct {
	Content []contentBlock `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type streamEvent struct {
	Type  string         `json:"type"`
	Index int            `json:"index"`
	Delta map[string]any `json:"delta"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	ContentBlock contentBlock `json:"content_block"`
	Message      struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func anthropicUsage(input, output int) *schema.TokenUsage {
	if input == 0 && output == 0 {
		return nil
	}
	return &schema.TokenUsage{
		PromptTokens:     input,
		CompletionTokens: output,
		TotalTokens:      input + output,
	}
}

func toAPIMessages(msgs []*schema.Message) (system string, out []apiMessage, err error) {
	var sysParts []string
	type pending struct {
		role   string
		blocks []contentBlock
	}
	var built []pending

	flush := func(p pending) {
		if len(p.blocks) == 0 {
			return
		}
		out = append(out, apiMessage{Role: p.role, Content: p.blocks})
	}

	appendUserBlocks := func(blocks ...contentBlock) {
		if len(built) > 0 && built[len(built)-1].role == "user" {
			built[len(built)-1].blocks = append(built[len(built)-1].blocks, blocks...)
			return
		}
		built = append(built, pending{role: "user", blocks: blocks})
	}

	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		switch msg.Role {
		case schema.System:
			if t := msg.PlainText(); t != "" {
				sysParts = append(sysParts, t)
			}
		case schema.User:
			text := msg.PlainText()
			if text == "" {
				continue
			}
			if len(built) > 0 && built[len(built)-1].role == "user" {
				built[len(built)-1].blocks = append(built[len(built)-1].blocks, contentBlock{"type": "text", "text": text})
			} else {
				built = append(built, pending{
					role:   "user",
					blocks: []contentBlock{{"type": "text", "text": text}},
				})
			}
		case schema.Assistant:
			var blocks []contentBlock
			if t := msg.PlainText(); t != "" {
				blocks = append(blocks, contentBlock{"type": "text", "text": t})
			}
			for _, tc := range msg.ToolCalls {
				var input any
				if tc.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
						input = map[string]any{"raw": tc.Function.Arguments}
					}
				} else {
					input = map[string]any{}
				}
				blocks = append(blocks, contentBlock{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": input,
				})
			}
			if len(blocks) == 0 {
				continue
			}
			built = append(built, pending{role: "assistant", blocks: blocks})
		case schema.Tool:
			appendUserBlocks(contentBlock{
				"type":        "tool_result",
				"tool_use_id": msg.ToolCallID,
				"content":     msg.Content,
			})
		default:
			return "", nil, fmt.Errorf("anthropic: unsupported role %q", msg.Role)
		}
	}

	for _, p := range built {
		flush(p)
	}
	if len(sysParts) > 0 {
		system = sysParts[0]
		for i := 1; i < len(sysParts); i++ {
			system += "\n" + sysParts[i]
		}
	}
	if len(out) == 0 {
		return system, nil, fmt.Errorf("anthropic: at least one user or assistant message required")
	}
	return system, out, nil
}

func fromResponse(resp messagesResponse) (*schema.Message, error) {
	msg := &schema.Message{
		Role: schema.Assistant,
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: resp.StopReason,
			Usage: &schema.TokenUsage{
				PromptTokens:     resp.Usage.InputTokens,
				CompletionTokens: resp.Usage.OutputTokens,
				TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
			},
		},
	}
	for _, block := range resp.Content {
		typ, _ := block["type"].(string)
		switch typ {
		case "text":
			if t, _ := block["text"].(string); t != "" {
				msg.Content += t
			}
		case "tool_use":
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			argsBytes, _ := json.Marshal(block["input"])
			msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
				ID:   id,
				Type: "tool_use",
				Function: schema.FunctionCall{
					Name:      name,
					Arguments: string(argsBytes),
				},
			})
		}
	}
	return msg, nil
}

func deltaToMessage(delta map[string]any) *schema.Message {
	if delta == nil {
		return nil
	}
	typ, _ := delta["type"].(string)
	switch typ {
	case "text_delta":
		if t, _ := delta["text"].(string); t != "" {
			return &schema.Message{Role: schema.Assistant, Content: t}
		}
	case "input_json_delta":
		// tool argument streaming — expose as partial tool call chunk
		if part, _ := delta["partial_json"].(string); part != "" {
			idx := 0
			return &schema.Message{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{{
					Index: &idx,
					Type:  "tool_use",
					Function: schema.FunctionCall{Arguments: part},
				}},
			}
		}
	}
	return nil
}
