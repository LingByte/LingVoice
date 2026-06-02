package adk

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// TransferAction routes control to another agent (Eino deterministic transfer subset).
const TransferAction = "transfer"

// TransferDataKey is the payload key for transfer target agent name.
const TransferDataKey = "target_agent"

// MakeTransferEvent builds a transfer action event.
func MakeTransferEvent(target string) *AgentEvent {
	return &AgentEvent{
		Kind:   EventAction,
		Action: TransferAction,
		Data:   map[string]any{TransferDataKey: target},
	}
}

// TransferTarget reads the target agent from a transfer event.
func TransferTarget(ev *AgentEvent) string {
	if ev == nil || ev.Data == nil {
		return ""
	}
	s, _ := ev.Data[TransferDataKey].(string)
	return s
}

// DeterministicTransferAgent runs one agent and follows transfer actions to sub-agents.
type DeterministicTransferAgent struct {
	Root      Agent
	SubAgents map[string]Agent
	MaxHops   int
}

// NewDeterministicTransferAgent builds a transfer-routing agent.
func NewDeterministicTransferAgent(root Agent, subs map[string]Agent, maxHops int) (*DeterministicTransferAgent, error) {
	if root == nil {
		return nil, fmt.Errorf("adk: nil root agent")
	}
	if maxHops <= 0 {
		maxHops = 8
	}
	return &DeterministicTransferAgent{Root: root, SubAgents: subs, MaxHops: maxHops}, nil
}

func (d *DeterministicTransferAgent) Name(ctx context.Context) string {
	if d == nil || d.Root == nil {
		return "transfer"
	}
	return d.Root.Name(ctx)
}

func (d *DeterministicTransferAgent) Description(ctx context.Context) string {
	if d == nil || d.Root == nil {
		return ""
	}
	return d.Root.Description(ctx)
}

func (d *DeterministicTransferAgent) Run(ctx context.Context, input *AgentInput, opts ...RunOption) *AsyncIterator[*AgentEvent] {
	it := NewAsyncIterator[*AgentEvent](4)
	go func() {
		defer it.Close()
		if d == nil || d.Root == nil {
			it.Send(&AgentEvent{Kind: EventError, Err: fmt.Errorf("adk: nil transfer agent")})
			return
		}
		current := d.Root
		msgs := append([]*schema.Message(nil), MessagesFromInput(input)...)
		for hop := 0; hop < d.MaxHops; hop++ {
			subIt := current.Run(ctx, &AgentInput{Messages: msgs}, opts...)
			var transferTo string
			for {
				ev, ok := subIt.Next()
				if !ok {
					break
				}
				if ev == nil {
					continue
				}
				if ev.Kind == EventAction && ev.Action == TransferAction {
					transferTo = TransferTarget(ev)
					it.Send(ev)
					continue
				}
				it.Send(ev)
				if ev.Message != nil {
					msgs = append(msgs, ev.Message)
				}
				if ev.Err != nil {
					return
				}
			}
			if transferTo == "" {
				it.Send(&AgentEvent{Kind: EventDone})
				return
			}
			next := LookupSubAgent(d.SubAgents, transferTo, "")
			if next == nil {
				it.Send(&AgentEvent{Kind: EventError, Err: fmt.Errorf("adk: unknown transfer target %q", transferTo)})
				return
			}
			current = next
		}
		it.Send(&AgentEvent{Kind: EventError, Err: fmt.Errorf("adk: transfer exceeded max hops (%d)", d.MaxHops)})
	}()
	return it
}
