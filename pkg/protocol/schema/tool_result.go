package schema

// ToolArgument is structured tool input (Eino schema.ToolArgument subset).
type ToolArgument struct {
	Text  string       `json:"text,omitempty"`
	Parts []InputPart  `json:"parts,omitempty"`
}

// ToolResult is structured tool output, optionally multimodal (Eino schema.ToolResult subset).
type ToolResult struct {
	Text  string       `json:"text,omitempty"`
	Parts []OutputPart `json:"parts,omitempty"`
}

// PlainText returns the best-effort text body.
func (r *ToolResult) PlainText() string {
	if r == nil {
		return ""
	}
	if r.Text != "" {
		return r.Text
	}
	for _, p := range r.Parts {
		if p.Type == PartTypeText && p.Text != "" {
			return p.Text
		}
	}
	return ""
}

// ToToolMessageContent returns JSON or plain text for a tool role message.
func (r *ToolResult) ToToolMessageContent() string {
	if r == nil {
		return ""
	}
	if r.Text != "" && len(r.Parts) == 0 {
		return r.Text
	}
	return r.PlainText()
}
