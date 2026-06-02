package compose

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// ChainBuilder constructs sequential pipelines with an Eino-style Append API.
type ChainBuilder struct {
	name  string
	steps []Step
}

// NewChainBuilder creates an empty builder.
func NewChainBuilder(name string) *ChainBuilder {
	return &ChainBuilder{name: name, steps: make([]Step, 0, 4)}
}

// Append adds any Step to the chain.
func (b *ChainBuilder) Append(step Step) *ChainBuilder {
	if b != nil && step != nil {
		b.steps = append(b.steps, step)
	}
	return b
}

// AppendPrompt adds a static prompt message step.
func (b *ChainBuilder) AppendPrompt(role schema.RoleType, content string) *ChainBuilder {
	return b.Append(PromptStep{Role: role, Content: content})
}

// AppendChatModel adds a ChatModel step.
func (b *ChainBuilder) AppendChatModel(model llm.ChatModel, opts ...llm.Option) *ChainBuilder {
	return b.Append(&ChatModelStep{Model: model, Opts: opts})
}

// AppendTemplate adds a template rendering step.
func (b *ChainBuilder) AppendTemplate(tpl *prompt.ChatTemplate) *ChainBuilder {
	return b.Append(TemplateStep{Template: tpl})
}

// AppendToolLoop adds a ToolLoop step.
func (b *ChainBuilder) AppendToolLoop(loop *ToolLoop) *ChainBuilder {
	return b.Append(ToolLoopStep{Loop: loop})
}

// AppendParallel adds a parallel fan-out step.
func (b *ChainBuilder) AppendParallel(p *Parallel) *ChainBuilder {
	return b.Append(ParallelStep{Parallel: p})
}

// AppendBranch adds a conditional branch step.
func (b *ChainBuilder) AppendBranch(branch BranchStep) *ChainBuilder {
	return b.Append(branch)
}

// AppendPassthrough adds a no-op passthrough step (Eino AppendPassthrough subset).
func (b *ChainBuilder) AppendPassthrough() *ChainBuilder {
	return b.Append(PassthroughStep{})
}

// AppendGraph embeds a nested graph as a chain step.
func (b *ChainBuilder) AppendGraph(name string, sub *Graph, opts ...GraphCompileOption) *ChainBuilder {
	if sub == nil {
		return b
	}
	return b.Append(SubGraphStep{Name: name, Graph: sub, Opts: opts})
}

// CompileGraphRunnable compiles to ChainRunnable with Invoke/Stream/Collect/Transform.
func (b *ChainBuilder) CompileGraphRunnable(ctx context.Context, opts ...GraphCompileOption) (*ChainRunnable, error) {
	g, err := b.CompileGraph(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return NewChainRunnable(g), nil
}

// BuildChain returns a sequential Chain (in-memory step runner).
func (b *ChainBuilder) BuildChain() *Chain {
	if b == nil {
		return NewChain("chain")
	}
	name := b.name
	if name == "" {
		name = "chain"
	}
	return NewChain(name, b.steps...)
}

// CompileGraph compiles steps into a graph. BranchStep expands to conditional edges (Eino AppendBranch subset).
func (b *ChainBuilder) CompileGraph(_ context.Context, opts ...GraphCompileOption) (*CompiledGraph, error) {
	if b == nil {
		return nil, fmt.Errorf("compose: nil chain builder")
	}
	return compileChainBuilderGraph(b.name, b.steps, opts...)
}

func stepToNodeFn(step Step) NodeFunc {
	return func(ctx context.Context, st *GraphState) error {
		vars := map[string]any{}
		for k, v := range st.Vars {
			if k != RunsKey {
				vars[k] = v
			}
		}
		chainSt := &State{
			Messages:   append([]*schema.Message(nil), st.Messages...),
			LastOutput: st.LastOutput,
			Vars:       vars,
		}
		if err := step.Run(ctx, chainSt); err != nil {
			return err
		}
		st.Messages = chainSt.Messages
		st.LastOutput = chainSt.LastOutput
		for k, v := range chainSt.Vars {
			if k == RunsKey {
				continue
			}
			if st.Vars == nil {
				st.Vars = map[string]any{}
			}
			st.Vars[k] = v
		}
		for _, r := range RunsFromState(chainSt) {
			appendGraphRun(st, r)
		}
		return nil
	}
}
