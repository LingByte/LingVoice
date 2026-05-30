package compose

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// StreamChainMultiBranch selects multiple branch targets for concurrent frame streaming.
type StreamChainMultiBranch struct {
	MultiSelect func(ctx context.Context, st *State) ([]string, error)
	steps       map[string]Step
	defaultKeys []string
}

// NewStreamChainMultiBranch creates a multi-target stream branch builder.
func NewStreamChainMultiBranch(selectFn func(ctx context.Context, st *State) ([]string, error)) *StreamChainMultiBranch {
	return &StreamChainMultiBranch{
		MultiSelect: selectFn,
		steps:       map[string]Step{},
	}
}

// Default sets fallback branch keys when MultiSelect returns unknown keys.
func (b *StreamChainMultiBranch) Default(keys ...string) *StreamChainMultiBranch {
	if b != nil {
		b.defaultKeys = append([]string(nil), keys...)
	}
	return b
}

// AddStep registers a branch target step.
func (b *StreamChainMultiBranch) AddStep(key string, step Step) *StreamChainMultiBranch {
	if b != nil && key != "" && step != nil {
		b.steps[key] = step
	}
	return b
}

// AddPrompt adds a PromptStep branch.
func (b *StreamChainMultiBranch) AddPrompt(key string, role schema.RoleType, content string) *StreamChainMultiBranch {
	return b.AddStep(key, PromptStep{Role: role, Content: content})
}

// AddChatModel adds a ChatModelStep branch.
func (b *StreamChainMultiBranch) AddChatModel(key string, model llm.ChatModel, opts ...llm.Option) *StreamChainMultiBranch {
	return b.AddStep(key, &ChatModelStep{Model: model, Opts: opts})
}

// AddTemplate adds a TemplateStep branch.
func (b *StreamChainMultiBranch) AddTemplate(key string, tpl *prompt.ChatTemplate) *StreamChainMultiBranch {
	return b.AddStep(key, TemplateStep{Template: tpl})
}

// AddToolLoop adds a ToolLoopStep branch (supports StreamFrames when wrapped as ToolLoopStreamStage).
func (b *StreamChainMultiBranch) AddToolLoop(key string, loop *ToolLoop) *StreamChainMultiBranch {
	return b.AddStep(key, ToolLoopStreamStage{Loop: loop, Stage: key})
}

// AddLambda adds a FuncStep branch.
func (b *StreamChainMultiBranch) AddLambda(key string, fn func(ctx context.Context, st *State) error) *StreamChainMultiBranch {
	return b.AddStep(key, FuncStep{Fn: fn})
}

// Step returns the multi-branch as a pipeline Step with StreamFrames support.
func (b *StreamChainMultiBranch) Step() MultiBranchStep {
	if b == nil {
		return MultiBranchStep{}
	}
	return MultiBranchStep{
		MultiSelect: b.MultiSelect,
		Steps:       b.steps,
		Default:     b.defaultKeys,
	}
}

// AppendStreamChainMultiBranch appends a stream multi-branch step to the builder.
func (b *ChainBuilder) AppendStreamChainMultiBranch(branch *StreamChainMultiBranch) *ChainBuilder {
	if branch == nil {
		return b
	}
	return b.Append(branch.Step())
}
