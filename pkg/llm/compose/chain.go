// Package compose provides minimal LLM orchestration primitives (Eino compose subset).
//
// Phase 1: sequential Chain only. Graph and ToolNode follow in later phases.
package compose

import (
	"context"
	"fmt"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// PromptStep renders a system or user template message into state.
type PromptStep struct {
	Role    schema.RoleType
	Content string
}

func (s PromptStep) Run(ctx context.Context, st *State) error {
	if s.Content == "" {
		return nil
	}
	st.Messages = append(st.Messages, &schema.Message{Role: s.Role, Content: s.Content})
	return nil
}

// ChatModelStep invokes a ChatModel with current messages and appends the reply.
type ChatModelStep struct {
	Model llm.ChatModel
	Opts  []llm.Option
}

func (s ChatModelStep) Run(ctx context.Context, st *State) error {
	if s.Model == nil {
		return fmt.Errorf("compose: nil ChatModel")
	}
	start := time.Now()
	out, err := s.Model.Generate(ctx, st.Messages, s.Opts...)
	entry := RunEntry{
		Step:          "ChatModel",
		Model:         modelName(s.Model),
		InputMessages: len(st.Messages),
		StartedAt:     start,
		DurationMs:    float64(time.Since(start)) / float64(time.Millisecond),
	}
	if out != nil && out.ResponseMeta != nil && out.ResponseMeta.Usage != nil {
		entry.Usage = out.ResponseMeta.Usage
	}
	if err != nil {
		entry.Error = err.Error()
		AppendRun(st, entry)
		return err
	}
	AppendRun(st, entry)
	st.LastOutput = out
	if out != nil {
		st.Messages = append(st.Messages, out)
	}
	return nil
}

// Step is one unit in a Chain.
type Step interface {
	Run(ctx context.Context, st *State) error
}

// State is shared mutable state for a Chain run.
type State struct {
	Messages   []*schema.Message
	LastOutput *schema.Message
	Vars       map[string]any
}

// Chain runs steps sequentially (Eino Chain subset).
type Chain struct {
	name  string
	steps []Step
}

// NewChain builds a named chain.
func NewChain(name string, steps ...Step) *Chain {
	return &Chain{name: name, steps: steps}
}

// Invoke executes all steps.
func (c *Chain) Invoke(ctx context.Context, input []*schema.Message, opts ...llm.Option) (*State, error) {
	if c == nil {
		return nil, fmt.Errorf("compose: nil chain")
	}
	ctx = callback.InitRun(ctx, &callback.RunInfo{Name: c.name, Component: callback.ComponentChain})
	st := &State{Messages: append([]*schema.Message(nil), input...), Vars: map[string]any{}}
	for i, step := range c.steps {
		if step == nil {
			return st, fmt.Errorf("compose: step %d is nil", i)
		}
		if err := step.Run(ctx, st); err != nil {
			return st, fmt.Errorf("compose chain %q step %d: %w", c.name, i, err)
		}
	}
	return st, nil
}

// ChatModelStepOption applies llm.Option to the embedded ChatModelStep when built inline.
func ChatModelStepOption(step *ChatModelStep, opts ...llm.Option) *ChatModelStep {
	step.Opts = append(step.Opts, opts...)
	return step
}
