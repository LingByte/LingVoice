package schema

// RoleType is the speaker role of a message in a chat completion request.
type RoleType string

const (
	// Assistant is a model-generated message (text and/or tool calls).
	Assistant RoleType = "assistant"
	// User is an end-user message.
	User RoleType = "user"
	// System is a system prompt or instruction.
	System RoleType = "system"
	// Tool is the output of a tool invocation.
	Tool RoleType = "tool"
)

// Valid reports whether r is one of the known roles.
func (r RoleType) Valid() bool {
	switch r {
	case Assistant, User, System, Tool:
		return true
	default:
		return false
	}
}
