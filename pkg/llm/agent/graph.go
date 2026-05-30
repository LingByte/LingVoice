package agent

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// GraphAgent runs a compiled ReAct graph (Eino react.Agent via compose.Graph).
type GraphAgent struct {
	graph *compose.CompiledGraph
	store compose.CheckPointStore
}

// NewGraphAgent compiles and returns a graph-based ReAct agent.
func NewGraphAgent(ctx context.Context, cfg ReActConfig) (*GraphAgent, error) {
	g, err := compose.CompileReActGraph(ctx, compose.ReActCompileConfig{
		Name:               "react-graph",
		Model:              cfg.Model,
		Tools:              cfg.Tools,
		ToolNodeConfig:     cfg.ToolNodeConfig,
		MaxSteps:           cfg.MaxSteps,
		ModelOpts:          cfg.ModelOpts,
		MessageModifier:    cfg.MessageModifier,
		ToolReturnDirectly: cfg.ToolReturnDirectly,
		ForceToolUse:       cfg.ForceToolUse,
		CheckPointStore:    cfg.CheckPointStore,
	})
	if err != nil {
		return nil, err
	}
	return &GraphAgent{graph: g, store: cfg.CheckPointStore}, nil
}

// Generate runs the compiled graph.
func (a *GraphAgent) Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	r, err := a.GenerateWithTrace(ctx, messages)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return r.Message, nil
}

// GraphResult holds graph execution output.
type GraphResult struct {
	Message *schema.Message
	Trace   []compose.GraphStep
	State   *compose.GraphState
}

// GenerateWithTrace runs the graph and returns node trace.
func (a *GraphAgent) GenerateWithTrace(ctx context.Context, messages []*schema.Message, opts ...compose.GraphInvokeOption) (*GraphResult, error) {
	if a == nil || a.graph == nil {
		return nil, nil
	}
	st, trace, err := a.graph.Invoke(ctx, messages, opts...)
	if err != nil {
		return &GraphResult{State: st, Trace: trace}, err
	}
	return &GraphResult{
		Message: compose.FinalMessage(st),
		Trace:   trace,
		State:   st,
	}, nil
}

// Resume continues a checkpointed graph run with resume data.
func (a *GraphAgent) Resume(ctx context.Context, checkpointID string, resume map[string]any) (*GraphResult, error) {
	return a.GenerateWithTrace(ctx, nil,
		compose.WithCheckPointID(checkpointID),
		compose.WithResumeData(resume),
	)
}

// Stream streams model tokens via compiled graph native streaming.
func (a *GraphAgent) Stream(ctx context.Context, messages []*schema.Message, opts ...compose.GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	if a == nil || a.graph == nil {
		return nil, nil
	}
	return a.graph.Stream(ctx, messages, opts...)
}

// StreamFrames streams node-level frames via compiled graph.
func (a *GraphAgent) StreamFrames(ctx context.Context, messages []*schema.Message, opts ...compose.GraphInvokeOption) (*compose.StreamFrameReader, error) {
	if a == nil || a.graph == nil {
		return nil, nil
	}
	return a.graph.StreamFrames(ctx, messages, opts...)
}
