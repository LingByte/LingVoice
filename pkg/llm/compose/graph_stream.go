package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// reActRuntime holds streaming/react execution state on a compiled ReAct graph.
type reActRuntime struct {
	model              llm.ToolCallingChatModel
	toolNode           *ToolNode
	messageModifier    MessageModifier
	modelOpts          []llm.Option
	toolReturnDirectly map[string]struct{}
	forceToolUse       bool
	maxRounds          int
}

func attachReActRuntime(g *CompiledGraph, cfg ReActCompileConfig, bound llm.ToolCallingChatModel, toolNode *ToolNode) {
	if g == nil {
		return
	}
	max := cfg.MaxSteps
	if max <= 0 {
		max = defaultMaxToolRounds
	}
	g.react = &reActRuntime{
		model:              bound,
		toolNode:           toolNode,
		messageModifier:    cfg.MessageModifier,
		modelOpts:          cfg.ModelOpts,
		toolReturnDirectly: cfg.ToolReturnDirectly,
		forceToolUse:       cfg.ForceToolUse,
		maxRounds:          max,
	}
	if g.nodeKinds == nil {
		g.nodeKinds = map[string]NodeKind{}
	}
	g.nodeKinds[NodeChatModel] = NodeKindChatModel
	g.nodeKinds[NodeTools] = NodeKindTools
}

func recordGraphModelRun(st *GraphState, model llm.ChatModel, msgs []*schema.Message, out *schema.Message, start time.Time, err error) {
	entry := RunEntry{
		Step:          NodeChatModel,
		Model:         modelName(model),
		InputMessages: len(msgs),
		StartedAt:     start,
		DurationMs:    float64(time.Since(start)) / float64(time.Millisecond),
	}
	if out != nil && out.ResponseMeta != nil && out.ResponseMeta.Usage != nil {
		entry.Usage = out.ResponseMeta.Usage
	}
	if err != nil {
		entry.Error = err.Error()
	}
	appendGraphRun(st, entry)
}

// Stream runs the ReAct graph with token-level model streaming.
func (r *CompiledGraph) Stream(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	frames, err := r.StreamFrames(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	outSR, outSW := schema.Pipe[*schema.Message](32)
	go func() {
		defer outSW.Close()
		defer frames.Close()
		for {
			f, err := frames.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					outSW.Send(nil, err)
				}
				return
			}
			if f != nil && f.Phase == PhaseModel && f.Chunk != nil {
				outSW.Send(f.Chunk, nil)
			}
			if f != nil && f.Done {
				return
			}
		}
	}()
	return outSR, nil
}

// StreamFrames runs the compiled ReAct graph with node-level streaming.
func (r *CompiledGraph) StreamFrames(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*StreamFrameReader, error) {
	if r == nil {
		return nil, fmt.Errorf("compose: nil compiled graph")
	}
	if r.react == nil {
		return nil, fmt.Errorf("compose: graph %q is not a ReAct graph", r.name)
	}
	rt := r.react
	frameSR, frameSW := schema.Pipe[*StreamFrame](32)
	go func() {
		defer frameSW.Close()
		icfg := applyInvokeOptions(opts...)
		if len(icfg.resumeData) > 0 {
			ctx = BatchResumeWithData(ctx, icfg.resumeData)
		}
		if len(icfg.chatModelOpts) > 0 {
			ctx = withGraphChatModelOpts(ctx, icfg.chatModelOpts)
		}
		ctx = callback.InitRun(ctx, &callback.RunInfo{Name: r.name, Component: callback.ComponentGraph})

		msgs := append([]*schema.Message(nil), messages...)
		startRound := 0
		resumeAtTools := false
		var resumedAssistant *schema.Message
		if sr := r.loadStreamResume(ctx, icfg.checkpointID); sr != nil {
			msgs = sr.msgs
			startRound = sr.round
			resumeAtTools = sr.atTools
			resumedAssistant = sr.lastOutput
		}

		for round := startRound; round < rt.maxRounds; round++ {
			var full *schema.Message
			var err error
			skippedModel := false
			if resumeAtTools {
				full = resumedAssistant
				resumeAtTools = false
				skippedModel = true
			} else {
				callMsgs := msgs
				if rt.messageModifier != nil {
					callMsgs = rt.messageModifier(ctx, msgs)
				}
				modelOpts := mergeModelOpts(rt.modelOpts, chatModelOptsFromContext(ctx))
				if rt.forceToolUse && round == 0 {
					modelOpts = roundOptsCopy(modelOpts, true)
				}
				full, err = r.streamModelRound(ctx, frameSW, round, callMsgs, modelOpts)
				if err != nil {
					frameSW.Send(nil, err)
					return
				}
				msgs = append(msgs, full)
			}
			if !skippedModel && full != nil {
				r.maybeSaveStreamCheckpoint(ctx, icfg, &GraphState{
					Messages:   append([]*schema.Message(nil), msgs...),
					LastOutput: full,
					Vars:       map[string]any{},
				}, NodeTools, round)
			}
			if full == nil || len(full.ToolCalls) == 0 {
				frameSW.Send(&StreamFrame{
					Round: round, Phase: PhaseModel, Node: NodeChatModel,
					Chunk: full, Done: true,
				}, nil)
				return
			}
			if len(rt.toolReturnDirectly) > 0 {
				for _, tc := range full.ToolCalls {
					if _, ok := rt.toolReturnDirectly[tc.Function.Name]; ok {
						frameSW.Send(&StreamFrame{
							Round: round, Phase: PhaseModel, Node: NodeChatModel,
							Chunk: full, Done: true, ToolCalls: full.ToolCalls,
						}, nil)
						return
					}
				}
			}
			frameSW.Send(&StreamFrame{
				Round: round, Phase: PhaseTools, Node: NodeTools,
				ToolCalls: full.ToolCalls,
			}, nil)
			toolMsgs, err := rt.toolNode.Invoke(ctx, full)
			if err != nil {
				if IsInterrupt(err) {
					frameSW.Send(&StreamFrame{
						Round: round, Phase: PhaseTools, Node: NodeTools, Done: true,
					}, err)
					return
				}
				frameSW.Send(nil, err)
				return
			}
			for _, tm := range toolMsgs {
				frameSW.Send(&StreamFrame{
					Round: round, Phase: PhaseTools, Node: NodeTools, Chunk: tm,
				}, nil)
			}
			msgs = append(msgs, toolMsgs...)
			r.maybeSaveStreamCheckpoint(ctx, icfg, &GraphState{
				Messages: append([]*schema.Message(nil), msgs...),
				LastOutput: full,
				Vars: map[string]any{},
			}, NodeChatModel, round+1)
		}
		frameSW.Send(nil, fmt.Errorf("compose: react graph exceeded max rounds (%d)", rt.maxRounds))
	}()
	return &StreamFrameReader{inner: frameSR}, nil
}

func (r *CompiledGraph) streamModelRound(
	ctx context.Context,
	frameSW *schema.StreamWriter[*StreamFrame],
	round int,
	msgs []*schema.Message,
	opts []llm.Option,
) (*schema.Message, error) {
	sr, err := r.react.model.Stream(ctx, msgs, opts...)
	if err != nil {
		start := time.Now()
		full, genErr := r.react.model.Generate(ctx, msgs, opts...)
		if genErr != nil {
			return nil, genErr
		}
		_ = start
		frameSW.Send(&StreamFrame{
			Round: round, Phase: PhaseModel, Node: NodeChatModel, Chunk: full,
			Done: full == nil || len(full.ToolCalls) == 0, ToolCalls: toolCallsOf(full),
		}, nil)
		return full, nil
	}
	var chunks []*schema.Message
	for {
		chunk, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			sr.Close()
			return nil, err
		}
		chunks = append(chunks, chunk)
		frameSW.Send(&StreamFrame{
			Round: round, Phase: PhaseModel, Node: NodeChatModel, Chunk: chunk,
		}, nil)
	}
	sr.Close()
	full, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, err
	}
	return full, nil
}
