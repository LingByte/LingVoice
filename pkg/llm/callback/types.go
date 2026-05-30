package callback

import "context"

// Component kind constants (align with Eino component categories).
const (
	ComponentChatModel = "ChatModel"
	ComponentChain     = "Chain"
	ComponentGraph     = "Graph"
	ComponentTool      = "Tool"
	ComponentPrompt    = "Prompt"
)

// RunInfo describes the entity that triggered a callback.
type RunInfo struct {
	Name      string
	Type      string // implementation id, e.g. "openai"
	Component string // ComponentChatModel, …
}

// CallbackInput is passed to OnStart handlers (untyped; use model.ConvCallbackInput).
type CallbackInput = any

// CallbackOutput is passed to OnEnd handlers.
type CallbackOutput = any

// Timing enumerates callback lifecycle moments.
type Timing int

const (
	TimingOnStart Timing = iota
	TimingOnEnd
	TimingOnError
	TimingOnEndWithStreamOutput
)

// Handler observes component execution.
type Handler interface {
	OnStart(ctx context.Context, info *RunInfo, input CallbackInput) context.Context
	OnEnd(ctx context.Context, info *RunInfo, output CallbackOutput) context.Context
	OnError(ctx context.Context, info *RunInfo, err error) context.Context
	OnEndWithStreamOutput(ctx context.Context, info *RunInfo, output CallbackOutput) context.Context
}

// TimingChecker allows skipping unused timings.
type TimingChecker interface {
	Needed(ctx context.Context, info *RunInfo, timing Timing) bool
}

// GlobalHandlers run for every instrumented invocation (set at init, not concurrent-safe).
var GlobalHandlers []Handler

// AppendGlobalHandlers registers process-wide handlers.
func AppendGlobalHandlers(handlers ...Handler) {
	GlobalHandlers = append(GlobalHandlers, handlers...)
}
