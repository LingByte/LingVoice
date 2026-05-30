package adk

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// SequentialAgent runs sub-agents in order (Eino adk.NewSequentialAgent subset).
type SequentialAgent struct {
	AgentName string
	Steps     []Agent
}

// NewSequentialAgent builds a sequential workflow agent.
func NewSequentialAgent(name string, steps ...Agent) (*SequentialAgent, error) {
	if len(steps) == 0 {
		return nil, fmt.Errorf("adk: sequential agent requires steps")
	}
	for i, s := range steps {
		if s == nil {
			return nil, fmt.Errorf("adk: nil step %d", i)
		}
	}
	if name == "" {
		name = "sequential"
	}
	return &SequentialAgent{AgentName: name, Steps: steps}, nil
}

func (s *SequentialAgent) Name(_ context.Context) string {
	if s == nil || s.AgentName == "" {
		return "sequential"
	}
	return s.AgentName
}

func (s *SequentialAgent) Description(_ context.Context) string {
	return "runs sub-agents sequentially"
}

func (s *SequentialAgent) Run(ctx context.Context, input *AgentInput, opts ...RunOption) *AsyncIterator[*AgentEvent] {
	it := NewAsyncIterator[*AgentEvent](4)
	go func() {
		defer it.Close()
		if s == nil || len(s.Steps) == 0 {
			it.Send(&AgentEvent{Kind: EventError, Err: fmt.Errorf("adk: nil sequential agent")})
			return
		}
		msgs := append([]*schema.Message(nil), MessagesFromInput(input)...)
		for _, step := range s.Steps {
			subIt := step.Run(ctx, &AgentInput{Messages: msgs}, opts...)
			for {
				ev, ok := subIt.Next()
				if !ok {
					break
				}
				it.Send(ev)
				if ev != nil && ev.Message != nil {
					msgs = append(msgs, ev.Message)
				}
				if ev != nil && ev.Err != nil {
					return
				}
			}
		}
		it.Send(&AgentEvent{Kind: EventDone})
	}()
	return it
}

// LoopAgent repeats a body agent until done or max iterations (Eino adk.NewLoopAgent subset).
type LoopAgent struct {
	AgentName   string
	Body        Agent
	MaxIter     int
	ShouldStop  func(ctx context.Context, msgs []*schema.Message) bool
}

// NewLoopAgent builds a loop workflow agent.
func NewLoopAgent(name string, body Agent, maxIter int, shouldStop func(context.Context, []*schema.Message) bool) (*LoopAgent, error) {
	if body == nil {
		return nil, fmt.Errorf("adk: loop body required")
	}
	if maxIter <= 0 {
		maxIter = 16
	}
	if name == "" {
		name = "loop"
	}
	return &LoopAgent{AgentName: name, Body: body, MaxIter: maxIter, ShouldStop: shouldStop}, nil
}

func (l *LoopAgent) Name(_ context.Context) string {
	if l == nil || l.AgentName == "" {
		return "loop"
	}
	return l.AgentName
}

func (l *LoopAgent) Description(_ context.Context) string {
	return "repeats a sub-agent until stop condition"
}

func (l *LoopAgent) Run(ctx context.Context, input *AgentInput, opts ...RunOption) *AsyncIterator[*AgentEvent] {
	it := NewAsyncIterator[*AgentEvent](4)
	go func() {
		defer it.Close()
		if l == nil || l.Body == nil {
			it.Send(&AgentEvent{Kind: EventError, Err: fmt.Errorf("adk: nil loop agent")})
			return
		}
		msgs := append([]*schema.Message(nil), MessagesFromInput(input)...)
		for i := 0; i < l.MaxIter; i++ {
			if l.ShouldStop != nil && l.ShouldStop(ctx, msgs) {
				break
			}
			subIt := l.Body.Run(ctx, &AgentInput{Messages: msgs}, opts...)
			for {
				ev, ok := subIt.Next()
				if !ok {
					break
				}
				it.Send(ev)
				if ev != nil && ev.Message != nil {
					msgs = append(msgs, ev.Message)
				}
				if ev != nil && ev.Err != nil {
					return
				}
			}
		}
		it.Send(&AgentEvent{Kind: EventDone})
	}()
	return it
}
