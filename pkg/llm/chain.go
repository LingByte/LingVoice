package llm

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"
)

// ChainStep represents a single step in a chain
type ChainStep interface {
	// Execute executes the step
	Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error)

	// GetInputKeys returns required input keys
	GetInputKeys() []string

	// GetOutputKeys returns produced output keys
	GetOutputKeys() []string

	// GetName returns the step name
	GetName() string
}

// Chain represents a sequence of steps
type Chain struct {
	name  string
	steps []ChainStep
	mu    sync.RWMutex
}

// NewChain creates a new chain
func NewChain(name string) *Chain {
	return &Chain{
		name:  name,
		steps: make([]ChainStep, 0),
	}
}

// AddStep adds a step to the chain
func (c *Chain) AddStep(step ChainStep) *Chain {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.steps = append(c.steps, step)
	return c
}

// Execute executes the entire chain
func (c *Chain) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	c.mu.RLock()
	steps := make([]ChainStep, len(c.steps))
	copy(steps, c.steps)
	c.mu.RUnlock()

	output := make(map[string]interface{})
	for k, v := range input {
		output[k] = v
	}

	for _, step := range steps {
		result, err := step.Execute(ctx, output)
		if err != nil {
			return nil, fmt.Errorf("chain %s step %s failed: %w", c.name, step.GetName(), err)
		}

		// Merge result into output
		for k, v := range result {
			output[k] = v
		}
	}

	return output, nil
}

// GetName returns the chain name
func (c *Chain) GetName() string {
	return c.name
}

// SimpleStep is a basic chain step implementation
type SimpleStep struct {
	name       string
	inputKeys  []string
	outputKeys []string
	handler    func(context.Context, map[string]interface{}) (map[string]interface{}, error)
}

// NewSimpleStep creates a new simple step
func NewSimpleStep(
	name string,
	inputKeys []string,
	outputKeys []string,
	handler func(context.Context, map[string]interface{}) (map[string]interface{}, error),
) *SimpleStep {
	return &SimpleStep{
		name:       name,
		inputKeys:  inputKeys,
		outputKeys: outputKeys,
		handler:    handler,
	}
}

// Execute executes the step
func (s *SimpleStep) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	// Validate inputs
	for _, key := range s.inputKeys {
		if _, exists := input[key]; !exists {
			return nil, fmt.Errorf("missing required input key: %s", key)
		}
	}

	return s.handler(ctx, input)
}

// GetInputKeys returns required input keys
func (s *SimpleStep) GetInputKeys() []string {
	return s.inputKeys
}

// GetOutputKeys returns produced output keys
func (s *SimpleStep) GetOutputKeys() []string {
	return s.outputKeys
}

// GetName returns the step name
func (s *SimpleStep) GetName() string {
	return s.name
}

// ConditionalStep executes different branches based on condition
type ConditionalStep struct {
	name       string
	condition  func(context.Context, map[string]interface{}) (bool, error)
	trueStep   ChainStep
	falseStep  ChainStep
	inputKeys  []string
	outputKeys []string
}

// NewConditionalStep creates a new conditional step
func NewConditionalStep(
	name string,
	condition func(context.Context, map[string]interface{}) (bool, error),
	trueStep ChainStep,
	falseStep ChainStep,
) *ConditionalStep {
	return &ConditionalStep{
		name:      name,
		condition: condition,
		trueStep:  trueStep,
		falseStep: falseStep,
	}
}

// Execute executes the conditional step
func (cs *ConditionalStep) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	cond, err := cs.condition(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("condition check failed: %w", err)
	}

	if cond {
		return cs.trueStep.Execute(ctx, input)
	}
	return cs.falseStep.Execute(ctx, input)
}

// GetInputKeys returns required input keys
func (cs *ConditionalStep) GetInputKeys() []string {
	return cs.inputKeys
}

// GetOutputKeys returns produced output keys
func (cs *ConditionalStep) GetOutputKeys() []string {
	return cs.outputKeys
}

// GetName returns the step name
func (cs *ConditionalStep) GetName() string {
	return cs.name
}

// LoopStep repeats a step until condition is met
type LoopStep struct {
	name       string
	step       ChainStep
	condition  func(context.Context, map[string]interface{}) (bool, error)
	maxIter    int
	inputKeys  []string
	outputKeys []string
}

// NewLoopStep creates a new loop step
func NewLoopStep(
	name string,
	step ChainStep,
	condition func(context.Context, map[string]interface{}) (bool, error),
	maxIter int,
) *LoopStep {
	return &LoopStep{
		name:      name,
		step:      step,
		condition: condition,
		maxIter:   maxIter,
	}
}

// Execute executes the loop step
func (ls *LoopStep) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	output := make(map[string]interface{})
	for k, v := range input {
		output[k] = v
	}

	for i := 0; i < ls.maxIter; i++ {
		cond, err := ls.condition(ctx, output)
		if err != nil {
			return nil, fmt.Errorf("loop condition check failed: %w", err)
		}

		if !cond {
			break
		}

		result, err := ls.step.Execute(ctx, output)
		if err != nil {
			return nil, fmt.Errorf("loop step failed at iteration %d: %w", i, err)
		}

		for k, v := range result {
			output[k] = v
		}
	}

	return output, nil
}

// GetInputKeys returns required input keys
func (ls *LoopStep) GetInputKeys() []string {
	return ls.inputKeys
}

// GetOutputKeys returns produced output keys
func (ls *LoopStep) GetOutputKeys() []string {
	return ls.outputKeys
}

// GetName returns the step name
func (ls *LoopStep) GetName() string {
	return ls.name
}
