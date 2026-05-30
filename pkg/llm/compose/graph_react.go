package compose

import (
	"context"
	"fmt"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// ReActCompileConfig builds an Eino-style compiled ReAct graph.
type ReActCompileConfig struct {
	Name               string
	Model              llm.ToolCallingChatModel
	Tools              []tool.InvokableTool
	ToolNode           *ToolNode
	ToolNodeConfig     *ToolNodeConfig
	MaxSteps           int
	ModelOpts          []llm.Option
	MessageModifier    MessageModifier
	ToolReturnDirectly map[string]struct{}
	ForceToolUse       bool
	CompileOpts        []GraphCompileOption
	CheckPointStore    CheckPointStore
}

// CompileReActGraph builds chat_model ↔ tools graph with branches (Eino react.NewAgent).
func CompileReActGraph(ctx context.Context, cfg ReActCompileConfig) (*CompiledGraph, error) {
	if cfg.Model == nil {
		return nil, fmt.Errorf("compose: nil ToolCallingChatModel")
	}
	name := cfg.Name
	if name == "" {
		name = "react"
	}
	toolNode := cfg.ToolNode
	if toolNode == nil {
		nodeCfg := cfg.ToolNodeConfig
		if nodeCfg == nil {
			nodeCfg = &ToolNodeConfig{Tools: cfg.Tools}
		} else if len(nodeCfg.Tools) == 0 {
			nodeCfg.Tools = cfg.Tools
		}
		var err error
		toolNode, err = NewToolNode(ctx, nodeCfg)
		if err != nil {
			return nil, err
		}
	}

	infos, err := tool.CollectInfos(ctx, cfg.Tools)
	if err != nil {
		return nil, err
	}
	bound, err := cfg.Model.WithTools(infos)
	if err != nil {
		return nil, err
	}

	max := cfg.MaxSteps
	if max <= 0 {
		max = defaultMaxToolRounds
	}
	g := NewGraph(name).WithMaxSteps(max * 2)

	if err := g.AddLambdaNode(NodeChatModel, func(ctx context.Context, st *GraphState) error {
		msgs := st.Messages
		if cfg.MessageModifier != nil {
			msgs = cfg.MessageModifier(ctx, msgs)
		}
		opts := mergeModelOpts(cfg.ModelOpts, chatModelOptsFromContext(ctx))
		if cfg.ForceToolUse {
			round, _ := st.Vars["__model_round"].(int)
			if round == 0 {
				opts = roundOptsCopy(opts, true)
			}
			st.Vars["__model_round"] = round + 1
		}
		start := time.Now()
		out, err := bound.Generate(ctx, msgs, opts...)
		recordGraphModelRun(st, bound, msgs, out, start, err)
		if err != nil {
			return err
		}
		st.LastOutput = out
		st.Messages = append(st.Messages, out)
		return nil
	}); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(NodeTools, func(ctx context.Context, st *GraphState) error {
		if st.LastOutput == nil || len(st.LastOutput.ToolCalls) == 0 {
			return nil
		}
		toolMsgs, err := toolNode.Invoke(ctx, st.LastOutput)
		if err != nil {
			return err
		}
		st.Messages = append(st.Messages, toolMsgs...)
		return nil
	}); err != nil {
		return nil, err
	}

	if err := g.AddEdge(START, NodeChatModel); err != nil {
		return nil, err
	}
	if err := g.AddBranch(NodeChatModel, func(_ context.Context, st *GraphState) (string, error) {
		out := st.LastOutput
		if out == nil || len(out.ToolCalls) == 0 {
			return END, nil
		}
		if len(cfg.ToolReturnDirectly) > 0 {
			for _, tc := range out.ToolCalls {
				if _, ok := cfg.ToolReturnDirectly[tc.Function.Name]; ok {
					return END, nil
				}
			}
		}
		return NodeTools, nil
	}, map[string]bool{NodeTools: true, END: true}); err != nil {
		return nil, err
	}
	if err := g.AddEdge(NodeTools, NodeChatModel); err != nil {
		return nil, err
	}
	g.markNodeKind(NodeChatModel, NodeKindChatModel)
	g.markNodeKind(NodeTools, NodeKindTools)
	compileOpts := append([]GraphCompileOption{}, cfg.CompileOpts...)
	if cfg.CheckPointStore != nil {
		compileOpts = append(compileOpts, WithCheckPointStore(cfg.CheckPointStore))
	}
	compiled, err := g.Compile(compileOpts...)
	if err != nil {
		return nil, err
	}
	attachReActRuntime(compiled, cfg, bound, toolNode)
	return compiled, nil
}

func roundOptsCopy(base []llm.Option, force bool) []llm.Option {
	if !force {
		return base
	}
	out := make([]llm.Option, len(base), len(base)+1)
	copy(out, base)
	return append(out, llm.WithToolChoice(llm.ToolChoiceForced))
}

// FinalMessage returns the last assistant answer from graph state.
func FinalMessage(st *GraphState) *schema.Message {
	if st == nil {
		return nil
	}
	if st.LastOutput != nil && len(st.LastOutput.ToolCalls) == 0 {
		return st.LastOutput
	}
	for i := len(st.Messages) - 1; i >= 0; i-- {
		m := st.Messages[i]
		if m != nil && m.Role == schema.Assistant && len(m.ToolCalls) == 0 && m.Content != "" {
			return m
		}
	}
	return st.LastOutput
}
