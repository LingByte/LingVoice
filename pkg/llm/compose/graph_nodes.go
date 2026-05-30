package compose

import (
	"context"
	"fmt"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
)

// AddChatModelNode registers a ChatModel lambda node (Eino AddChatModelNode subset).
func (g *Graph) AddChatModelNode(name string, model llm.ChatModel, opts ...llm.Option) error {
	if model == nil {
		return fmt.Errorf("compose: nil ChatModel for node %q", name)
	}
	baseOpts := append([]llm.Option(nil), opts...)
	registerGraphChatModel(g, name, model, baseOpts)
	if err := g.AddLambdaNode(name, func(ctx context.Context, st *GraphState) error {
		callOpts := mergeModelOpts(baseOpts, chatModelOptsFromContext(ctx))
		start := time.Now()
		out, err := model.Generate(ctx, st.Messages, callOpts...)
		recordGraphModelRun(st, model, st.Messages, out, start, err)
		if err != nil {
			return err
		}
		st.LastOutput = out
		if out != nil {
			st.Messages = append(st.Messages, out)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func registerGraphChatModel(g *Graph, name string, model llm.ChatModel, opts []llm.Option) {
	if g == nil || model == nil || name == "" {
		return
	}
	if g.chatModels == nil {
		g.chatModels = map[string]llm.ChatModel{}
	}
	if g.chatModelOpts == nil {
		g.chatModelOpts = map[string][]llm.Option{}
	}
	g.chatModels[name] = model
	g.chatModelOpts[name] = append([]llm.Option(nil), opts...)
	g.markNodeKind(name, NodeKindChatModel)
}

// AddTemplateNode renders a ChatTemplate into graph state messages.
func (g *Graph) AddTemplateNode(name string, tpl *prompt.ChatTemplate) error {
	if tpl == nil {
		return fmt.Errorf("compose: nil template for node %q", name)
	}
	if err := g.AddLambdaNode(name, func(ctx context.Context, st *GraphState) error {
		vars := st.Vars
		if vars == nil {
			vars = map[string]any{}
		}
		msgs, err := tpl.Format(ctx, vars)
		if err != nil {
			return fmt.Errorf("compose: template node %q: %w", name, err)
		}
		st.Messages = append(st.Messages, msgs...)
		return nil
	}); err != nil {
		return err
	}
	g.markNodeKind(name, NodeKindTemplate)
	return nil
}

// AddToolsNode executes tool calls on the last assistant message.
func (g *Graph) AddToolsNode(ctx context.Context, name string, cfg *ToolNodeConfig) error {
	if cfg == nil {
		return fmt.Errorf("compose: nil ToolNodeConfig for node %q", name)
	}
	node, err := NewToolNode(ctx, cfg)
	if err != nil {
		return err
	}
	if err := g.AddLambdaNode(name, func(ctx context.Context, st *GraphState) error {
		if st.LastOutput == nil || len(st.LastOutput.ToolCalls) == 0 {
			return nil
		}
		toolMsgs, err := node.Invoke(ctx, st.LastOutput)
		if err != nil {
			return err
		}
		st.Messages = append(st.Messages, toolMsgs...)
		return nil
	}); err != nil {
		return err
	}
	g.markNodeKind(name, NodeKindTools)
	return nil
}

// AddToolCallingModelNode binds tools then runs Generate (single-turn tool model node).
func (g *Graph) AddToolCallingModelNode(ctx context.Context, name string, model llm.ToolCallingChatModel, tools []tool.InvokableTool, opts ...llm.Option) error {
	if model == nil {
		return fmt.Errorf("compose: nil ToolCallingChatModel for node %q", name)
	}
	infos, err := tool.CollectInfos(ctx, tools)
	if err != nil {
		return err
	}
	bound, err := model.WithTools(infos)
	if err != nil {
		return err
	}
	return g.AddChatModelNode(name, bound, opts...)
}
