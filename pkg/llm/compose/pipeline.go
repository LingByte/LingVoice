package compose

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/LingByte/LingVoice/pkg/llm/prompt"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// PipelineConfig builds Template → optional Branch → Steps.
type PipelineConfig struct {
	Name     string
	Template *prompt.ChatTemplate
	Branch   *BranchStep
	Steps    []Step
}

// Pipeline is a multi-stage LLM orchestration runner.
type Pipeline struct {
	name      string
	template  *prompt.ChatTemplate
	branch    BranchStep
	stages    []Step
	hasBranch bool
}

// NewPipeline creates a pipeline from config.
func NewPipeline(cfg PipelineConfig) (*Pipeline, error) {
	if len(cfg.Steps) == 0 && cfg.Template == nil && cfg.Branch == nil {
		return nil, fmt.Errorf("compose: empty pipeline")
	}
	name := cfg.Name
	if name == "" {
		name = "pipeline"
	}
	p := &Pipeline{name: name, template: cfg.Template, stages: cfg.Steps}
	if cfg.Branch != nil {
		p.branch = *cfg.Branch
		p.hasBranch = true
	}
	return p, nil
}

// Run executes all stages against input messages.
func (p *Pipeline) Run(ctx context.Context, input []*schema.Message, vars map[string]any) (*State, error) {
	if p == nil {
		return nil, fmt.Errorf("compose: nil pipeline")
	}
	st := &State{
		Messages: append([]*schema.Message(nil), input...),
		Vars:     map[string]any{},
	}
	for k, v := range vars {
		st.Vars[k] = v
	}
	if p.template != nil {
		step := TemplateStep{Template: p.template}
		if err := step.Run(ctx, st); err != nil {
			return st, fmt.Errorf("compose: pipeline %q template: %w", p.name, err)
		}
	}
	if p.hasBranch {
		if err := p.branch.Run(ctx, st); err != nil {
			return st, fmt.Errorf("compose: pipeline %q branch: %w", p.name, err)
		}
	}
	for i, stage := range p.stages {
		if stage == nil {
			continue
		}
		if err := stage.Run(ctx, st); err != nil {
			return st, fmt.Errorf("compose: pipeline %q stage %d: %w", p.name, i, err)
		}
	}
	return st, nil
}

// FuncStage wraps a function as a Step (alias for FuncStep in pipelines).
type FuncStage = FuncStep

// ToolLoopStage adapts ToolLoop as a pipeline stage.
type ToolLoopStage struct {
	Loop *ToolLoop
}

func (s ToolLoopStage) Run(ctx context.Context, st *State) error {
	return ToolLoopStep{Loop: s.Loop}.Run(ctx, st)
}

// FrameStreamer is a pipeline stage that emits frame-level traces.
type FrameStreamer interface {
	Step
	StreamFrames(ctx context.Context, st *State) (*StreamFrameReader, error)
}

// ToolLoopStreamStage streams ToolLoop frames inside a pipeline.
type ToolLoopStreamStage struct {
	Loop  *ToolLoop
	Stage string
}

func (s ToolLoopStreamStage) Run(ctx context.Context, st *State) error {
	return ToolLoopStage{Loop: s.Loop}.Run(ctx, st)
}

func (s ToolLoopStreamStage) StreamFrames(ctx context.Context, st *State) (*StreamFrameReader, error) {
	if s.Loop == nil {
		return nil, fmt.Errorf("compose: nil ToolLoop")
	}
	sr, err := s.Loop.StreamFrames(ctx, st.Messages)
	if err != nil {
		return nil, err
	}
	return TagStreamFrames(sr, s.stageName())
}

func (s ToolLoopStreamStage) stageName() string {
	if s.Stage != "" {
		return s.Stage
	}
	return "tool-loop"
}

// StreamFrames executes the pipeline and emits frame-level trace events.
func (p *Pipeline) StreamFrames(ctx context.Context, input []*schema.Message, vars map[string]any) (*StreamFrameReader, error) {
	if p == nil {
		return nil, fmt.Errorf("compose: nil pipeline")
	}
	frameSR, frameSW := schema.Pipe[*StreamFrame](32)
	go func() {
		defer frameSW.Close()
		st := &State{
			Messages: append([]*schema.Message(nil), input...),
			Vars:     map[string]any{},
		}
		for k, v := range vars {
			st.Vars[k] = v
		}
		if p.template != nil {
			frameSW.Send(&StreamFrame{Stage: "template", Phase: PhaseModel}, nil)
			step := TemplateStep{Template: p.template}
			if err := step.Run(ctx, st); err != nil {
				frameSW.Send(nil, fmt.Errorf("compose: pipeline %q template: %w", p.name, err))
				return
			}
		}
		if p.hasBranch {
			frameSW.Send(&StreamFrame{Stage: "branch", Phase: PhaseModel}, nil)
			if err := forwardTaggedFramesFromStep(ctx, st, p.branch, "branch", frameSW); err != nil {
				frameSW.Send(nil, fmt.Errorf("compose: pipeline %q branch: %w", p.name, err))
				return
			}
		}
		for i, stage := range p.stages {
			if stage == nil {
				continue
			}
			stageName := fmt.Sprintf("stage-%d", i)
			if fs, ok := stage.(FrameStreamer); ok {
				sr, err := fs.StreamFrames(ctx, st)
				if err != nil {
					frameSW.Send(nil, fmt.Errorf("compose: pipeline %q stage %d stream: %w", p.name, i, err))
					return
				}
				if err := forwardTaggedFrames(sr, frameSW, stageName); err != nil {
					frameSW.Send(nil, err)
					return
				}
				continue
			}
			frameSW.Send(&StreamFrame{Stage: stageName, Phase: PhaseModel}, nil)
			if err := stage.Run(ctx, st); err != nil {
				frameSW.Send(nil, fmt.Errorf("compose: pipeline %q stage %d: %w", p.name, i, err))
				return
			}
			frameSW.Send(&StreamFrame{Stage: stageName, Phase: PhaseModel, Done: true}, nil)
		}
		frameSW.Send(&StreamFrame{Stage: p.name, Phase: PhaseModel, Done: true}, nil)
	}()
	return &StreamFrameReader{inner: frameSR}, nil
}

// TagStreamFrames rewrites empty Stage fields on forwarded frames.
func TagStreamFrames(sr *StreamFrameReader, stage string) (*StreamFrameReader, error) {
	return tagStreamFrames(sr, stage)
}

func tagStreamFrames(sr *StreamFrameReader, stage string) (*StreamFrameReader, error) {
	if sr == nil {
		return nil, fmt.Errorf("compose: nil stream frame reader")
	}
	outSR, outSW := schema.Pipe[*StreamFrame](32)
	go func() {
		defer outSW.Close()
		defer sr.Close()
		for {
			f, err := sr.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					outSW.Send(nil, err)
				}
				return
			}
			if f != nil && f.Stage == "" {
				f.Stage = stage
			}
			outSW.Send(f, nil)
		}
	}()
	return &StreamFrameReader{inner: outSR}, nil
}

func forwardTaggedFrames(sr *StreamFrameReader, sw *schema.StreamWriter[*StreamFrame], stage string) error {
	defer sr.Close()
	for {
		f, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if f != nil && f.Stage == "" {
			f.Stage = stage
		}
		sw.Send(f, nil)
	}
}

func forwardTaggedFramesFromStep(ctx context.Context, st *State, step Step, stage string, sw *schema.StreamWriter[*StreamFrame]) error {
	if fs, ok := step.(FrameStreamer); ok {
		sr, err := fs.StreamFrames(ctx, st)
		if err != nil {
			return err
		}
		return forwardTaggedFrames(sr, sw, stage)
	}
	if err := step.Run(ctx, st); err != nil {
		return err
	}
	sw.Send(&StreamFrame{Stage: stage, Phase: PhaseModel, Done: true}, nil)
	return nil
}
