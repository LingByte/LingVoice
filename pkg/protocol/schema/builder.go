package schema

// SystemMessage returns a system role message.
func SystemMessage(content string) *Message {
	return &Message{Role: System, Content: content}
}

// UserMessage returns a user role message.
func UserMessage(content string) *Message {
	return &Message{Role: User, Content: content}
}

// UserMessageParts returns a user message with multimodal input parts.
func UserMessageParts(parts []InputPart) *Message {
	return &Message{Role: User, UserInputParts: parts}
}

// AssistantMessage returns an assistant message, optionally with tool calls.
func AssistantMessage(content string, toolCalls []ToolCall) *Message {
	return &Message{Role: Assistant, Content: content, ToolCalls: toolCalls}
}

// ToolMessageOption configures ToolMessage.
type ToolMessageOption func(*toolMessageOpts)

type toolMessageOpts struct {
	name string
}

// WithToolName sets ToolName on a tool message.
func WithToolName(name string) ToolMessageOption {
	return func(o *toolMessageOpts) {
		o.name = name
	}
}

// ToolMessage returns a tool role message linked to a prior tool call.
func ToolMessage(content, toolCallID string, opts ...ToolMessageOption) *Message {
	o := &toolMessageOpts{}
	for _, opt := range opts {
		opt(o)
	}
	return &Message{
		Role:       Tool,
		Content:    content,
		ToolCallID: toolCallID,
		ToolName:   o.name,
	}
}
