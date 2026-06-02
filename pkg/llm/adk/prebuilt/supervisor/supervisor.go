package supervisor

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/llm/adk"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// Config configures a supervisor multi-agent (Eino adk/prebuilt/supervisor subset).
type Config struct {
	Name        string
	Supervisor  adk.Agent
	Workers     map[string]adk.Agent
	DefaultWorker string
}

// Agent supervises workers via transfer actions (Eino supervisor subset).
type Agent struct {
	name          string
	supervisor    adk.Agent
	workers       map[string]adk.Agent
	defaultWorker string
}

// New builds a supervisor agent.
func New(cfg Config) (*Agent, error) {
	if cfg.Supervisor == nil {
		return nil, fmt.Errorf("supervisor: nil supervisor agent")
	}
	workers, err := adk.SetSubAgents(adk.SubAgentsConfig{
		Parent:    cfg.Supervisor,
		SubAgents: cfg.Workers,
	})
	if err != nil {
		return nil, err
	}
	name := cfg.Name
	if name == "" {
		name = "supervisor"
	}
	def := cfg.DefaultWorker
	if def == "" {
		for k := range workers {
			def = k
			break
		}
	}
	return &Agent{
		name:          name,
		supervisor:    cfg.Supervisor,
		workers:       workers,
		defaultWorker: def,
	}, nil
}

func (a *Agent) Name(_ context.Context) string {
	if a == nil || a.name == "" {
		return "supervisor"
	}
	return a.name
}

func (a *Agent) Description(_ context.Context) string {
	return "supervisor routes tasks to worker agents"
}

func (a *Agent) Run(ctx context.Context, input *adk.AgentInput, opts ...adk.RunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	it := adk.NewAsyncIterator[*adk.AgentEvent](4)
	go func() {
		defer it.Close()
		if a == nil || a.supervisor == nil {
			it.Send(&adk.AgentEvent{Kind: adk.EventError, Err: fmt.Errorf("supervisor: nil agent")})
			return
		}
		transfer, err := adk.NewDeterministicTransferAgent(a.supervisor, a.workers, 8)
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
			if ev != nil && ev.Kind == adk.EventError && ev.Err != nil {
				it.Send(ev)
				return
			}
			if ev != nil && ev.Kind == adk.EventAction && ev.Action == adk.TransferAction {
				target := adk.TransferTarget(ev)
				if target == "" {
					target = a.defaultWorker
				}
				if _, ok := a.workers[target]; !ok {
					it.Send(&adk.AgentEvent{
						Kind: adk.EventError,
						Err:  fmt.Errorf("supervisor: unknown worker %q", target),
					})
					return
				}
			}
			it.Send(ev)
		}
		it.Send(&adk.AgentEvent{Kind: adk.EventDone})
	}()
	return it
}

// RunToMessage is a convenience wrapper.
func (a *Agent) RunToMessage(ctx context.Context, messages []*schema.Message, opts ...adk.RunOption) (*schema.Message, error) {
	return adk.RunToMessage(ctx, a, &adk.AgentInput{Messages: messages}, opts...)
}
