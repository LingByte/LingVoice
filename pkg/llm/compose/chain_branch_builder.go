package compose

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// ChainBranch is an Eino-style conditional branch builder for Chain/Pipeline.
type ChainBranch struct {
	Select     func(ctx context.Context, st *State) (string, error)
	steps      map[string]Step
	defaultKey string
}

// NewChainBranch creates a branch with a selector over chain state.
func NewChainBranch(selectFn func(ctx context.Context, st *State) (string, error)) *ChainBranch {
	return &ChainBranch{
		Select: selectFn,
		steps:  map[string]Step{},
	}
}

// Default sets the fallback branch key when Select returns an unknown key.
func (b *ChainBranch) Default(key string) *ChainBranch {
	if b != nil {
		b.defaultKey = key
	}
	return b
}

// AddStep registers a branch target step.
func (b *ChainBranch) AddStep(key string, step Step) *ChainBranch {
	if b != nil && key != "" && step != nil {
		b.steps[key] = step
	}
	return b
}

// AddPrompt adds a PromptStep branch.
func (b *ChainBranch) AddPrompt(key string, role schema.RoleType, content string) *ChainBranch {
	return b.AddStep(key, PromptStep{Role: role, Content: content})
}

// AddChatModel adds a ChatModelStep branch.
func (b *ChainBranch) AddChatModel(key string, model llm.ChatModel, opts ...llm.Option) *ChainBranch {
	return b.AddStep(key, &ChatModelStep{Model: model, Opts: opts})
}

// AddTemplate adds a TemplateStep branch.
func (b *ChainBranch) AddTemplate(key string, tpl *prompt.ChatTemplate) *ChainBranch {
	return b.AddStep(key, TemplateStep{Template: tpl})
}

// AddToolLoop adds a ToolLoopStep branch.
func (b *ChainBranch) AddToolLoop(key string, loop *ToolLoop) *ChainBranch {
	return b.AddStep(key, ToolLoopStep{Loop: loop})
}

// AddLambda adds a FuncStep branch.
func (b *ChainBranch) AddLambda(key string, fn func(ctx context.Context, st *State) error) *ChainBranch {
	return b.AddStep(key, FuncStep{Fn: fn})
}

// Step returns the branch as a pipeline/chain Step.
func (b *ChainBranch) Step() BranchStep {
	if b == nil {
		return BranchStep{}
	}
	return BranchStep{
		Select:  b.Select,
		Steps:   b.steps,
		Default: b.defaultKey,
	}
}

// AppendChainBranch on ChainBuilder appends a ChainBranch step.
func (b *ChainBuilder) AppendChainBranch(branch *ChainBranch) *ChainBuilder {
	if branch == nil {
		return b
	}
	return b.Append(branch.Step())
}
