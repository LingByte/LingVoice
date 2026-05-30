package deep

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/llm/adk"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// TaskTool dispatches a sub-task to a named sub-agent (Eino deep agent task tool subset).
type TaskTool struct {
	SubAgents map[string]adk.Agent
	Default   string
}

// Run executes a named sub-task.
func (t *TaskTool) Run(ctx context.Context, taskName string, messages []*schema.Message) (*schema.Message, error) {
	if t == nil || len(t.SubAgents) == 0 {
		return nil, fmt.Errorf("deep: no sub-agents")
	}
	ag, ok := t.SubAgents[taskName]
	if !ok || ag == nil {
		return nil, fmt.Errorf("deep: unknown task %q", taskName)
	}
	return adk.RunToMessage(ctx, ag, &adk.AgentInput{Messages: messages})
}

// Config configures a deep multi-agent (Eino deep subset; no filesystem/shell).
type Config struct {
	Name      string
	Lead      adk.Agent
	TaskTool  *TaskTool
	SubAgents map[string]adk.Agent
	MaxTasks  int
}

// Agent coordinates sub-agents via a lead agent and optional explicit tasks.
type Agent struct {
	name      string
	lead      adk.Agent
	taskTool  *TaskTool
	subAgents map[string]adk.Agent
	maxTasks  int
}

// New builds a deep agent.
func New(cfg Config) (*Agent, error) {
	if cfg.Lead == nil {
		return nil, fmt.Errorf("deep: nil lead agent")
	}
	subs, err := adk.SetSubAgents(adk.SubAgentsConfig{Parent: cfg.Lead, SubAgents: cfg.SubAgents})
	if err != nil && len(cfg.SubAgents) > 0 {
		return nil, err
	}
	tt := cfg.TaskTool
	if tt == nil && len(subs) > 0 {
		def := ""
		for k := range subs {
			def = k
			break
		}
		tt = &TaskTool{SubAgents: subs, Default: def}
	}
	maxTasks := cfg.MaxTasks
	if maxTasks <= 0 {
		maxTasks = 8
	}
	name := cfg.Name
	if name == "" {
		name = "deep"
	}
	return &Agent{name: name, lead: cfg.Lead, taskTool: tt, subAgents: subs, maxTasks: maxTasks}, nil
}

func (a *Agent) Name(_ context.Context) string {
	if a == nil || a.name == "" {
		return "deep"
	}
	return a.name
}

func (a *Agent) Description(_ context.Context) string {
	return "deep agent delegates to sub-agents"
}

func (a *Agent) Run(ctx context.Context, input *adk.AgentInput, opts ...adk.RunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	it := adk.NewAsyncIterator[*adk.AgentEvent](4)
	go func() {
		defer it.Close()
		if a == nil || a.lead == nil {
			it.Send(&adk.AgentEvent{Kind: adk.EventError, Err: fmt.Errorf("deep: nil agent")})
			return
		}
		transfer, err := adk.NewDeterministicTransferAgent(a.lead, a.subAgents, a.maxTasks)
		if err != nil {
			it.Send(&adk.AgentEvent{Kind: adk.EventError, Err: err})
			return
		}
		subIt := transfer.Run(ctx, input, opts...)
		for {
			ev, ok := subIt.Next()
			if !ok {
				break
			}
			it.Send(ev)
			if ev != nil && ev.Err != nil {
				return
			}
		}
		it.Send(&adk.AgentEvent{Kind: adk.EventDone})
	}()
	return it
}

// DelegateTask runs an explicit sub-task via TaskTool.
func (a *Agent) DelegateTask(ctx context.Context, taskName string, messages []*schema.Message) (*schema.Message, error) {
	if a == nil || a.taskTool == nil {
		return nil, fmt.Errorf("deep: no task tool")
	}
	return a.taskTool.Run(ctx, taskName, messages)
}
