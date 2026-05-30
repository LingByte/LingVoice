package callback_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
)

type skipAll struct {
	callback.NopHandler
	called bool
}

func (s *skipAll) OnStart(ctx context.Context, _ *callback.RunInfo, _ callback.CallbackInput) context.Context {
	s.called = true
	return ctx
}

func (s *skipAll) Needed(_ context.Context, _ *callback.RunInfo, _ callback.Timing) bool {
	return false
}

func TestInitRun_TimingFilter(t *testing.T) {
	h := &skipAll{}
	ctx := callback.InitRun(context.Background(), &callback.RunInfo{}, h)
	callback.OnStart(ctx, nil)
	if h.called {
		t.Fatal("handler with Needed=false should skip OnStart")
	}
}

func TestInitRun_OnEndErrorStream(t *testing.T) {
	var end, errCalled, stream bool
	h := callback.NewHandlerBuilder().
		OnEndFn(func(ctx context.Context, _ *callback.RunInfo, _ callback.CallbackOutput) context.Context {
			end = true
			return ctx
		}).
		OnErrorFn(func(ctx context.Context, _ *callback.RunInfo, _ error) context.Context {
			errCalled = true
			return ctx
		}).
		OnEndWithStreamOutputFn(func(ctx context.Context, _ *callback.RunInfo, _ callback.CallbackOutput) context.Context {
			stream = true
			return ctx
		}).
		Build()

	ctx := callback.InitRun(context.Background(), &callback.RunInfo{Name: "g"}, h)
	callback.OnEnd(ctx, nil)
	callback.OnError(ctx, errors.New("boom"))
	callback.OnEndWithStreamOutput(ctx, nil)
	if !end || !errCalled || !stream {
		t.Fatalf("end=%v err=%v stream=%v", end, errCalled, stream)
	}
}

func TestAppendGlobalHandlers(t *testing.T) {
	prev := callback.GlobalHandlers
	t.Cleanup(func() { callback.GlobalHandlers = prev })

	var called bool
	callback.AppendGlobalHandlers(callback.NewHandlerBuilder().OnStartFn(func(ctx context.Context, _ *callback.RunInfo, _ callback.CallbackInput) context.Context {
		called = true
		return ctx
	}).Build())
	ctx := callback.InitRun(context.Background(), &callback.RunInfo{Name: "global"})
	callback.OnStart(ctx, nil)
	if !called {
		t.Fatal("global handler not invoked")
	}
}

func TestHandlerBuilder_NeededDefaults(t *testing.T) {
	h := callback.NewHandlerBuilder().Build()
	checker, ok := h.(callback.TimingChecker)
	if !ok {
		t.Fatal("expected timing checker")
	}
	if checker.Needed(context.Background(), nil, callback.TimingOnEnd) {
		t.Fatal("empty builder should not need OnEnd")
	}
}
