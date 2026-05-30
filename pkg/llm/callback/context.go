package callback

import "context"

type ctxKey struct{}

type runState struct {
	info     *RunInfo
	handlers []Handler
}

// InitRun attaches run info and handlers to ctx for one component invocation.
func InitRun(ctx context.Context, info *RunInfo, handlers ...Handler) context.Context {
	all := make([]Handler, 0, len(GlobalHandlers)+len(handlers))
	all = append(all, GlobalHandlers...)
	all = append(all, handlers...)
	return context.WithValue(ctx, ctxKey{}, &runState{info: info, handlers: all})
}

// WithHandlers adds per-invocation handlers (in addition to GlobalHandlers).
func WithHandlers(ctx context.Context, handlers ...Handler) context.Context {
	st, ok := ctx.Value(ctxKey{}).(*runState)
	if !ok || st == nil {
		return InitRun(ctx, &RunInfo{}, handlers...)
	}
	st.handlers = append(st.handlers, handlers...)
	return ctx
}

func handlersFrom(ctx context.Context) ([]Handler, *RunInfo) {
	st, ok := ctx.Value(ctxKey{}).(*runState)
	if !ok || st == nil {
		return GlobalHandlers, nil
	}
	return st.handlers, st.info
}

func needed(h Handler, ctx context.Context, info *RunInfo, t Timing) bool {
	if c, ok := h.(TimingChecker); ok {
		return c.Needed(ctx, info, t)
	}
	return true
}

// OnStart invokes OnStart on all handlers.
func OnStart(ctx context.Context, input CallbackInput) context.Context {
	hs, info := handlersFrom(ctx)
	for _, h := range hs {
		if h == nil || !needed(h, ctx, info, TimingOnStart) {
			continue
		}
		ctx = h.OnStart(ctx, info, input)
	}
	return ctx
}

// OnEnd invokes OnEnd on all handlers.
func OnEnd(ctx context.Context, output CallbackOutput) context.Context {
	hs, info := handlersFrom(ctx)
	for _, h := range hs {
		if h == nil || !needed(h, ctx, info, TimingOnEnd) {
			continue
		}
		ctx = h.OnEnd(ctx, info, output)
	}
	return ctx
}

// OnError invokes OnError on all handlers.
func OnError(ctx context.Context, err error) context.Context {
	hs, info := handlersFrom(ctx)
	for _, h := range hs {
		if h == nil || !needed(h, ctx, info, TimingOnError) {
			continue
		}
		ctx = h.OnError(ctx, info, err)
	}
	return ctx
}

// OnEndWithStreamOutput invokes stream completion handlers.
func OnEndWithStreamOutput(ctx context.Context, output CallbackOutput) context.Context {
	hs, info := handlersFrom(ctx)
	for _, h := range hs {
		if h == nil || !needed(h, ctx, info, TimingOnEndWithStreamOutput) {
			continue
		}
		ctx = h.OnEndWithStreamOutput(ctx, info, output)
	}
	return ctx
}
