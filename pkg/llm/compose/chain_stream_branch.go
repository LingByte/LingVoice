package compose

import (
	"context"
	"sync"

	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// StreamChainBranch is a ChainBranch builder for pipeline frame streaming (Eino NewStreamChainBranch subset).
type StreamChainBranch struct {
	Select     func(ctx context.Context, st *State) (string, error)
	steps      map[string]Step
	defaultKey string
}

// NewStreamChainBranch creates a branch builder intended for Pipeline.StreamFrames.
func NewStreamChainBranch(selectFn func(ctx context.Context, st *State) (string, error)) *StreamChainBranch {
	return &StreamChainBranch{
		Select: selectFn,
		steps:  map[string]Step{},
	}
}

// Default sets the fallback branch key when Select returns an unknown key.
func (b *StreamChainBranch) Default(key string) *StreamChainBranch {
	if b != nil {
		b.defaultKey = key
	}
	return b
}

// AddStep registers a branch target step.
func (b *StreamChainBranch) AddStep(key string, step Step) *StreamChainBranch {
	if b != nil && key != "" && step != nil {
		b.steps[key] = step
	}
	return b
}

// AddPrompt adds a PromptStep branch.
func (b *StreamChainBranch) AddPrompt(key string, role schema.RoleType, content string) *StreamChainBranch {
	return b.AddStep(key, PromptStep{Role: role, Content: content})
}

// AddChatModel adds a ChatModelStep branch.
func (b *StreamChainBranch) AddChatModel(key string, model llm.ChatModel, opts ...llm.Option) *StreamChainBranch {
	return b.AddStep(key, &ChatModelStep{Model: model, Opts: opts})
}

// AddTemplate adds a TemplateStep branch.
func (b *StreamChainBranch) AddTemplate(key string, tpl *prompt.ChatTemplate) *StreamChainBranch {
	return b.AddStep(key, TemplateStep{Template: tpl})
}

// AddToolLoop adds a ToolLoopStep branch.
func (b *StreamChainBranch) AddToolLoop(key string, loop *ToolLoop) *StreamChainBranch {
	return b.AddStep(key, ToolLoopStep{Loop: loop})
}

// AddLambda adds a FuncStep branch.
func (b *StreamChainBranch) AddLambda(key string, fn func(ctx context.Context, st *State) error) *StreamChainBranch {
	return b.AddStep(key, FuncStep{Fn: fn})
}

// Step returns the branch as a pipeline Step with StreamFrames support.
func (b *StreamChainBranch) Step() BranchStep {
	if b == nil {
		return BranchStep{}
	}
	return BranchStep{
		Select:  b.Select,
		Steps:   b.steps,
		Default: b.defaultKey,
	}
}

// AppendStreamChainBranch appends a stream branch step to the builder.
func (b *ChainBuilder) AppendStreamChainBranch(branch *StreamChainBranch) *ChainBuilder {
	if branch == nil {
		return b
	}
	return b.Append(branch.Step())
}

// StreamFrames runs selected branch steps concurrently and merges frame streams.
func (s MultiBranchStep) StreamFrames(ctx context.Context, st *State) (*StreamFrameReader, error) {
	keys, err := s.resolveKeys(ctx, st)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}
	var readers []*StreamFrameReader
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	for _, k := range keys {
		step, ok := s.Steps[k]
		if !ok || step == nil {
			continue
		}
		wg.Add(1)
		go func(key string, step Step) {
			defer wg.Done()
			stage := "multi_branch:" + key
			var sr *StreamFrameReader
			var err error
			if fs, ok := step.(FrameStreamer); ok {
				sr, err = fs.StreamFrames(ctx, st)
				if err == nil && sr != nil {
					sr, err = TagStreamFrames(sr, stage)
				}
			} else {
				if err = step.Run(ctx, st); err == nil {
					pipeSR, pipeSW := schema.Pipe[*StreamFrame](1)
					go func() {
						defer pipeSW.Close()
						pipeSW.Send(&StreamFrame{Stage: stage, Phase: PhaseModel, Done: true}, nil)
					}()
					sr = &StreamFrameReader{inner: pipeSR}
				}
			}
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			if sr != nil {
				mu.Lock()
				readers = append(readers, sr)
				mu.Unlock()
			}
		}(k, step)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if len(readers) == 0 {
		return nil, nil
	}
	if len(readers) == 1 {
		return readers[0], nil
	}
	return MergeStreamFrames(readers...), nil
}
