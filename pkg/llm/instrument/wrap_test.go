package instrument_test

import (
	"context"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/instrument"
	"github.com/LingByte/LingVoice/pkg/llm/metrics"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestWrap_RecordsMetrics(t *testing.T) {
	store := metrics.NewMemoryStore()
	defer store.Close()

	inner := llm.NewFuncModel("openai/test", func(ctx context.Context, input []*schema.Message, opts llm.Options) (*schema.Message, error) {
		msg := schema.AssistantMessage("ok", nil)
		msg.ResponseMeta = &schema.ResponseMeta{
			FinishReason: "stop",
			Usage:        &schema.TokenUsage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4},
		}
		return msg, nil
	}, nil)

	m := instrument.Wrap(inner, metrics.NewHandler(store))
	ctx, slot := metrics.WithRunSlot(context.Background())
	out, err := m.Generate(ctx, []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "ok" {
		t.Fatalf("content=%q", out.Content)
	}
	if slot.ID == "" {
		t.Fatal("expected run slot id")
	}

	deadline := time.After(2 * time.Second)
	for {
		if rec, ok := store.Get(slot.ID); ok && rec.Usage != nil {
			if rec.Usage.TotalTokens != 4 {
				t.Fatalf("usage=%+v", rec.Usage)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("metrics not recorded in time")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestWrap_RecordsStreamMetrics(t *testing.T) {
	store := metrics.NewMemoryStore()
	defer store.Close()

	inner := llm.NewFuncModel("openai/test", nil, func(ctx context.Context, input []*schema.Message, opts llm.Options) (*schema.StreamReader[*schema.Message], error) {
		sr, sw := schema.Pipe[*schema.Message](8)
		go func() {
			defer sw.Close()
			sw.Send(&schema.Message{Role: schema.Assistant, Content: "hello"}, nil)
			sw.Send(&schema.Message{
				Role: schema.Assistant,
				ResponseMeta: &schema.ResponseMeta{
					FinishReason: "stop",
					Usage:        &schema.TokenUsage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6},
				},
			}, nil)
		}()
		return sr, nil
	})

	m := instrument.Wrap(inner, metrics.NewHandler(store))
	ctx, slot := metrics.WithRunSlot(context.Background())
	sr, err := m.Stream(ctx, []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err := sr.Recv()
		if err != nil {
			break
		}
	}
	sr.Close()

	deadline := time.After(2 * time.Second)
	for {
		if rec, ok := store.Get(slot.ID); ok && rec.Usage != nil {
			if rec.Usage.TotalTokens != 6 {
				t.Fatalf("usage=%+v", rec.Usage)
			}
			if rec.TokensPerSecond <= 0 {
				t.Fatalf("tokens/s=%f", rec.TokensPerSecond)
			}
			if rec.TTFTMs <= 0 {
				t.Fatalf("ttft=%f", rec.TTFTMs)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("metrics not recorded in time")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
