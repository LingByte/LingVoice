package planexecute

import (
	"context"
	"fmt"
	"strings"

	"github.com/LingByte/LingVoice/pkg/llm/adk"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// Planner produces a numbered plan from user input (Eino planexecute subset).
type Planner interface {
	Plan(ctx context.Context, messages []*schema.Message) ([]string, error)
}

// Executor runs one plan step.
type Executor interface {
	Execute(ctx context.Context, step string, messages []*schema.Message) (*schema.Message, error)
}

// DefaultPlanner splits user text into simple steps by newline or sentence.
type DefaultPlanner struct{}

func (DefaultPlanner) Plan(_ context.Context, messages []*schema.Message) ([]string, error) {
	text := lastUserText(messages)
	if text == "" {
		return nil, fmt.Errorf("planexecute: empty input")
	}
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == ';'
	})
	var steps []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			steps = append(steps, p)
		}
	}
	if len(steps) == 0 {
		steps = []string{text}
	}
	return steps, nil
}

// Config configures plan-execute agent (Eino planexecute subset).
type Config struct {
	Name     string
	Planner  Planner
	Executor Executor
}

// Agent plans then executes each step sequentially.
type Agent struct {
	name     string
	planner  Planner
	executor Executor
}

// New builds a plan-execute agent.
func New(cfg Config) (*Agent, error) {
	if cfg.Executor == nil {
		return nil, fmt.Errorf("planexecute: nil executor")
	}
	planner := cfg.Planner
	if planner == nil {
		planner = DefaultPlanner{}
	}
	name := cfg.Name
	if name == "" {
		name = "plan-execute"
	}
	return &Agent{name: name, planner: planner, executor: cfg.Executor}, nil
}

func (a *Agent) Name(_ context.Context) string {
	if a == nil || a.name == "" {
		return "plan-execute"
	}
	return a.name
}

func (a *Agent) Description(_ context.Context) string {
	return "plans tasks then executes each step"
}

func (a *Agent) Run(ctx context.Context, input *adk.AgentInput, _ ...adk.RunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	it := adk.NewAsyncIterator[*adk.AgentEvent](4)
	go func() {
		defer it.Close()
		if a == nil || a.executor == nil {
			it.Send(&adk.AgentEvent{Kind: adk.EventError, Err: fmt.Errorf("planexecute: nil agent")})
			return
		}
		msgs := append([]*schema.Message(nil), adk.MessagesFromInput(input)...)
		steps, err := a.planner.Plan(ctx, msgs)
		if err != nil {
			it.Send(&adk.AgentEvent{Kind: adk.EventError, Err: err})
			return
		}
		it.Send(&adk.AgentEvent{
			Kind:   adk.EventAction,
			Action: "plan",
			Data:   map[string]any{"steps": steps},
		})
		for i, step := range steps {
			out, err := a.executor.Execute(ctx, step, msgs)
			if err != nil {
				it.Send(&adk.AgentEvent{Kind: adk.EventError, Err: err})
				return
			}
			it.Send(&adk.AgentEvent{
				Kind:    adk.EventMessage,
				Message: out,
				Data:    map[string]any{"step_index": i, "step": step},
			})
			if out != nil {
				msgs = append(msgs, out)
			}
		}
		it.Send(&adk.AgentEvent{Kind: adk.EventDone})
	}()
	return it
}

func lastUserText(messages []*schema.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m != nil && m.Role == schema.User {
			return m.PlainText()
		}
	}
	return ""
}
