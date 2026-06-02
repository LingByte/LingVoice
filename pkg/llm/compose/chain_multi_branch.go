package compose

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

const selectedBranchesKey = "__selected_branches"

// ChainMultiBranch selects multiple branch targets and runs them in parallel (Eino NewChainMultiBranch subset).
type ChainMultiBranch struct {
	MultiSelect func(ctx context.Context, st *State) ([]string, error)
	steps       map[string]Step
	defaultKeys []string
}

// NewChainMultiBranch creates a multi-target branch builder.
func NewChainMultiBranch(selectFn func(ctx context.Context, st *State) ([]string, error)) *ChainMultiBranch {
	return &ChainMultiBranch{
		MultiSelect: selectFn,
		steps:       map[string]Step{},
	}
}

// Default sets fallback branch keys when MultiSelect returns unknown keys.
func (b *ChainMultiBranch) Default(keys ...string) *ChainMultiBranch {
	if b != nil {
		b.defaultKeys = append([]string(nil), keys...)
	}
	return b
}

// AddStep registers a branch target step.
func (b *ChainMultiBranch) AddStep(key string, step Step) *ChainMultiBranch {
	if b != nil && key != "" && step != nil {
		b.steps[key] = step
	}
	return b
}

// AddPrompt adds a PromptStep branch.
func (b *ChainMultiBranch) AddPrompt(key string, role schema.RoleType, content string) *ChainMultiBranch {
	return b.AddStep(key, PromptStep{Role: role, Content: content})
}

// AddChatModel adds a ChatModelStep branch.
func (b *ChainMultiBranch) AddChatModel(key string, model llm.ChatModel, opts ...llm.Option) *ChainMultiBranch {
	return b.AddStep(key, &ChatModelStep{Model: model, Opts: opts})
}

// AddTemplate adds a TemplateStep branch.
func (b *ChainMultiBranch) AddTemplate(key string, tpl *prompt.ChatTemplate) *ChainMultiBranch {
	return b.AddStep(key, TemplateStep{Template: tpl})
}

// AddToolLoop adds a ToolLoopStep branch.
func (b *ChainMultiBranch) AddToolLoop(key string, loop *ToolLoop) *ChainMultiBranch {
	return b.AddStep(key, ToolLoopStep{Loop: loop})
}

// AddLambda adds a FuncStep branch.
func (b *ChainMultiBranch) AddLambda(key string, fn func(ctx context.Context, st *State) error) *ChainMultiBranch {
	return b.AddStep(key, FuncStep{Fn: fn})
}

// Step returns the multi-branch as a chain Step (runs selected branches concurrently in-process).
func (b *ChainMultiBranch) Step() MultiBranchStep {
	if b == nil {
		return MultiBranchStep{}
	}
	return MultiBranchStep{
		MultiSelect: b.MultiSelect,
		Steps:       b.steps,
		Default:     b.defaultKeys,
	}
}

// MultiBranchStep runs zero or more branch steps concurrently.
type MultiBranchStep struct {
	Name        string
	MultiSelect func(ctx context.Context, st *State) ([]string, error)
	Steps       map[string]Step
	Default     []string
}

func (s MultiBranchStep) Run(ctx context.Context, st *State) error {
	keys, err := s.resolveKeys(ctx, st)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	par := NewParallel(map[string]Step{})
	for _, k := range keys {
		if step, ok := s.Steps[k]; ok && step != nil {
			par.Steps[k] = step
		}
	}
	if len(par.Steps) == 0 {
		return nil
	}
	return par.Run(ctx, st)
}

func (s MultiBranchStep) resolveKeys(ctx context.Context, st *State) ([]string, error) {
	if s.MultiSelect == nil || len(s.Steps) == 0 {
		return nil, nil
	}
	keys, err := s.MultiSelect(ctx, st)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, ok := s.Steps[k]; ok {
			out = append(out, k)
		}
	}
	if len(out) == 0 && len(s.Default) > 0 {
		for _, k := range s.Default {
			if _, ok := s.Steps[k]; ok {
				out = append(out, k)
			}
		}
	}
	return out, nil
}

// AppendChainMultiBranch appends a multi-branch step to the builder.
func (b *ChainBuilder) AppendChainMultiBranch(branch *ChainMultiBranch) *ChainBuilder {
	if branch == nil {
		return b
	}
	return b.Append(branch.Step())
}

func resolveMultiBranchKeys(ms MultiBranchStep, ctx context.Context, st *GraphState) ([]string, error) {
	cs := chainStateFromGraph(st)
	return ms.resolveKeys(ctx, cs)
}

func wrapGatedBranch(key string, ms MultiBranchStep, fn NodeFunc) NodeFunc {
	return func(ctx context.Context, st *GraphState) error {
		keys, err := resolveMultiBranchKeys(ms, ctx, st)
		if err != nil {
			return err
		}
		if !containsString(keys, key) {
			return nil
		}
		return fn(ctx, st)
	}
}

func containsString(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
