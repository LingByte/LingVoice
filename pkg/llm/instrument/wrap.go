package instrument

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	modelcb "github.com/LingByte/LingVoice/pkg/llm/callback/model"
	"github.com/LingByte/LingVoice/pkg/llm/metrics"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// Config wraps a ChatModel with callbacks and optional run name.
type Config struct {
	Inner    llm.ChatModel
	Name     string
	Handlers []callback.Handler
}

// Model is an instrumented ChatModel (Eino-style aspect around Generate/Stream).
type Model struct {
	inner    llm.ChatModel
	runInfo  callback.RunInfo
	handlers []callback.Handler
}

// Wrap returns an instrumented ChatModel. When handlers is empty, a metrics
// handler writing to metrics.Default is attached automatically.
func Wrap(inner llm.ChatModel, handlers ...callback.Handler) llm.ChatModel {
	return WrapConfig(Config{Inner: inner, Handlers: handlers})
}

// WrapConfig wraps with full config.
func WrapConfig(cfg Config) llm.ChatModel {
	if cfg.Inner == nil {
		return nil
	}
	hs := cfg.Handlers
	if len(hs) == 0 {
		hs = []callback.Handler{metrics.NewHandler(metrics.Default)}
	}
	name := cfg.Name
	if name == "" {
		name = cfg.Inner.Name()
	}
	parts := strings.SplitN(cfg.Inner.Name(), "/", 2)
	typ := parts[0]
	return &Model{
		inner: cfg.Inner,
		runInfo: callback.RunInfo{
			Name:      name,
			Type:      typ,
			Component: callback.ComponentChatModel,
		},
		handlers: hs,
	}
}

func (m *Model) Name() string {
	if m == nil || m.inner == nil {
		return ""
	}
	return m.inner.Name()
}

func (m *Model) Generate(ctx context.Context, input []*schema.Message, opts ...llm.Option) (*schema.Message, error) {
	o := llm.ApplyOptions(opts...)
	modelName := o.Model
	if modelName == "" {
		modelName = m.inner.Name()
	}
	ctx = callback.InitRun(ctx, &m.runInfo, m.handlers...)
	in := &modelcb.CallbackInput{
		Messages: input, Tools: o.Tools, ToolChoice: o.ToolChoice,
		Options: o, Model: modelName, Stream: false,
	}
	ctx = callback.OnStart(ctx, in)

	upstreamStart := time.Now()
	out, err := m.inner.Generate(ctx, input, opts...)
	upstreamLatency := time.Since(upstreamStart)

	if err != nil {
		ctx = callback.OnError(ctx, err)
		return nil, err
	}
	timing := &modelcb.CallbackTiming{
		UpstreamLatency: upstreamLatency,
		TTFT:            upstreamLatency, // non-stream: first output arrives with full response
	}
	cbOut := &modelcb.CallbackOutput{Message: out, Stream: false, Timing: timing}
	if out != nil && out.ResponseMeta != nil {
		cbOut.TokenUsage = metrics.CloneUsage(out.ResponseMeta.Usage)
	}
	ctx = callback.OnEnd(ctx, cbOut)
	_ = ctx
	return out, nil
}

func (m *Model) Stream(ctx context.Context, input []*schema.Message, opts ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
	o := llm.ApplyOptions(opts...)
	modelName := o.Model
	if modelName == "" {
		modelName = m.inner.Name()
	}
	ctx = callback.InitRun(ctx, &m.runInfo, m.handlers...)
	in := &modelcb.CallbackInput{
		Messages: input, Tools: o.Tools, ToolChoice: o.ToolChoice,
		Options: o, Model: modelName, Stream: true,
	}
	ctx = callback.OnStart(ctx, in)

	upstreamStart := time.Now()
	innerSR, err := m.inner.Stream(ctx, input, opts...)
	upstreamReady := time.Since(upstreamStart)
	if err != nil {
		ctx = callback.OnError(ctx, err)
		return nil, err
	}

	outSR, outSW := schema.Pipe[*schema.Message](16)
	go func(runCtx context.Context) {
		defer outSW.Close()
		var chunks []*schema.Message
		var firstTokenAt time.Time
		streamStarted := upstreamStart
		for {
			chunk, err := innerSR.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				callback.OnError(runCtx, err)
				outSW.Send(nil, err)
				return
			}
			if firstTokenAt.IsZero() && hasModelOutput(chunk) {
				firstTokenAt = time.Now()
			}
			chunks = append(chunks, chunk)
			outSW.Send(chunk, nil)
		}
		innerSR.Close()
		full, cerr := schema.ConcatMessages(chunks)
		if cerr != nil {
			callback.OnError(runCtx, cerr)
			return
		}
		timing := &modelcb.CallbackTiming{UpstreamLatency: upstreamReady}
		if !firstTokenAt.IsZero() {
			timing.TTFT = firstTokenAt.Sub(streamStarted)
		}
		cbOut := &modelcb.CallbackOutput{Message: full, Stream: true, Timing: timing}
		if full != nil && full.ResponseMeta != nil {
			cbOut.TokenUsage = metrics.CloneUsage(full.ResponseMeta.Usage)
		}
		callback.OnEndWithStreamOutput(runCtx, cbOut)
	}(ctx)
	return outSR, nil
}

func hasModelOutput(msg *schema.Message) bool {
	if msg == nil {
		return false
	}
	if msg.Content != "" {
		return true
	}
	if len(msg.ToolCalls) > 0 {
		return true
	}
	for _, p := range msg.AssistantOutputParts {
		if p.Text != "" || p.Media != nil {
			return true
		}
	}
	return false
}

func (m *Model) WithTools(tools []*schema.ToolInfo) (llm.ToolCallingChatModel, error) {
	tc, ok := m.inner.(llm.ToolCallingChatModel)
	if !ok {
		return nil, errors.New("instrument: inner model does not support WithTools")
	}
	inner, err := tc.WithTools(tools)
	if err != nil {
		return nil, err
	}
	w := WrapConfig(Config{Inner: inner, Name: m.runInfo.Name, Handlers: m.handlers})
	if m2, ok := w.(*Model); ok {
		return m2, nil
	}
	return nil, errors.New("instrument: unexpected wrap type")
}

var (
	_ llm.ChatModel            = (*Model)(nil)
	_ llm.ToolCallingChatModel = (*Model)(nil)
)
