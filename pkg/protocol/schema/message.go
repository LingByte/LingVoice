package schema

// Message is the universal unit exchanged between users, chat models, and tools.
type Message struct {
	Role    RoleType `json:"role"`
	Content string   `json:"content"`

	// UserInputParts carries multimodal user input. When non-empty, adapters may
	// prefer this over Content alone.
	UserInputParts []InputPart `json:"user_input_parts,omitempty"`
	// AssistantOutputParts carries multimodal model output.
	AssistantOutputParts []OutputPart `json:"assistant_output_parts,omitempty"`

	Name string `json:"name,omitempty"`

	// ToolCalls is set on assistant messages when the model requests tools.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID and ToolName are set on tool role messages.
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`

	ResponseMeta *ResponseMeta `json:"response_meta,omitempty"`
	// ReasoningContent holds chain-of-thought text from reasoning models.
	ReasoningContent string `json:"reasoning_content,omitempty"`
	Extra            map[string]any `json:"extra,omitempty"`
}

// Clone returns a shallow copy of m. Nil receiver returns nil.
func (m *Message) Clone() *Message {
	if m == nil {
		return nil
	}
	out := *m
	if len(m.ToolCalls) > 0 {
		out.ToolCalls = append([]ToolCall(nil), m.ToolCalls...)
	}
	if len(m.UserInputParts) > 0 {
		out.UserInputParts = append([]InputPart(nil), m.UserInputParts...)
	}
	if len(m.AssistantOutputParts) > 0 {
		out.AssistantOutputParts = append([]OutputPart(nil), m.AssistantOutputParts...)
	}
	return &out
}

// PlainText returns the best-effort text body for logging and voice TTS.
func (m *Message) PlainText() string {
	if m == nil {
		return ""
	}
	if m.Content != "" {
		return m.Content
	}
	if len(m.UserInputParts) > 0 {
		return TextContent(m.UserInputParts)
	}
	for _, p := range m.AssistantOutputParts {
		if p.Type == PartTypeText && p.Text != "" {
			return p.Text
		}
	}
	return ""
}
