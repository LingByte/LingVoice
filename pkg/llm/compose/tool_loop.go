package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	"github.com/LingByte/LingVoice/pkg/llm/tool"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

const defaultMaxToolRounds = 8

// MessageModifier adjusts messages before each model call (Eino react.MessageModifier).
type MessageModifier func(ctx context.Context, messages []*schema.Message) []*schema.Message

// ToolLoopConfig configures a minimal ReAct tool loop (Eino react.Agent subset).
type ToolLoopConfig struct {
	Model     llm.ToolCallingChatModel
	Tools     []tool.InvokableTool
	MaxRounds int
	ModelOpts []llm.Option

	MessageModifier    MessageModifier
	ToolReturnDirectly map[string]struct{}
	ToolNodeConfig     *ToolNodeConfig
	// ForceToolUse requires tool calls on the first model turn (demo / strict ReAct).
	ForceToolUse bool
}

// ToolLoop runs model → tools → model until no tool calls or max rounds.
type ToolLoop struct {
	model              llm.ToolCallingChatModel
	toolNode           *ToolNode
	tools              []tool.InvokableTool
	maxRounds          int
	modelOpts          []llm.Option
	messageModifier    MessageModifier
	toolReturnDirectly map[string]struct{}
	forceToolUse       bool
}

// NewToolLoop builds a ToolLoop from config.
func NewToolLoop(ctx context.Context, cfg ToolLoopConfig) (*ToolLoop, error) {
	if cfg.Model == nil {
		return nil, fmt.Errorf("compose: nil ToolCallingChatModel")
	}
	nodeCfg := cfg.ToolNodeConfig
	if nodeCfg == nil {
		nodeCfg = &ToolNodeConfig{Tools: cfg.Tools}
	} else if len(nodeCfg.Tools) == 0 {
		nodeCfg.Tools = cfg.Tools
	}
	node, err := NewToolNode(ctx, nodeCfg)
	if err != nil {
		return nil, err
	}
	max := cfg.MaxRounds
	if max <= 0 {
		max = defaultMaxToolRounds
	}
	return &ToolLoop{
		model:              cfg.Model,
		toolNode:           node,
		tools:              cfg.Tools,
		maxRounds:          max,
		modelOpts:          cfg.ModelOpts,
		messageModifier:    cfg.MessageModifier,
		toolReturnDirectly: cfg.ToolReturnDirectly,
		forceToolUse:       cfg.ForceToolUse,
	}, nil
}

// Run executes the loop and returns the final assistant message and message history delta.
func (l *ToolLoop) Run(ctx context.Context, messages []*schema.Message) (*schema.Message, []*schema.Message, error) {
	out, delta, _, err := l.RunWithTrace(ctx, messages)
	return out, delta, err
}

// RunWithTrace runs the loop and returns a step-by-step trace.
func (l *ToolLoop) RunWithTrace(ctx context.Context, messages []*schema.Message) (*schema.Message, []*schema.Message, *LoopTrace, error) {
	if l == nil {
		return nil, nil, nil, fmt.Errorf("compose: nil ToolLoop")
	}
	ctx = callback.InitRun(ctx, &callback.RunInfo{Name: "ToolLoop", Component: callback.ComponentChain})
	trace := &LoopTrace{}

	infos, err := tool.CollectInfos(ctx, l.tools)
	if err != nil {
		return nil, nil, trace, err
	}
	model, err := l.model.WithTools(infos)
	if err != nil {
		return nil, nil, trace, err
	}

	msgs := append([]*schema.Message(nil), messages...)
	var appended []*schema.Message

	for round := 0; round < l.maxRounds; round++ {
		callMsgs := l.applyModifier(ctx, msgs)
		opts := l.roundOpts(round)

		start := time.Now()
		out, err := model.Generate(ctx, callMsgs, opts...)
		trace.AppendModel(round, out, start)
		if err != nil {
			trace.finish()
			return nil, appended, trace, fmt.Errorf("compose: tool loop round %d: %w", round, err)
		}
		msgs = append(msgs, out)
		appended = append(appended, out)

		if out == nil || len(out.ToolCalls) == 0 {
			trace.finish()
			return out, appended, trace, nil
		}
		if direct, ok := l.shouldReturnDirectly(out); ok {
			trace.finish()
			return direct, appended, trace, nil
		}

		tStart := time.Now()
		toolMsgs, err := l.toolNode.Invoke(ctx, out)
		trace.AppendTools(round, toolMsgs, tStart)
		if err != nil {
			trace.finish()
			if IsInterrupt(err) {
				return out, appended, trace, err
			}
			return out, appended, trace, err
		}
		msgs = append(msgs, toolMsgs...)
		appended = append(appended, toolMsgs...)
	}
	trace.finish()
	return nil, appended, trace, fmt.Errorf("compose: tool loop exceeded max rounds (%d)", l.maxRounds)
}

func (l *ToolLoop) roundOpts(round int) []llm.Option {
	if !l.forceToolUse || round > 0 {
		return l.modelOpts
	}
	opts := make([]llm.Option, len(l.modelOpts), len(l.modelOpts)+1)
	copy(opts, l.modelOpts)
	return append(opts, llm.WithToolChoice(llm.ToolChoiceForced))
}

// Stream runs the loop with streaming model output; tool rounds still forward chunks.
func (l *ToolLoop) Stream(ctx context.Context, messages []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
	if l == nil {
		return nil, fmt.Errorf("compose: nil ToolLoop")
	}
	infos, err := tool.CollectInfos(ctx, l.tools)
	if err != nil {
		return nil, err
	}
	model, err := l.model.WithTools(infos)
	if err != nil {
		return nil, err
	}

	outSR, outSW := schema.Pipe[*schema.Message](32)
	go func() {
		defer outSW.Close()
		msgs := append([]*schema.Message(nil), messages...)
		for round := 0; round < l.maxRounds; round++ {
			callMsgs := l.applyModifier(ctx, msgs)
			sr, err := model.Stream(ctx, callMsgs, l.roundOpts(round)...)
			if err != nil {
				outSW.Send(nil, err)
				return
			}
			chunks, err := forwardStream(sr, outSW)
			sr.Close()
			if err != nil {
				outSW.Send(nil, err)
				return
			}
			full, err := schema.ConcatMessages(chunks)
			if err != nil {
				outSW.Send(nil, err)
				return
			}
			msgs = append(msgs, full)
			if full == nil || len(full.ToolCalls) == 0 {
				return
			}
			if _, ok := l.shouldReturnDirectly(full); ok {
				return
			}
			toolMsgs, err := l.toolNode.Invoke(ctx, full)
			if err != nil {
				outSW.Send(nil, err)
				return
			}
			msgs = append(msgs, toolMsgs...)
		}
		outSW.Send(nil, fmt.Errorf("compose: tool loop exceeded max rounds (%d)", l.maxRounds))
	}()
	return outSR, nil
}

// StreamFrames streams ReAct output with phase/round metadata.
func (l *ToolLoop) StreamFrames(ctx context.Context, messages []*schema.Message) (*StreamFrameReader, error) {
	if l == nil {
		return nil, fmt.Errorf("compose: nil ToolLoop")
	}
	infos, err := tool.CollectInfos(ctx, l.tools)
	if err != nil {
		return nil, err
	}
	model, err := l.model.WithTools(infos)
	if err != nil {
		return nil, err
	}

	frameSR, frameSW := schema.Pipe[*StreamFrame](32)
	go func() {
		defer frameSW.Close()
		msgs := append([]*schema.Message(nil), messages...)
		for round := 0; round < l.maxRounds; round++ {
			callMsgs := l.applyModifier(ctx, msgs)
			sr, err := model.Stream(ctx, callMsgs, l.roundOpts(round)...)
			if err != nil {
				full, genErr := model.Generate(ctx, callMsgs, l.roundOpts(round)...)
				if genErr != nil {
					frameSW.Send(nil, genErr)
					return
				}
				frameSW.Send(&StreamFrame{Round: round, Phase: PhaseModel, Chunk: full, Done: full == nil || len(full.ToolCalls) == 0, ToolCalls: toolCallsOf(full)}, nil)
				if full == nil || len(full.ToolCalls) == 0 {
					return
				}
				if _, ok := l.shouldReturnDirectly(full); ok {
					return
				}
				msgs = append(msgs, full)
				frameSW.Send(&StreamFrame{Round: round, Phase: PhaseTools, ToolCalls: full.ToolCalls}, nil)
				toolMsgs, err := l.toolNode.Invoke(ctx, full)
				if err != nil {
					if IsInterrupt(err) {
						frameSW.Send(&StreamFrame{Round: round, Phase: PhaseTools, Done: true}, err)
						return
					}
					frameSW.Send(nil, err)
					return
				}
				for _, tm := range toolMsgs {
					frameSW.Send(&StreamFrame{Round: round, Phase: PhaseTools, Chunk: tm}, nil)
				}
				msgs = append(msgs, toolMsgs...)
				continue
			}
			var chunks []*schema.Message
			for {
				chunk, err := sr.Recv()
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					sr.Close()
					frameSW.Send(nil, err)
					return
				}
				chunks = append(chunks, chunk)
				frameSW.Send(&StreamFrame{Round: round, Phase: PhaseModel, Chunk: chunk}, nil)
			}
			sr.Close()
			full, err := schema.ConcatMessages(chunks)
			if err != nil {
				frameSW.Send(nil, err)
				return
			}
			msgs = append(msgs, full)
			if full == nil || len(full.ToolCalls) == 0 {
				frameSW.Send(&StreamFrame{Round: round, Phase: PhaseModel, Chunk: full, Done: true}, nil)
				return
			}
			if _, ok := l.shouldReturnDirectly(full); ok {
				frameSW.Send(&StreamFrame{Round: round, Phase: PhaseModel, Chunk: full, Done: true, ToolCalls: full.ToolCalls}, nil)
				return
			}
			frameSW.Send(&StreamFrame{Round: round, Phase: PhaseTools, ToolCalls: full.ToolCalls}, nil)
			toolMsgs, err := l.toolNode.Invoke(ctx, full)
			if err != nil {
				if IsInterrupt(err) {
					frameSW.Send(&StreamFrame{Round: round, Phase: PhaseTools, Done: true}, err)
					return
				}
				frameSW.Send(nil, err)
				return
			}
			for _, tm := range toolMsgs {
				frameSW.Send(&StreamFrame{Round: round, Phase: PhaseTools, Chunk: tm}, nil)
			}
			msgs = append(msgs, toolMsgs...)
		}
		frameSW.Send(nil, fmt.Errorf("compose: tool loop exceeded max rounds (%d)", l.maxRounds))
	}()
	return &StreamFrameReader{inner: frameSR}, nil
}

func toolCallsOf(m *schema.Message) []schema.ToolCall {
	if m == nil {
		return nil
	}
	return m.ToolCalls
}

func forwardStream(sr *schema.StreamReader[*schema.Message], sw *schema.StreamWriter[*schema.Message]) ([]*schema.Message, error) {
	var chunks []*schema.Message
	for {
		chunk, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return chunks, nil
			}
			return chunks, err
		}
		chunks = append(chunks, chunk)
		sw.Send(chunk, nil)
	}
}

func (l *ToolLoop) applyModifier(ctx context.Context, msgs []*schema.Message) []*schema.Message {
	if l.messageModifier == nil {
		return msgs
	}
	return l.messageModifier(ctx, msgs)
}

func (l *ToolLoop) shouldReturnDirectly(out *schema.Message) (*schema.Message, bool) {
	if len(l.toolReturnDirectly) == 0 || out == nil {
		return nil, false
	}
	for _, tc := range out.ToolCalls {
		if _, ok := l.toolReturnDirectly[tc.Function.Name]; ok {
			return out, true
		}
	}
	return nil, false
}

// ToolLoopStep is a Chain step that runs ToolLoop and updates state.
type ToolLoopStep struct {
	Loop *ToolLoop
}

func (s ToolLoopStep) Run(ctx context.Context, st *State) error {
	if s.Loop == nil {
		return fmt.Errorf("compose: nil ToolLoop")
	}
	out, delta, err := s.Loop.Run(ctx, st.Messages)
	if err != nil {
		return err
	}
	st.Messages = append(st.Messages, delta...)
	st.LastOutput = out
	return nil
}
