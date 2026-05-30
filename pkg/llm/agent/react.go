// Package agent provides high-level agents built on compose (Eino flow/agent/react subset).
package agent

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// ReActConfig configures a ReAct agent (Eino react.AgentConfig subset).
type ReActConfig struct {
	Model              llm.ToolCallingChatModel
	Tools              []tool.InvokableTool
	MaxSteps           int
	ModelOpts          []llm.Option
	MessageModifier    compose.MessageModifier
	ToolReturnDirectly map[string]struct{}
	ToolNodeConfig     *compose.ToolNodeConfig
	ForceToolUse       bool
	CheckPointStore    compose.CheckPointStore
}

// ReActResult is the outcome of a traced agent run.
type ReActResult struct {
	Message    *schema.Message
	Trace      *compose.LoopTrace
	GraphTrace []compose.GraphStep
	Delta      []*schema.Message
	State      *compose.GraphState
}

// ReActAgent runs tool-augmented chat via compiled graph (primary) + ToolLoop (stream).
type ReActAgent struct {
	graph *GraphAgent
	loop  *compose.ToolLoop
}

// NewReActAgent builds an agent from config.
func NewReActAgent(ctx context.Context, cfg ReActConfig) (*ReActAgent, error) {
	nodeCfg := cfg.ToolNodeConfig
	if nodeCfg == nil {
		nodeCfg = &compose.ToolNodeConfig{Tools: cfg.Tools}
	} else if len(nodeCfg.Tools) == 0 {
		nodeCfg.Tools = cfg.Tools
	}
	loopCfg := compose.ToolLoopConfig{
		Model:              cfg.Model,
		Tools:              cfg.Tools,
		MaxRounds:          cfg.MaxSteps,
		ModelOpts:          cfg.ModelOpts,
		MessageModifier:    cfg.MessageModifier,
		ToolReturnDirectly: cfg.ToolReturnDirectly,
		ToolNodeConfig:     nodeCfg,
		ForceToolUse:       cfg.ForceToolUse,
	}
	loop, err := compose.NewToolLoop(ctx, loopCfg)
	if err != nil {
		return nil, err
	}
	ga, err := NewGraphAgent(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &ReActAgent{graph: ga, loop: loop}, nil
}

// Generate runs the agent to completion.
func (a *ReActAgent) Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	r, err := a.GenerateWithTrace(ctx, messages)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return r.Message, nil
}

// GenerateWithTrace runs via compiled graph and returns ReAct-compatible trace.
func (a *ReActAgent) GenerateWithTrace(ctx context.Context, messages []*schema.Message, opts ...compose.GraphInvokeOption) (*ReActResult, error) {
	if a == nil || a.graph == nil {
		return nil, nil
	}
	inputLen := len(messages)
	gr, err := a.graph.GenerateWithTrace(ctx, messages, opts...)
	if gr == nil {
		return nil, err
	}
	trace := compose.LoopTraceFromGraph(gr.Trace, gr.State, inputLen)
	result := &ReActResult{
		Message:    gr.Message,
		Trace:      trace,
		GraphTrace: gr.Trace,
		State:      gr.State,
	}
	if gr.State != nil {
		result.Delta = compose.MessageDelta(gr.State.Messages, inputLen)
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

// Resume continues a checkpointed run (HITL / tool approval).
func (a *ReActAgent) Resume(ctx context.Context, checkpointID string, resume map[string]any) (*ReActResult, error) {
	if a == nil || a.graph == nil {
		return nil, nil
	}
	gr, err := a.graph.Resume(ctx, checkpointID, resume)
	if gr == nil {
		return nil, err
	}
	inputLen := 0
	if gr.State != nil {
		// on resume, delta is full new messages since checkpoint state already includes history
		inputLen = len(gr.State.Messages)
	}
	trace := compose.LoopTraceFromGraph(gr.Trace, gr.State, inputLen)
	result := &ReActResult{
		Message:    gr.Message,
		Trace:      trace,
		GraphTrace: gr.Trace,
		State:      gr.State,
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

// Stream streams assistant output across tool rounds (graph-native when available).
func (a *ReActAgent) Stream(ctx context.Context, messages []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
	if a != nil && a.graph != nil {
		if sr, err := a.graph.Stream(ctx, messages); err == nil && sr != nil {
			return sr, nil
		}
	}
	if a == nil || a.loop == nil {
		return nil, nil
	}
	return a.loop.Stream(ctx, messages)
}

// StreamFrames streams with ReAct phase/round metadata (graph-native when available).
func (a *ReActAgent) StreamFrames(ctx context.Context, messages []*schema.Message) (*compose.StreamFrameReader, error) {
	if a != nil && a.graph != nil {
		if sr, err := a.graph.StreamFrames(ctx, messages); err == nil && sr != nil {
			return sr, nil
		}
	}
	if a == nil || a.loop == nil {
		return nil, nil
	}
	return a.loop.StreamFrames(ctx, messages)
}

// CheckPointStore returns the bound checkpoint store if any.
func (a *ReActAgent) CheckPointStore() compose.CheckPointStore {
	if a == nil || a.graph == nil {
		return nil
	}
	return a.graph.store
}

// GraphAgent returns the underlying compiled-graph agent.
func (a *ReActAgent) GraphAgent() *GraphAgent {
	if a == nil {
		return nil
	}
	return a.graph
}
