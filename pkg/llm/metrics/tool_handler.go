package metrics

import (
	"context"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	toolcb "github.com/LingByte/LingVoice/pkg/llm/callback/tool"
)

type toolStartKey struct{}

// ToolRunRecord is one tool invocation metric snapshot.
type ToolRunRecord struct {
	ID         string    `json:"id"`
	ToolName   string    `json:"tool_name"`
	CallID     string    `json:"call_id,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at"`
	DurationMs float64   `json:"duration_ms"`
	Error      string    `json:"error,omitempty"`
	Response   string    `json:"response,omitempty"`
}

// ToolHandler records tool invocations into Store (Eino tool callback + metrics).
type ToolHandler struct {
	Store *MemoryStore
}

// NewToolHandler creates a tool metrics handler.
func NewToolHandler(store *MemoryStore) *ToolHandler {
	if store == nil {
		store = Default
	}
	return &ToolHandler{Store: store}
}

func (h *ToolHandler) OnStart(ctx context.Context, info *callback.RunInfo, input callback.CallbackInput) context.Context {
	in := toolcb.ConvCallbackInput(input)
	rec := ToolRunRecord{
		ID:        newRunID(),
		StartedAt: time.Now(),
	}
	if in != nil {
		rec.ToolName = in.Name
		rec.CallID = in.CallID
	}
	if info != nil && rec.ToolName == "" {
		rec.ToolName = info.Name
	}
	return context.WithValue(ctx, toolStartKey{}, rec)
}

func (h *ToolHandler) OnEnd(ctx context.Context, _ *callback.RunInfo, output callback.CallbackOutput) context.Context {
	h.finish(ctx, output, nil)
	return ctx
}

func (h *ToolHandler) OnError(ctx context.Context, _ *callback.RunInfo, err error) context.Context {
	h.finish(ctx, nil, err)
	return ctx
}

func (h *ToolHandler) OnEndWithStreamOutput(ctx context.Context, _ *callback.RunInfo, output callback.CallbackOutput) context.Context {
	h.finish(ctx, output, nil)
	return ctx
}

func (h *ToolHandler) Needed(_ context.Context, _ *callback.RunInfo, timing callback.Timing) bool {
	switch timing {
	case callback.TimingOnStart, callback.TimingOnEnd, callback.TimingOnError:
		return true
	default:
		return false
	}
}

func (h *ToolHandler) finish(ctx context.Context, output callback.CallbackOutput, err error) {
	rec, _ := ctx.Value(toolStartKey{}).(ToolRunRecord)
	if rec.ID == "" || h.Store == nil {
		return
	}
	rec.EndedAt = time.Now()
	rec.DurationMs = float64(rec.EndedAt.Sub(rec.StartedAt)) / float64(time.Millisecond)
	if out := toolcb.ConvCallbackOutput(output); out != nil {
		rec.Response = truncateStr(out.Response, 512)
		if out.Error != "" {
			rec.Error = out.Error
		}
	}
	if err != nil {
		rec.Error = err.Error()
	}
	h.Store.EnqueueTool(rec)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
