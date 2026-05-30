package callback

import "context"

// HandlerBuilder builds a Handler with selected timings (Eino-style).
type HandlerBuilder struct {
	onStartFn               func(ctx context.Context, info *RunInfo, input CallbackInput) context.Context
	onEndFn                 func(ctx context.Context, info *RunInfo, output CallbackOutput) context.Context
	onErrorFn               func(ctx context.Context, info *RunInfo, err error) context.Context
	onEndWithStreamOutputFn func(ctx context.Context, info *RunInfo, output CallbackOutput) context.Context
}

// NewHandlerBuilder creates a builder.
func NewHandlerBuilder() *HandlerBuilder {
	return &HandlerBuilder{}
}

func (b *HandlerBuilder) OnStartFn(fn func(ctx context.Context, info *RunInfo, input CallbackInput) context.Context) *HandlerBuilder {
	b.onStartFn = fn
	return b
}

func (b *HandlerBuilder) OnEndFn(fn func(ctx context.Context, info *RunInfo, output CallbackOutput) context.Context) *HandlerBuilder {
	b.onEndFn = fn
	return b
}

func (b *HandlerBuilder) OnErrorFn(fn func(ctx context.Context, info *RunInfo, err error) context.Context) *HandlerBuilder {
	b.onErrorFn = fn
	return b
}

func (b *HandlerBuilder) OnEndWithStreamOutputFn(fn func(ctx context.Context, info *RunInfo, output CallbackOutput) context.Context) *HandlerBuilder {
	b.onEndWithStreamOutputFn = fn
	return b
}

// Build returns a Handler.
func (b *HandlerBuilder) Build() Handler {
	return &builtHandler{HandlerBuilder: *b}
}

type builtHandler struct {
	HandlerBuilder
}

func (h *builtHandler) OnStart(ctx context.Context, info *RunInfo, input CallbackInput) context.Context {
	if h.onStartFn == nil {
		return ctx
	}
	return h.onStartFn(ctx, info, input)
}

func (h *builtHandler) OnEnd(ctx context.Context, info *RunInfo, output CallbackOutput) context.Context {
	if h.onEndFn == nil {
		return ctx
	}
	return h.onEndFn(ctx, info, output)
}

func (h *builtHandler) OnError(ctx context.Context, info *RunInfo, err error) context.Context {
	if h.onErrorFn == nil {
		return ctx
	}
	return h.onErrorFn(ctx, info, err)
}

func (h *builtHandler) OnEndWithStreamOutput(ctx context.Context, info *RunInfo, output CallbackOutput) context.Context {
	if h.onEndWithStreamOutputFn == nil {
		return ctx
	}
	return h.onEndWithStreamOutputFn(ctx, info, output)
}

func (h *builtHandler) Needed(_ context.Context, _ *RunInfo, timing Timing) bool {
	switch timing {
	case TimingOnStart:
		return h.onStartFn != nil
	case TimingOnEnd:
		return h.onEndFn != nil
	case TimingOnError:
		return h.onErrorFn != nil
	case TimingOnEndWithStreamOutput:
		return h.onEndWithStreamOutputFn != nil
	default:
		return false
	}
}

// NopHandler is a no-op Handler useful as embed base.
type NopHandler struct{}

func (NopHandler) OnStart(ctx context.Context, _ *RunInfo, _ CallbackInput) context.Context {
	return ctx
}
func (NopHandler) OnEnd(ctx context.Context, _ *RunInfo, _ CallbackOutput) context.Context {
	return ctx
}
func (NopHandler) OnError(ctx context.Context, _ *RunInfo, _ error) context.Context { return ctx }
func (NopHandler) OnEndWithStreamOutput(ctx context.Context, _ *RunInfo, _ CallbackOutput) context.Context {
	return ctx
}
