package compose

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// BranchStep runs one of several steps based on a selector (Eino conditional edge subset).
type BranchStep struct {
	Name      string
	Select    func(ctx context.Context, st *State) (string, error)
	Steps     map[string]Step
	Default   string
}

func (s BranchStep) Run(ctx context.Context, st *State) error {
	step, _, err := s.resolve(ctx, st)
	if err != nil || step == nil {
		return err
	}
	return step.Run(ctx, st)
}

// StreamFrames runs the selected branch step with frame-level trace when supported.
func (s BranchStep) StreamFrames(ctx context.Context, st *State) (*StreamFrameReader, error) {
	step, key, err := s.resolve(ctx, st)
	if err != nil {
		return nil, err
	}
	if step == nil {
		return nil, nil
	}
	stage := "branch"
	if key != "" {
		stage = "branch:" + key
	}
	if fs, ok := step.(FrameStreamer); ok {
		inner, err := fs.StreamFrames(ctx, st)
		if err != nil {
			return nil, err
		}
		return TagStreamFrames(inner, stage)
	}
	if err := step.Run(ctx, st); err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*StreamFrame](1)
	go func() {
		defer sw.Close()
		sw.Send(&StreamFrame{Stage: stage, Phase: PhaseModel, Done: true}, nil)
	}()
	return &StreamFrameReader{inner: sr}, nil
}

func (s BranchStep) resolve(ctx context.Context, st *State) (Step, string, error) {
	if s.Select == nil || len(s.Steps) == 0 {
		return nil, "", nil
	}
	key, err := s.Select(ctx, st)
	if err != nil {
		return nil, "", err
	}
	step, ok := s.Steps[key]
	if !ok {
		if s.Default != "" {
			step = s.Steps[s.Default]
		}
	}
	if step == nil {
		return nil, key, nil
	}
	return step, key, nil
}
