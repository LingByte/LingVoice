package compose

import (
	"context"
	"fmt"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
)

// AddChatModelNode registers a chat model workflow step.
func (w *Workflow) AddChatModelNode(name string, model llm.ChatModel, opts ...llm.Option) error {
	if w == nil {
		return fmt.Errorf("compose: nil workflow")
	}
	baseOpts := append([]llm.Option(nil), opts...)
	return w.AddLambdaStep(name, func(ctx context.Context, st *GraphState) error {
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
	})
}

// AddTemplateNode registers a template workflow step.
func (w *Workflow) AddTemplateNode(name string, tpl *prompt.ChatTemplate) error {
	if w == nil {
		return fmt.Errorf("compose: nil workflow")
	}
	return w.AddLambdaStep(name, func(ctx context.Context, st *GraphState) error {
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
	})
}

// JoinAnyPredecessor marks a workflow node as any-predecessor fan-in.
func (n *WorkflowNode) JoinAnyPredecessor() *WorkflowNode {
	if n != nil && n.wf != nil && n.wf.graph != nil {
		n.wf.graph.MarkAnyPredecessorJoin(n.key)
	}
	return n
}

// AddEnd connects a node to END.
func (w *Workflow) AddEnd(fromNode string) error {
	if w == nil {
		return fmt.Errorf("compose: nil workflow")
	}
	return w.AddEdge(fromNode, END)
}

// AddToolsNode registers a tool execution workflow step.
func (w *Workflow) AddToolsNode(ctx context.Context, name string, cfg *ToolNodeConfig) error {
	if w == nil {
		return fmt.Errorf("compose: nil workflow")
	}
	if cfg == nil {
		return fmt.Errorf("compose: nil ToolNodeConfig")
	}
	node, err := NewToolNode(ctx, cfg)
	if err != nil {
		return err
	}
	if err := w.AddLambdaStep(name, func(ctx context.Context, st *GraphState) error {
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
	w.graph.markNodeKind(name, NodeKindTools)
	return nil
}

// AddReActNode embeds a compiled ReAct graph as one workflow step.
func (w *Workflow) AddReActNode(ctx context.Context, name string, cfg ReActCompileConfig) error {
	if w == nil {
		return fmt.Errorf("compose: nil workflow")
	}
	cg, err := CompileReActGraph(ctx, cfg)
	if err != nil {
		return err
	}
	if err := w.AddLambdaStep(name, func(ctx context.Context, st *GraphState) error {
		subSt, _, err := cg.Invoke(ctx, st.Messages)
		if err != nil {
			return err
		}
		if subSt == nil {
			return nil
		}
		st.Messages = subSt.Messages
		st.LastOutput = subSt.LastOutput
		for k, v := range subSt.Vars {
			if k == RunsKey {
				continue
			}
			if st.Vars == nil {
				st.Vars = map[string]any{}
			}
			st.Vars[k] = v
		}
		for _, r := range GraphRunsFromState(subSt) {
			appendGraphRun(st, r)
		}
		return nil
	}); err != nil {
		return err
	}
	w.graph.markNodeKind(name, NodeKindReactEmbed)
	return nil
}
