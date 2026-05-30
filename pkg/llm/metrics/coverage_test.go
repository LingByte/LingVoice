package metrics_test

import (
	"context"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	modelcb "github.com/LingByte/LingVoice/pkg/llm/callback/model"
	toolcb "github.com/LingByte/LingVoice/pkg/llm/callback/tool"
	"github.com/LingByte/LingVoice/pkg/llm/metrics"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestMemoryStore_ListAndTrim(t *testing.T) {
	store := metrics.NewMemoryStore(metrics.WithMaxRecords(2), metrics.WithQueueSize(4))
	defer store.Close()

	for i := 0; i < 3; i++ {
		id := []string{"run-a", "run-b", "run-c"}[i]
		store.Enqueue(metrics.RunRecord{
			ID:        id,
			Model:     "m",
			StartedAt: time.Now(),
			EndedAt:   time.Now(),
			Error:     func() string { if i == 1 { return "fail" }; return "" }(),
			ErrorType: func() metrics.ErrorType { if i == 1 { return metrics.ErrorTypeUnknown }; return "" }(),
		})
	}
	deadline := time.After(2 * time.Second)
	for store.Snapshot().TotalRuns < 3 {
		select {
		case <-deadline:
			t.Fatalf("snap=%+v", store.Snapshot())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if len(store.List(0)) != 2 {
		t.Fatalf("list=%d", len(store.List(0)))
	}
	if _, ok := store.Get("run-a"); ok {
		t.Fatal("oldest should be trimmed")
	}
}

func TestToolHandler_Records(t *testing.T) {
	store := metrics.NewMemoryStore()
	defer store.Close()
	h := metrics.NewToolHandler(store)
	ctx := h.OnStart(context.Background(), &callback.RunInfo{Name: "add"}, &toolcb.CallbackInput{
		Name: "add", CallID: "c1", Arguments: `{"a":1}`,
	})
	h.OnEnd(ctx, nil, &toolcb.CallbackOutput{Response: `{"sum":2}`})

	deadline := time.After(2 * time.Second)
	for store.ToolTotal() == 0 {
		select {
		case <-deadline:
			t.Fatal("timeout")
		case <-time.After(5 * time.Millisecond):
		}
	}
	tools := store.ListTools(1)
	if len(tools) != 1 || tools[0].ToolName != "add" {
		t.Fatalf("tools=%+v", tools)
	}

	ctx2 := h.OnStart(context.Background(), nil, &toolcb.CallbackInput{Name: "bad"})
	h.OnError(ctx2, nil, context.Canceled)
	time.Sleep(20 * time.Millisecond)
	if store.ToolTotal() < 2 {
		t.Fatalf("total=%d", store.ToolTotal())
	}
	if !h.Needed(context.Background(), nil, callback.TimingOnEnd) {
		t.Fatal("needed on end")
	}
	if h.Needed(context.Background(), nil, callback.TimingOnEndWithStreamOutput) {
		t.Fatal("not needed on stream end")
	}
}

func TestEstimateUsageFromMessage(t *testing.T) {
	msg := schema.AssistantMessage("hello world", []schema.ToolCall{{
		Function: schema.FunctionCall{Name: "add", Arguments: `{"a":1}`},
	}})
	u := metrics.EstimateUsageFromMessage(msg, 2)
	if u == nil || u.TotalTokens <= 0 {
		t.Fatalf("usage=%+v", u)
	}
	if metrics.EstimateUsageFromMessage(nil, 0) != nil {
		t.Fatal("nil msg")
	}
	if metrics.EstimateUsageFromMessage(&schema.Message{Role: schema.Assistant}, 0) != nil {
		t.Fatal("empty msg")
	}
}

func TestHandler_OnEndWithUsageEstimate(t *testing.T) {
	store := metrics.NewMemoryStore()
	defer store.Close()
	h := metrics.NewHandler(store)
	ctx := h.OnStart(context.Background(), &callback.RunInfo{Type: "openai"}, nil)
	runID := metrics.RunIDFromContext(ctx)
	h.OnEnd(ctx, nil, nil) // no output — should noop finish

	ctx = h.OnStart(context.Background(), &callback.RunInfo{Type: "openai"}, nil)
	runID = metrics.RunIDFromContext(ctx)
	h.OnEnd(ctx, nil, &modelcb.CallbackOutput{
		Message: schema.AssistantMessage("estimated tokens here", nil),
	})

	deadline := time.After(2 * time.Second)
	for {
		rec, ok := store.Get(runID)
		if ok && rec.Usage != nil {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timeout")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestWithRunSlot(t *testing.T) {
	ctx, slot := metrics.WithRunSlot(context.Background())
	ctx = metrics.WithRunID(ctx, "rid-1")
	if slot.ID != "rid-1" || metrics.RunIDFromContext(ctx) != "rid-1" {
		t.Fatalf("slot=%+v", slot)
	}
}

func TestCloneUsage(t *testing.T) {
	if metrics.CloneUsage(nil) != nil {
		t.Fatal("nil clone")
	}
	u := metrics.CloneUsage(&schema.TokenUsage{TotalTokens: 5})
	if u.TotalTokens != 5 {
		t.Fatal(u)
	}
}
