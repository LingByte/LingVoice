// Package adk provides Agent Development Kit primitives (Eino adk subset).
package adk

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// AgentInput is the standard agent run input (Eino adk subset).
type AgentInput struct {
	Messages []*schema.Message
	Vars     map[string]any
}

// AgentEventKind classifies streaming agent events.
type AgentEventKind string

const (
	EventMessage AgentEventKind = "message"
	EventAction  AgentEventKind = "action"
	EventError   AgentEventKind = "error"
	EventDone    AgentEventKind = "done"
)

// AgentEvent is one item from Agent.Run (Eino adk.AgentEvent subset).
type AgentEvent struct {
	Kind    AgentEventKind
	Message *schema.Message
	Action  string
	Data    map[string]any
	Err     error
}

// Agent is the ADK agent interface (Eino adk.Agent subset).
type Agent interface {
	Name(ctx context.Context) string
	Description(ctx context.Context) string
	Run(ctx context.Context, input *AgentInput, opts ...RunOption) *AsyncIterator[*AgentEvent]
}

// ResumableAgent supports checkpoint resume (Eino adk.ResumableAgent subset).
type ResumableAgent interface {
	Agent
	Resume(ctx context.Context, checkpointID string, resume map[string]any, opts ...RunOption) *AsyncIterator[*AgentEvent]
}

// RunOption configures agent runs.
type RunOption func(*runConfig)

type runConfig struct {
	checkpointID string
	resumeData   map[string]any
}

// WithCheckpointID binds a checkpoint for resumable runs.
func WithCheckpointID(id string) RunOption {
	return func(c *runConfig) { c.checkpointID = id }
}

// WithResumeData passes HITL resume payloads.
func WithResumeData(data map[string]any) RunOption {
	return func(c *runConfig) { c.resumeData = data }
}

func applyRunOptions(opts ...RunOption) runConfig {
	var cfg runConfig
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	return cfg
}

// MessagesFromInput returns input messages or nil.
func MessagesFromInput(in *AgentInput) []*schema.Message {
	if in == nil {
		return nil
	}
	return in.Messages
}
