package a2a

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func schemaRoleFromA2A(role string) schema.RoleType {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "agent", "assistant":
		return schema.Assistant
	case "system":
		return schema.System
	case "tool":
		return schema.Tool
	default:
		return schema.User
	}
}

func a2aRoleFromSchema(role schema.RoleType) string {
	switch role {
	case schema.Assistant, schema.Tool:
		return "agent"
	case schema.System:
		return "agent"
	default:
		return "user"
	}
}

func textFromParts(parts []Part) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Kind == "text" && p.Text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func partsFromText(text string) []Part {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return []Part{{Kind: "text", Text: text}}
}

func a2aMessageFromSchema(m *schema.Message) *A2AMessage {
	if m == nil {
		return nil
	}
	msg := &A2AMessage{
		Kind:      "message",
		Role:      a2aRoleFromSchema(m.Role),
		MessageID: newID(),
		Parts:     partsFromText(m.PlainText()),
	}
	return msg
}

func schemaMessagesFromA2A(msgs []A2AMessage) []*schema.Message {
	out := make([]*schema.Message, 0, len(msgs))
	for i := range msgs {
		if text := textFromParts(msgs[i].Parts); text != "" {
			role := schemaRoleFromA2A(msgs[i].Role)
			if role == schema.Assistant {
				out = append(out, schema.AssistantMessage(text, nil))
			} else {
				out = append(out, schema.UserMessage(text))
			}
		}
	}
	return out
}

func schemaMessagesFromRequest(params MessageSendParams, history []A2AMessage) []*schema.Message {
	msgs := append(append([]A2AMessage{}, history...), params.Message)
	return schemaMessagesFromA2A(msgs)
}

func messageRequestFromSend(params MessageSendParams, taskID, contextID string, blocking bool) *MessageRequest {
	msgs := schemaMessagesFromA2A([]A2AMessage{params.Message})
	req := &MessageRequest{
		Messages:  msgs,
		TaskID:    taskID,
		ContextID: contextID,
		Blocking:  blocking,
	}
	if len(params.Metadata) > 0 {
		req.Vars = map[string]any{"metadata": params.Metadata}
	}
	return req
}

func taskFromAgentMessage(taskID, contextID string, userMsg A2AMessage, agent *schema.Message, execErr error) *Task {
	userMsg.Kind = "message"
	if userMsg.MessageID == "" {
		userMsg.MessageID = newID()
	}
	userMsg.TaskID = taskID
	userMsg.ContextID = contextID
	task := &Task{
		Kind:      "task",
		ID:        taskID,
		ContextID: contextID,
		History:   []A2AMessage{userMsg},
	}
	if agent != nil {
		agentA2A := a2aMessageFromSchema(agent)
		agentA2A.TaskID = taskID
		agentA2A.ContextID = contextID
		task.Artifacts = []Artifact{{
			ArtifactID: newID(),
			Name:       "response",
			Parts:      agentA2A.Parts,
		}}
	}
	if execErr != nil {
		task.Status = TaskStatus{State: TaskFailed, Message: &A2AMessage{
			Kind: "message", Role: "agent", Parts: partsFromText(execErr.Error()),
		}, Timestamp: nowRFC3339()}
		return task
	}
	task.Status = TaskStatus{State: TaskCompleted, Timestamp: nowRFC3339()}
	return task
}

func sendResultFromHandler(useTask bool, taskID, contextID string, userMsg A2AMessage, agent *schema.Message) any {
	if useTask {
		return taskFromAgentMessage(taskID, contextID, userMsg, agent, nil)
	}
	msg := a2aMessageFromSchema(agent)
	if msg == nil {
		msg = &A2AMessage{Kind: "message", Role: "agent", Parts: nil}
	}
	msg.ContextID = contextID
	msg.TaskID = taskID
	return msg
}
