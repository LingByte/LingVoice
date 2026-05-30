package agent

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// Runner executes a compiled graph with checkpoint support (Eino adk.Runner subset).
type Runner struct {
	Graph *compose.CompiledGraph
	Store compose.CheckPointStore
}

// RunnerConfig configures a graph runner.
type RunnerConfig struct {
	Graph           *compose.CompiledGraph
	CheckPointStore compose.CheckPointStore
}

// NewRunner builds a Runner.
func NewRunner(_ context.Context, cfg RunnerConfig) (*Runner, error) {
	if cfg.Graph == nil {
		return nil, composeErr("nil graph")
	}
	store := cfg.CheckPointStore
	if store == nil {
		store = compose.NewMemoryCheckPointStore()
	}
	return &Runner{Graph: cfg.Graph, Store: store}, nil
}

// Invoke runs the graph with an optional checkpoint id.
func (r *Runner) Invoke(ctx context.Context, messages []*schema.Message, checkpointID string, opts ...compose.GraphInvokeOption) (*compose.GraphState, []compose.GraphStep, error) {
	if r == nil || r.Graph == nil {
		return nil, nil, composeErr("nil runner")
	}
	all := append([]compose.GraphInvokeOption{}, opts...)
	if checkpointID != "" {
		all = append(all, compose.WithCheckPointID(checkpointID))
	}
	return r.Graph.Invoke(ctx, messages, all...)
}

// Resume continues from a saved checkpoint (Eino Runner.ResumeWithParams subset).
func (r *Runner) Resume(ctx context.Context, checkpointID string, resumeData map[string]any, opts ...compose.GraphInvokeOption) (*compose.GraphState, []compose.GraphStep, error) {
	if r == nil || r.Graph == nil {
		return nil, nil, composeErr("nil runner")
	}
	all := append([]compose.GraphInvokeOption{
		compose.WithCheckPointID(checkpointID),
		compose.WithResumeData(resumeData),
	}, opts...)
	return r.Graph.Invoke(ctx, nil, all...)
}

// Stream runs the graph with token streaming when the graph supports native react streaming.
func (r *Runner) Stream(ctx context.Context, messages []*schema.Message, checkpointID string, opts ...compose.GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	if r == nil || r.Graph == nil {
		return nil, composeErr("nil runner")
	}
	all := append([]compose.GraphInvokeOption{}, opts...)
	if checkpointID != "" {
		all = append(all, compose.WithCheckPointID(checkpointID))
	}
	return r.Graph.Stream(ctx, messages, all...)
}

// StreamFrames runs frame-level streaming when the graph has ReAct runtime.
func (r *Runner) StreamFrames(ctx context.Context, messages []*schema.Message, checkpointID string, opts ...compose.GraphInvokeOption) (*compose.StreamFrameReader, error) {
	if r == nil || r.Graph == nil {
		return nil, composeErr("nil runner")
	}
	if !r.Graph.HasReActRuntime() {
		return nil, composeErr("graph has no ReAct stream runtime")
	}
	all := append([]compose.GraphInvokeOption{}, opts...)
	if checkpointID != "" {
		all = append(all, compose.WithCheckPointID(checkpointID))
	}
	return r.Graph.StreamFrames(ctx, messages, all...)
}

func composeErr(msg string) error {
	return &runnerError{msg: msg}
}

type runnerError struct{ msg string }

func (e *runnerError) Error() string { return "agent: " + e.msg }
