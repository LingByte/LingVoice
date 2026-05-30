package callback_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
)

func TestHandlerBuilder_AllTimings(t *testing.T) {
	var started, ended, errored, streamEnd bool
	h := callback.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callback.RunInfo, input callback.CallbackInput) context.Context {
			started = true
			return ctx
		}).
		OnEndFn(func(ctx context.Context, info *callback.RunInfo, output callback.CallbackOutput) context.Context {
			ended = true
			return ctx
		}).
		OnErrorFn(func(ctx context.Context, info *callback.RunInfo, err error) context.Context {
			errored = true
			return ctx
		}).
		OnEndWithStreamOutputFn(func(ctx context.Context, info *callback.RunInfo, output callback.CallbackOutput) context.Context {
			streamEnd = true
			return ctx
		}).
		Build()

	ctx := callback.InitRun(context.Background(), &callback.RunInfo{Name: "t", Component: callback.ComponentChain})
	ctx = h.OnStart(ctx, &callback.RunInfo{}, nil)
	ctx = h.OnEnd(ctx, &callback.RunInfo{}, nil)
	ctx = h.OnError(ctx, &callback.RunInfo{}, context.Canceled)
	ctx = h.OnEndWithStreamOutput(ctx, &callback.RunInfo{}, nil)
	if !started || !ended || !errored || !streamEnd {
		t.Fatalf("flags start=%v end=%v err=%v stream=%v", started, ended, errored, streamEnd)
	}
	if !h.(interface {
		Needed(context.Context, *callback.RunInfo, callback.Timing) bool
	}).Needed(ctx, nil, callback.TimingOnStart) {
		t.Fatal("expected needed on start")
	}
}

func TestContextHandlers(t *testing.T) {
	var called bool
	h := callback.NewHandlerBuilder().OnStartFn(func(ctx context.Context, _ *callback.RunInfo, _ callback.CallbackInput) context.Context {
		called = true
		return ctx
	}).Build()
	ctx := callback.WithHandlers(context.Background(), h)
	callback.OnStart(ctx, nil)
	if !called {
		t.Fatal("handler not called")
	}
	var nop callback.NopHandler
	ctx = nop.OnStart(context.Background(), nil, nil)
	ctx = nop.OnEnd(ctx, nil, nil)
	ctx = nop.OnError(ctx, nil, nil)
	ctx = nop.OnEndWithStreamOutput(ctx, nil, nil)
	_ = ctx
}
