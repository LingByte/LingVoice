package compose

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// GenericChain runs typed sequential transforms (Eino Generic Chain[I,O] subset).
type GenericChain[I, O any] struct {
	name   string
	steps  []func(context.Context, I) (I, error)
	toOut  func(I) O
}

// NewGenericChain builds a chain with a final output mapper.
func NewGenericChain[I, O any](name string, toOut func(I) O) *GenericChain[I, O] {
	return &GenericChain[I, O]{name: name, toOut: toOut}
}

// NewGenericChainIO builds a chain where input and output share the same type.
func NewGenericChainIO[T any](name string) *GenericChain[T, T] {
	return &GenericChain[T, T]{
		name:  name,
		toOut: func(v T) T { return v },
	}
}

// Append adds a transform step (I → I).
func (c *GenericChain[I, O]) Append(fn func(context.Context, I) (I, error)) *GenericChain[I, O] {
	if c != nil && fn != nil {
		c.steps = append(c.steps, fn)
	}
	return c
}

// Invoke runs all steps and maps the final value to O.
func (c *GenericChain[I, O]) Invoke(ctx context.Context, input I) (O, error) {
	if c == nil {
		var zero O
		return zero, fmt.Errorf("compose: nil generic chain")
	}
	if c.toOut == nil {
		var zero O
		return zero, fmt.Errorf("compose: generic chain %q missing output mapper", c.name)
	}
	ctx = callback.InitRun(ctx, &callback.RunInfo{Name: c.name, Component: callback.ComponentChain})
	cur := input
	for i, fn := range c.steps {
		next, err := fn(ctx, cur)
		if err != nil {
			return c.toOut(cur), fmt.Errorf("compose: generic chain %q step %d: %w", c.name, i, err)
		}
		cur = next
	}
	return c.toOut(cur), nil
}

// MessageGenericChain wraps message-oriented chain steps with typed I/O.
type MessageGenericChain struct {
	name  string
	steps []Step
}

// NewMessageGenericChain builds a chain over []*schema.Message → *schema.Message.
func NewMessageGenericChain(name string, steps ...Step) *MessageGenericChain {
	return &MessageGenericChain{name: name, steps: steps}
}

// Invoke executes chain steps and returns the last assistant output.
func (c *MessageGenericChain) Invoke(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
	if c == nil {
		return nil, fmt.Errorf("compose: nil message generic chain")
	}
	chain := NewChain(c.name, c.steps...)
	st, err := chain.Invoke(ctx, input)
	if err != nil {
		return nil, err
	}
	if st != nil && st.LastOutput != nil {
		return st.LastOutput, nil
	}
	return FinalMessage(chainStateToGraph(st)), nil
}

func chainStateToGraph(st *State) *GraphState {
	if st == nil {
		return nil
	}
	return &GraphState{
		Messages:   st.Messages,
		LastOutput: st.LastOutput,
		Vars:       st.Vars,
	}
}

// MappedChain runs chain steps with custom typed input/output mappers.
type MappedChain[I, O any] struct {
	name      string
	steps     []Step
	fromInput func(context.Context, I) (*State, error)
	toOutput  func(*State) (O, error)
}

// NewMappedChain builds a typed chain over existing compose Steps.
func NewMappedChain[I, O any](
	name string,
	fromInput func(context.Context, I) (*State, error),
	toOutput func(*State) (O, error),
	steps ...Step,
) *MappedChain[I, O] {
	return &MappedChain[I, O]{
		name:      name,
		steps:     steps,
		fromInput: fromInput,
		toOutput:  toOutput,
	}
}

// Invoke executes the mapped chain.
func (c *MappedChain[I, O]) Invoke(ctx context.Context, input I) (O, error) {
	var zero O
	if c == nil || c.fromInput == nil || c.toOutput == nil {
		return zero, fmt.Errorf("compose: nil mapped chain")
	}
	st, err := c.fromInput(ctx, input)
	if err != nil {
		return zero, err
	}
	ctx = callback.InitRun(ctx, &callback.RunInfo{Name: c.name, Component: callback.ComponentChain})
	for i, step := range c.steps {
		if step == nil {
			return zero, fmt.Errorf("compose: mapped chain %q step %d is nil", c.name, i)
		}
		if err := step.Run(ctx, st); err != nil {
			return zero, fmt.Errorf("compose: mapped chain %q step %d: %w", c.name, i, err)
		}
	}
	return c.toOutput(st)
}

// NewStringMessageChain maps string prompts through message chain steps.
func NewStringMessageChain(name string, steps ...Step) *MappedChain[string, string] {
	return NewMappedChain(name,
		func(_ context.Context, prompt string) (*State, error) {
			return &State{
				Messages: []*schema.Message{schema.UserMessage(prompt)},
				Vars:     map[string]any{},
			}, nil
		},
		func(st *State) (string, error) {
			if st == nil || st.LastOutput == nil {
				return "", fmt.Errorf("compose: empty chain output")
			}
			return st.LastOutput.PlainText(), nil
		},
		steps...,
	)
}
