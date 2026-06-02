package compose

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// ChainParallel is an Eino-style parallel step builder for Chain/Pipeline.
type ChainParallel struct {
	steps map[string]Step
}

// NewChainParallel creates an empty parallel builder.
func NewChainParallel() *ChainParallel {
	return &ChainParallel{steps: map[string]Step{}}
}

// AddStep registers a named parallel branch step.
func (p *ChainParallel) AddStep(key string, step Step) *ChainParallel {
	if p != nil && key != "" && step != nil {
		if p.steps == nil {
			p.steps = map[string]Step{}
		}
		p.steps[key] = step
	}
	return p
}

// AddPrompt adds a PromptStep branch.
func (p *ChainParallel) AddPrompt(key string, role schema.RoleType, content string) *ChainParallel {
	return p.AddStep(key, PromptStep{Role: role, Content: content})
}

// AddChatModel adds a ChatModelStep branch.
func (p *ChainParallel) AddChatModel(key string, model llm.ChatModel, opts ...llm.Option) *ChainParallel {
	return p.AddStep(key, &ChatModelStep{Model: model, Opts: opts})
}

// AddTemplate adds a TemplateStep branch.
func (p *ChainParallel) AddTemplate(key string, tpl *prompt.ChatTemplate) *ChainParallel {
	return p.AddStep(key, TemplateStep{Template: tpl})
}

// AddToolLoop adds a ToolLoopStep branch.
func (p *ChainParallel) AddToolLoop(key string, loop *ToolLoop) *ChainParallel {
	return p.AddStep(key, ToolLoopStep{Loop: loop})
}

// AddLambda adds a FuncStep branch.
func (p *ChainParallel) AddLambda(key string, fn func(ctx context.Context, st *State) error) *ChainParallel {
	return p.AddStep(key, FuncStep{Fn: fn})
}

// Step returns the parallel group as a pipeline/chain Step.
func (p *ChainParallel) Step() ParallelStep {
	if p == nil {
		return ParallelStep{}
	}
	return ParallelStep{Parallel: NewParallel(p.steps)}
}

// AppendChainParallel appends a ChainParallel step to the builder.
func (b *ChainBuilder) AppendChainParallel(p *ChainParallel) *ChainBuilder {
	if p == nil {
		return b
	}
	return b.Append(p.Step())
}
