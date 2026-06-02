package metrics

import (
	"context"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	modelcb "github.com/LingByte/LingVoice/pkg/llm/callback/model"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type startKey struct{}

// Handler records ChatModel runs into Store asynchronously (Eino callback style).
type Handler struct {
	Store Store
}

// NewHandler creates a metrics callback handler targeting store.
func NewHandler(store Store) *Handler {
	if store == nil {
		store = Default
	}
	return &Handler{Store: store}
}

func (h *Handler) OnStart(ctx context.Context, info *callback.RunInfo, input callback.CallbackInput) context.Context {
	in := modelcb.ConvCallbackInput(input)
	rec := RunRecord{
		ID:        newRunID(),
		Component: callback.ComponentChatModel,
		StartedAt: time.Now(),
	}
	if info != nil {
		rec.RunName = info.Name
		rec.Component = info.Component
		rec.ProviderType = info.Type
	}
	if in != nil {
		rec.InputMessages = len(in.Messages)
		rec.Stream = in.Stream
		rec.Model = in.Model
		if rec.Model == "" && in.Options.Model != "" {
			rec.Model = in.Options.Model
		}
	}
	ctx = context.WithValue(ctx, startKey{}, rec)
	ctx = WithRunID(ctx, rec.ID)
	return ctx
}

func (h *Handler) OnEnd(ctx context.Context, info *callback.RunInfo, output callback.CallbackOutput) context.Context {
	h.finish(ctx, info, output, nil)
	return ctx
}

func (h *Handler) OnError(ctx context.Context, info *callback.RunInfo, err error) context.Context {
	h.finish(ctx, info, nil, err)
	return ctx
}

func (h *Handler) OnEndWithStreamOutput(ctx context.Context, info *callback.RunInfo, output callback.CallbackOutput) context.Context {
	h.finish(ctx, info, output, nil)
	return ctx
}

func (h *Handler) Needed(_ context.Context, _ *callback.RunInfo, timing callback.Timing) bool {
	switch timing {
	case callback.TimingOnStart, callback.TimingOnEnd, callback.TimingOnError, callback.TimingOnEndWithStreamOutput:
		return true
	default:
		return false
	}
}

func (h *Handler) finish(ctx context.Context, info *callback.RunInfo, output callback.CallbackOutput, err error) {
	rec, _ := ctx.Value(startKey{}).(RunRecord)
	if rec.ID == "" {
		return
	}
	rec.EndedAt = time.Now()
	rec.Duration = rec.EndedAt.Sub(rec.StartedAt)
	rec.DurationMs = float64(rec.Duration) / float64(time.Millisecond)

	if info != nil && rec.ProviderType == "" {
		rec.ProviderType = info.Type
	}
	out := modelcb.ConvCallbackOutput(output)
	if out != nil {
		if out.Message != nil && out.Message.ResponseMeta != nil {
			rec.FinishReason = out.Message.ResponseMeta.FinishReason
		}
		rec.Usage = CloneUsage(out.TokenUsage)
		if rec.Usage == nil && out.Message != nil && out.Message.ResponseMeta != nil {
			rec.Usage = CloneUsage(out.Message.ResponseMeta.Usage)
		}
		if rec.Usage == nil && out.Message != nil {
			rec.Usage = EstimateUsageFromMessage(out.Message, rec.InputMessages)
		}
		if out.Timing != nil {
			if out.Timing.UpstreamLatency > 0 {
				rec.UpstreamLatencyMs = float64(out.Timing.UpstreamLatency) / float64(time.Millisecond)
			}
			if out.Timing.TTFT > 0 {
				rec.TTFTMs = float64(out.Timing.TTFT) / float64(time.Millisecond)
			}
		}
	}
	if err != nil {
		rec.Error = err.Error()
		rec.ErrorType, rec.ErrorCode = ClassifyError(err)
	}
	if rec.TTFTMs == 0 && rec.UpstreamLatencyMs > 0 {
		rec.TTFTMs = rec.UpstreamLatencyMs
	}
	if rec.Error == "" && rec.Usage != nil && rec.DurationMs > 0 {
		rec.TokensPerSecond = TokensPerSecond(rec)
	}
	go h.Store.Enqueue(rec)
}

func newRunID() string {
	return time.Now().Format("20060102150405.000") + "-" + randHex(4)
}

func randHex(n int) string {
	const hexdigits = "0123456789abcdef"
	b := make([]byte, n)
	now := time.Now().UnixNano()
	for i := range b {
		b[i] = hexdigits[now%16]
		now /= 16
	}
	return string(b)
}

// CloneUsage copies token usage for safe async storage.
func CloneUsage(u *schema.TokenUsage) *schema.TokenUsage {
	if u == nil {
		return nil
	}
	c := *u
	return &c
}
