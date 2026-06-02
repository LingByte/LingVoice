package schema

import (
	"fmt"
	"strings"
)

// ConcatMessages merges streaming message chunks into one message.
// Content, reasoning, tool calls, and response metadata are combined.
func ConcatMessages(msgs []*Message) (*Message, error) {
	if len(msgs) == 0 {
		return &Message{}, nil
	}
	var (
		contents           []string
		contentLen         int
		reasoningParts     []string
		reasoningLen       int
		toolCalls          []ToolCall
		userParts          []InputPart
		assistantParts     []OutputPart
		ret                Message
		extras             []map[string]any
	)
	for idx, msg := range msgs {
		if msg == nil {
			return nil, fmt.Errorf("schema: nil message chunk at index %d", idx)
		}
		if msg.Role != "" {
			if ret.Role == "" {
				ret.Role = msg.Role
			} else if ret.Role != msg.Role {
				return nil, fmt.Errorf("schema: role mismatch %q vs %q", ret.Role, msg.Role)
			}
		}
		if msg.Name != "" {
			if ret.Name == "" {
				ret.Name = msg.Name
			} else if ret.Name != msg.Name {
				return nil, fmt.Errorf("schema: name mismatch %q vs %q", ret.Name, msg.Name)
			}
		}
		if msg.ToolCallID != "" {
			if ret.ToolCallID == "" {
				ret.ToolCallID = msg.ToolCallID
			} else if ret.ToolCallID != msg.ToolCallID {
				return nil, fmt.Errorf("schema: tool_call_id mismatch")
			}
		}
		if msg.ToolName != "" {
			if ret.ToolName == "" {
				ret.ToolName = msg.ToolName
			} else if ret.ToolName != msg.ToolName {
				return nil, fmt.Errorf("schema: tool_name mismatch")
			}
		}
		if msg.Content != "" {
			contents = append(contents, msg.Content)
			contentLen += len(msg.Content)
		}
		if msg.ReasoningContent != "" {
			reasoningParts = append(reasoningParts, msg.ReasoningContent)
			reasoningLen += len(msg.ReasoningContent)
		}
		if len(msg.ToolCalls) > 0 {
			toolCalls = append(toolCalls, msg.ToolCalls...)
		}
		if len(msg.UserInputParts) > 0 {
			userParts = append(userParts, msg.UserInputParts...)
		}
		if len(msg.AssistantOutputParts) > 0 {
			assistantParts = append(assistantParts, msg.AssistantOutputParts...)
		}
		if len(msg.Extra) > 0 {
			extras = append(extras, msg.Extra)
		}
		if msg.ResponseMeta != nil {
			if ret.ResponseMeta == nil {
				ret.ResponseMeta = &ResponseMeta{}
			}
			if msg.ResponseMeta.FinishReason != "" {
				ret.ResponseMeta.FinishReason = msg.ResponseMeta.FinishReason
			}
			if msg.ResponseMeta.Usage != nil {
				if ret.ResponseMeta.Usage == nil {
					ret.ResponseMeta.Usage = &TokenUsage{}
				}
				ret.ResponseMeta.Usage.Add(msg.ResponseMeta.Usage)
			}
		}
	}
	if len(contents) > 0 {
		var b strings.Builder
		b.Grow(contentLen)
		for _, c := range contents {
			b.WriteString(c)
		}
		ret.Content = b.String()
	}
	if len(reasoningParts) > 0 {
		var b strings.Builder
		b.Grow(reasoningLen)
		for _, c := range reasoningParts {
			b.WriteString(c)
		}
		ret.ReasoningContent = b.String()
	}
	if len(toolCalls) > 0 {
		merged, err := concatToolCalls(toolCalls)
		if err != nil {
			return nil, err
		}
		ret.ToolCalls = merged
	}
	if len(userParts) > 0 {
		ret.UserInputParts = userParts
	}
	if len(assistantParts) > 0 {
		ret.AssistantOutputParts = assistantParts
	}
	if len(extras) == 1 {
		ret.Extra = extras[0]
	} else if len(extras) > 1 {
		ret.Extra = make(map[string]any)
		for _, e := range extras {
			for k, v := range e {
				ret.Extra[k] = v
			}
		}
	}
	return &ret, nil
}
