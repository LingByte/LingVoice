package metrics_test

import (
	"context"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	modelcb "github.com/LingByte/LingVoice/pkg/llm/callback/model"
	"github.com/LingByte/LingVoice/pkg/llm/internal/httputil"
	"github.com/LingByte/LingVoice/pkg/llm/metrics"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestHandler_RecordsErrorClassification(t *testing.T) {
	store := metrics.NewMemoryStore()
	defer store.Close()
	h := metrics.NewHandler(store)

	ctx := context.Background()
	info := &callback.RunInfo{Name: "test", Type: "openai", Component: callback.ComponentChatModel}
	in := &modelcb.CallbackInput{Messages: []*schema.Message{schema.UserMessage("hi")}, Model: "gpt-4o-mini"}
	ctx = h.OnStart(ctx, info, in)
	runID := metrics.RunIDFromContext(ctx)

	err := httputil.NewHTTPError(429, `{"error":{"code":"rate_limit_exceeded"}}`)
	h.OnError(ctx, info, err)

	deadline := time.After(2 * time.Second)
	for {
		rec, ok := store.Get(runID)
		if ok && rec.Error != "" {
			if rec.ErrorType != metrics.ErrorTypeRateLimit {
				t.Fatalf("error_type=%q", rec.ErrorType)
			}
			if rec.ErrorCode != "rate_limit_exceeded" {
				t.Fatalf("error_code=%q", rec.ErrorCode)
			}
			snap := store.Snapshot()
			if snap.ErrorsByType[string(metrics.ErrorTypeRateLimit)] != 1 {
				t.Fatalf("snap=%+v", snap)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("timeout")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestHandler_RecordsTiming(t *testing.T) {
	store := metrics.NewMemoryStore()
	defer store.Close()
	h := metrics.NewHandler(store)

	ctx := context.Background()
	info := &callback.RunInfo{Type: "openai", Component: callback.ComponentChatModel}
	ctx = h.OnStart(ctx, info, &modelcb.CallbackInput{Stream: true, Model: "gpt-4o-mini"})
	runID := metrics.RunIDFromContext(ctx)

	out := &modelcb.CallbackOutput{
		Stream: true,
		Message: func() *schema.Message {
			msg := schema.AssistantMessage("ok", nil)
			msg.ResponseMeta = &schema.ResponseMeta{
				Usage: &schema.TokenUsage{CompletionTokens: 10, TotalTokens: 10},
			}
			return msg
		}(),
		Timing: &modelcb.CallbackTiming{
			UpstreamLatency: 150 * time.Millisecond,
			TTFT:            200 * time.Millisecond,
		},
	}
	h.OnEndWithStreamOutput(ctx, info, out)

	deadline := time.After(2 * time.Second)
	for {
		rec, ok := store.Get(runID)
		if ok && rec.TTFTMs > 0 {
			if rec.UpstreamLatencyMs != 150 {
				t.Fatalf("upstream=%f", rec.UpstreamLatencyMs)
			}
			if rec.TTFTMs != 200 {
				t.Fatalf("ttft=%f", rec.TTFTMs)
			}
			if rec.TokensPerSecond <= 0 {
				t.Fatalf("expected tokens/s > 0, got %f", rec.TokensPerSecond)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("timeout")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
