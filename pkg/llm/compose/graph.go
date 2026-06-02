package compose

import (
	"context"
	"fmt"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// Graph node names (Eino react graph subset).
const (
	NodeStart     = "__start__"
	NodeEnd       = "__end__"
	NodeChatModel = "chat_model"
	NodeTools     = "tools"
)

// GraphStep records one node transition for observability.
type GraphStep struct {
	Node      string    `json:"node"`
	StartedAt time.Time `json:"started_at"`
	Duration  string    `json:"duration"`
}

// ReActGraphConfig configures a chat ↔ tools cyclic graph (Eino react.Agent graph subset).
type ReActGraphConfig struct {
	Model              llm.ToolCallingChatModel
	Tools              []tool.InvokableTool
	ToolNode           *ToolNode
	MaxSteps           int
	ModelOpts          []llm.Option
	MessageModifier    MessageModifier
	ToolReturnDirectly map[string]struct{}
	ToolNodeConfig     *ToolNodeConfig
	// OnStep is called after each node (optional tracing).
	OnStep func(step GraphStep)
}

// ReActGraph executes model/tools cycles with explicit node semantics.
type ReActGraph struct {
	model              llm.ToolCallingChatModel
	toolNode           *ToolNode
	tools              []tool.InvokableTool
	maxSteps           int
	modelOpts          []llm.Option
	messageModifier    MessageModifier
	toolReturnDirectly map[string]struct{}
	onStep             func(step GraphStep)
}

// NewReActGraph builds a compiled react-style graph executor.
func NewReActGraph(ctx context.Context, cfg ReActGraphConfig) (*ReActGraph, error) {
	if cfg.Model == nil {
		return nil, fmt.Errorf("compose: nil ToolCallingChatModel")
	}
	node := cfg.ToolNode
	if node == nil {
		nodeCfg := cfg.ToolNodeConfig
		if nodeCfg == nil {
			nodeCfg = &ToolNodeConfig{Tools: cfg.Tools}
		} else if len(nodeCfg.Tools) == 0 {
			nodeCfg.Tools = cfg.Tools
		}
		var err error
		node, err = NewToolNode(ctx, nodeCfg)
		if err != nil {
			return nil, err
		}
	}
	max := cfg.MaxSteps
	if max <= 0 {
		max = defaultMaxToolRounds
	}
	return &ReActGraph{
		model:              cfg.Model,
		toolNode:           node,
		tools:              cfg.Tools,
		maxSteps:           max,
		modelOpts:          cfg.ModelOpts,
		messageModifier:    cfg.MessageModifier,
		toolReturnDirectly: cfg.ToolReturnDirectly,
		onStep:             cfg.OnStep,
	}, nil
}

// Invoke runs START → chat_model → (tools → chat_model)* → END.
func (g *ReActGraph) Invoke(ctx context.Context, messages []*schema.Message) (*schema.Message, []GraphStep, error) {
	if g == nil {
		return nil, nil, fmt.Errorf("compose: nil ReActGraph")
	}
	ctx = callback.InitRun(ctx, &callback.RunInfo{Name: "ReActGraph", Component: callback.ComponentGraph})

	infos, err := tool.CollectInfos(ctx, g.tools)
	if err != nil {
		return nil, nil, err
	}
	model, err := g.model.WithTools(infos)
	if err != nil {
		return nil, nil, err
	}

	msgs := append([]*schema.Message(nil), messages...)
	var trace []GraphStep
	current := NodeChatModel

	for step := 0; step < g.maxSteps; step++ {
		switch current {
		case NodeChatModel:
			start := time.Now()
			callMsgs := g.applyModifier(ctx, msgs)
			out, err := model.Generate(ctx, callMsgs, g.modelOpts...)
			g.recordStep(&trace, NodeChatModel, start)
			if err != nil {
				return nil, trace, fmt.Errorf("compose: graph node %s: %w", NodeChatModel, err)
			}
			msgs = append(msgs, out)
			if out == nil || len(out.ToolCalls) == 0 {
				return out, trace, nil
			}
			if _, ok := g.shouldReturnDirectly(out); ok {
				return out, trace, nil
			}
			current = NodeTools

		case NodeTools:
			start := time.Now()
			assistant := msgs[len(msgs)-1]
			toolMsgs, err := g.toolNode.Invoke(ctx, assistant)
			g.recordStep(&trace, NodeTools, start)
			if err != nil {
				return assistant, trace, fmt.Errorf("compose: graph node %s: %w", NodeTools, err)
			}
			msgs = append(msgs, toolMsgs...)
			current = NodeChatModel

		default:
			return nil, trace, fmt.Errorf("compose: unknown graph node %q", current)
		}
	}
	return nil, trace, fmt.Errorf("compose: graph exceeded max steps (%d)", g.maxSteps)
}

// Stream delegates to ToolLoop streaming (same graph semantics).
func (g *ReActGraph) Stream(ctx context.Context, messages []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
	loop, err := NewToolLoop(ctx, ToolLoopConfig{
		Model:              g.model,
		Tools:              g.tools,
		MaxRounds:          g.maxSteps,
		ModelOpts:          g.modelOpts,
		MessageModifier:    g.messageModifier,
		ToolReturnDirectly: g.toolReturnDirectly,
		ToolNodeConfig:     &ToolNodeConfig{Tools: g.tools},
	})
	if err != nil {
		return nil, err
	}
	return loop.Stream(ctx, messages)
}

func (g *ReActGraph) recordStep(trace *[]GraphStep, node string, start time.Time) {
	if g.onStep != nil {
		g.onStep(GraphStep{Node: node, StartedAt: start, Duration: time.Since(start).String()})
	}
	if trace != nil {
		*trace = append(*trace, GraphStep{Node: node, StartedAt: start, Duration: time.Since(start).String()})
	}
}

func (g *ReActGraph) applyModifier(ctx context.Context, msgs []*schema.Message) []*schema.Message {
	if g.messageModifier == nil {
		return msgs
	}
	return g.messageModifier(ctx, msgs)
}

func (g *ReActGraph) shouldReturnDirectly(out *schema.Message) (*schema.Message, bool) {
	if len(g.toolReturnDirectly) == 0 || out == nil {
		return nil, false
	}
	for _, tc := range out.ToolCalls {
		if _, ok := g.toolReturnDirectly[tc.Function.Name]; ok {
			return out, true
		}
	}
	return nil, false
}
