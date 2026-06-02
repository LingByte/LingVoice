package llm

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"
)

// Tool defines the interface for tools that agents can use
type Tool interface {
	// GetName returns the tool name
	GetName() string

	// GetDescription returns the tool description
	GetDescription() string

	// GetInputSchema returns the input schema
	GetInputSchema() map[string]interface{}

	// Execute executes the tool
	Execute(ctx context.Context, input map[string]interface{}) (interface{}, error)
}

// SimpleTool is a basic tool implementation
type SimpleTool struct {
	name        string
	description string
	schema      map[string]interface{}
	handler     func(context.Context, map[string]interface{}) (interface{}, error)
}

// NewSimpleTool creates a new simple tool
func NewSimpleTool(
	name string,
	description string,
	schema map[string]interface{},
	handler func(context.Context, map[string]interface{}) (interface{}, error),
) *SimpleTool {
	return &SimpleTool{
		name:        name,
		description: description,
		schema:      schema,
		handler:     handler,
	}
}

// GetName returns the tool name
func (st *SimpleTool) GetName() string {
	return st.name
}

// GetDescription returns the tool description
func (st *SimpleTool) GetDescription() string {
	return st.description
}

// GetInputSchema returns the input schema
func (st *SimpleTool) GetInputSchema() map[string]interface{} {
	return st.schema
}

// Execute executes the tool
func (st *SimpleTool) Execute(ctx context.Context, input map[string]interface{}) (interface{}, error) {
	return st.handler(ctx, input)
}

// ToolRegistry manages available tools
type ToolRegistry struct {
	tools map[string]Tool
	mu    sync.RWMutex
}

// NewToolRegistry creates a new tool registry
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// Register registers a tool
func (tr *ToolRegistry) Register(tool Tool) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if tool.GetName() == "" {
		return fmt.Errorf("tool name cannot be empty")
	}

	tr.tools[tool.GetName()] = tool
	return nil
}

// Get retrieves a tool by name
func (tr *ToolRegistry) Get(name string) (Tool, error) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	tool, exists := tr.tools[name]
	if !exists {
		return nil, fmt.Errorf("tool %s not found", name)
	}

	return tool, nil
}

// List returns all registered tools
func (tr *ToolRegistry) List() []Tool {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	tools := make([]Tool, 0, len(tr.tools))
	for _, tool := range tr.tools {
		tools = append(tools, tool)
	}

	return tools
}

// Agent represents an AI agent that can use tools
type Agent struct {
	name     string
	llm      LLMModel
	tools    *ToolRegistry
	maxSteps int
	mu       sync.RWMutex
}

// NewAgent creates a new agent
func NewAgent(name string, llm LLMModel, tools *ToolRegistry, maxSteps int) *Agent {
	return &Agent{
		name:     name,
		llm:      llm,
		tools:    tools,
		maxSteps: maxSteps,
	}
}

// AgentAction represents an action the agent wants to take
type AgentAction struct {
	Tool  string
	Input map[string]interface{}
}

// AgentResult represents the result of an agent step
type AgentResult struct {
	Action AgentAction
	Output interface{}
}

// Run runs the agent with the given input
func (a *Agent) Run(ctx context.Context, input string) (string, error) {
	a.mu.RLock()
	llm := a.llm
	tools := a.tools
	maxSteps := a.maxSteps
	a.mu.RUnlock()

	// Build tool descriptions
	toolDescriptions := ""
	for _, tool := range tools.List() {
		toolDescriptions += fmt.Sprintf("- %s: %s\n", tool.GetName(), tool.GetDescription())
	}

	// Build initial prompt
	prompt := fmt.Sprintf(`You are a helpful AI assistant with access to the following tools:

%s

User input: %s

Think step by step and use tools as needed to answer the user's question.`, toolDescriptions, input)

	// Run agent loop
	for step := 0; step < maxSteps; step++ {
		// Get LLM response
		response, err := llm.Generate(ctx, prompt)
		if err != nil {
			return "", fmt.Errorf("llm generation failed: %w", err)
		}

		// Parse response for tool use (simplified)
		// In a real implementation, this would parse structured output
		if response == "" {
			return "", fmt.Errorf("empty response from llm")
		}

		// For now, just return the response
		return response, nil
	}

	return "", fmt.Errorf("agent exceeded max steps")
}

// AgentExecutor manages agent execution
type AgentExecutor struct {
	agent *Agent
	mu    sync.RWMutex
}

// NewAgentExecutor creates a new agent executor
func NewAgentExecutor(agent *Agent) *AgentExecutor {
	return &AgentExecutor{
		agent: agent,
	}
}

// Execute executes the agent
func (ae *AgentExecutor) Execute(ctx context.Context, input string) (string, error) {
	ae.mu.RLock()
	agent := ae.agent
	ae.mu.RUnlock()

	return agent.Run(ctx, input)
}

// AgentStep is a chain step for agent execution
type AgentStep struct {
	name       string
	executor   *AgentExecutor
	inputKey   string
	outputKey  string
}

// NewAgentStep creates a new agent step
func NewAgentStep(name string, executor *AgentExecutor, inputKey, outputKey string) *AgentStep {
	return &AgentStep{
		name:      name,
		executor:  executor,
		inputKey:  inputKey,
		outputKey: outputKey,
	}
}

// Execute executes the agent step
func (as *AgentStep) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	query, ok := input[as.inputKey].(string)
	if !ok {
		return nil, fmt.Errorf("input key %s not found or not a string", as.inputKey)
	}

	result, err := as.executor.Execute(ctx, query)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		as.outputKey: result,
	}, nil
}

// GetInputKeys returns required input keys
func (as *AgentStep) GetInputKeys() []string {
	return []string{as.inputKey}
}

// GetOutputKeys returns produced output keys
func (as *AgentStep) GetOutputKeys() []string {
	return []string{as.outputKey}
}

// GetName returns the step name
func (as *AgentStep) GetName() string {
	return as.name
}
