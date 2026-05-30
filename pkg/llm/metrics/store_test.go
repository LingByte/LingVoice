package metrics_test

import (
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/metrics"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestMemoryStore_AsyncRecord(t *testing.T) {
	store := metrics.NewMemoryStore(metrics.WithQueueSize(8))
	defer store.Close()

	store.Enqueue(metrics.RunRecord{
		ID:        "run-1",
		Model:     "openai/gpt-4o-mini",
		StartedAt: time.Now().Add(-100 * time.Millisecond),
		EndedAt:   time.Now(),
		Duration:  100 * time.Millisecond,
		Usage:     &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	})

	deadline := time.After(2 * time.Second)
	for {
		if _, ok := store.Get("run-1"); ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for async record")
		case <-time.After(10 * time.Millisecond):
		}
	}

	rec, ok := store.Get("run-1")
	if !ok || rec.Usage.TotalTokens != 15 {
		t.Fatalf("record=%+v ok=%v", rec, ok)
	}
	snap := store.Snapshot()
	if snap.TotalRuns != 1 || snap.TotalTokens != 15 {
		t.Fatalf("snap=%+v", snap)
	}
}
