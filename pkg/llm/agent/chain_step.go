package agent

import (
	"context"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
)

// ReActStep runs a ReActAgent inside a Chain (compose.State integration).
type ReActStep struct {
	Agent        *ReActAgent
	CheckPointID string
}

// Run executes the agent and merges results into chain state.
func (s ReActStep) Run(ctx context.Context, st *compose.State) error {
	if s.Agent == nil {
		return composeErr("nil ReActAgent in ReActStep")
	}
	var opts []compose.GraphInvokeOption
	if s.CheckPointID != "" {
		opts = append(opts, compose.WithCheckPointID(s.CheckPointID))
	}
	result, err := s.Agent.GenerateWithTrace(ctx, st.Messages, opts...)
	if result != nil {
		if result.Message != nil {
			st.LastOutput = result.Message
		}
		if result.State != nil && len(result.State.Messages) > 0 {
			st.Messages = result.State.Messages
		} else if len(result.Delta) > 0 {
			st.Messages = append(st.Messages, result.Delta...)
		}
		if result.State != nil && result.State.Vars != nil {
			if st.Vars == nil {
				st.Vars = map[string]any{}
			}
			for k, v := range result.State.Vars {
				st.Vars[k] = v
			}
		}
		for _, r := range compose.GraphRunsFromState(result.State) {
			compose.AppendRun(st, r)
		}
	}
	if err != nil {
		if info, ok := compose.ExtractInterruptInfo(err); ok {
			if st.Vars == nil {
				st.Vars = map[string]any{}
			}
			st.Vars["__interrupt"] = info
		}
		return err
	}
	return nil
}

// StreamStep streams ReAct output with frame metadata via ToolLoop.
type StreamStep struct {
	Agent *ReActAgent
}

// Stream returns framed stream for chain/streaming demos.
func (s StreamStep) Stream(ctx context.Context, st *compose.State) (*compose.StreamFrameReader, error) {
	if s.Agent == nil {
		return nil, composeErr("nil ReActAgent in StreamStep")
	}
	return s.Agent.StreamFrames(ctx, st.Messages)
}

// StreamFrames implements compose.FrameStreamer for pipeline streaming.
func (s ReActStep) StreamFrames(ctx context.Context, st *compose.State) (*compose.StreamFrameReader, error) {
	if s.Agent == nil {
		return nil, composeErr("nil ReActAgent in ReActStep")
	}
	sr, err := s.Agent.StreamFrames(ctx, st.Messages)
	if err != nil {
		return nil, err
	}
	return compose.TagStreamFrames(sr, "react")
}

// ResumeStep continues a checkpointed ReAct run inside a Chain.
type ResumeStep struct {
	Agent        *ReActAgent
	CheckPointID string
	ResumeData   map[string]any
}

func (s ResumeStep) Run(ctx context.Context, st *compose.State) error {
	if s.Agent == nil {
		return composeErr("nil ReActAgent in ResumeStep")
	}
	result, err := s.Agent.Resume(ctx, s.CheckPointID, s.ResumeData)
	if result != nil {
		st.LastOutput = result.Message
		if result.State != nil {
			st.Messages = result.State.Messages
		}
	}
	return err
}

var _ compose.Step = ReActStep{}
