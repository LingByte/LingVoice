package adk

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// GenerateAgent is the minimal generate-capable agent bridged into ADK.
type GenerateAgent interface {
	Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error)
}

// GenerateAdapter wraps GenerateAgent as an ADK Agent.
type GenerateAdapter struct {
	AgentName        string
	AgentDescription string
	Inner            GenerateAgent
}

// NewGenerateAdapter builds an ADK agent from a GenerateAgent.
func NewGenerateAdapter(name, desc string, inner GenerateAgent) *GenerateAdapter {
	return &GenerateAdapter{AgentName: name, AgentDescription: desc, Inner: inner}
}

func (a *GenerateAdapter) Name(_ context.Context) string {
	if a == nil || a.AgentName == "" {
		return "agent"
	}
	return a.AgentName
}

func (a *GenerateAdapter) Description(_ context.Context) string {
	if a == nil {
		return ""
	}
	return a.AgentDescription
}

func (a *GenerateAdapter) Run(ctx context.Context, input *AgentInput, _ ...RunOption) *AsyncIterator[*AgentEvent] {
	it := NewAsyncIterator[*AgentEvent](2)
	go func() {
		defer it.Close()
		if a == nil || a.Inner == nil {
			it.Send(&AgentEvent{Kind: EventError, Err: fmt.Errorf("adk: nil generate adapter")})
			return
		}
		msg, err := a.Inner.Generate(ctx, MessagesFromInput(input))
		if err != nil {
			it.Send(&AgentEvent{Kind: EventError, Err: err})
			return
		}
		it.Send(&AgentEvent{Kind: EventMessage, Message: msg})
		it.Send(&AgentEvent{Kind: EventDone})
	}()
	return it
}

// RunToMessage drains Run and returns the final message or error.
func RunToMessage(ctx context.Context, ag Agent, input *AgentInput, opts ...RunOption) (*schema.Message, error) {
	if ag == nil {
		return nil, fmt.Errorf("adk: nil agent")
	}
	it := ag.Run(ctx, input, opts...)
	var lastErr error
	var lastMsg *schema.Message
	for {
		ev, ok := it.Next()
		if !ok {
			break
		}
		if ev == nil {
			continue
		}
		if ev.Err != nil {
			lastErr = ev.Err
		}
		if ev.Message != nil {
			lastMsg = ev.Message
		}
	}
	if lastErr != nil {
		return lastMsg, lastErr
	}
	return lastMsg, nil
}
