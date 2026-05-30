package instrument_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	"github.com/LingByte/LingVoice/pkg/llm/instrument"
	"github.com/LingByte/LingVoice/pkg/llm/metrics"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type toolCallingModel struct {
	llm.ChatModel
	tools []*schema.ToolInfo
}

func (m *toolCallingModel) WithTools(tools []*schema.ToolInfo) (llm.ToolCallingChatModel, error) {
	return &toolCallingModel{ChatModel: m.ChatModel, tools: tools}, nil
}

func TestWrapConfig_NilInner(t *testing.T) {
	if instrument.WrapConfig(instrument.Config{Inner: nil}) != nil {
		t.Fatal("expected nil")
	}
}

func TestWrap_WithTools(t *testing.T) {
	inner := &toolCallingModel{
		ChatModel: llm.NewFuncModel("openai/t", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
			return schema.AssistantMessage("ok", nil), nil
		}, nil),
	}
	m := instrument.WrapConfig(instrument.Config{Inner: inner, Handlers: []callback.Handler{}})
	tc, ok := m.(llm.ToolCallingChatModel)
	if !ok {
		t.Fatal("expected tool calling model")
	}
	bound, err := tc.WithTools([]*schema.ToolInfo{{Name: "add"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := bound.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil || out.Content != "ok" {
		t.Fatalf("err=%v out=%v", err, out)
	}
}

func TestWrap_StreamError(t *testing.T) {
	store := metrics.NewMemoryStore()
	defer store.Close()
	inner := llm.NewFuncModel("openai/err", nil, func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.StreamReader[*schema.Message], error) {
		return nil, context.Canceled
	})
	m := instrument.Wrap(inner, metrics.NewHandler(store))
	_, err := m.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err == nil {
		t.Fatal("expected stream error")
	}
}

func TestWrap_StreamConcatError(t *testing.T) {
	store := metrics.NewMemoryStore()
	defer store.Close()
	inner := llm.NewFuncModel("openai/bad-chunk", nil, func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.StreamReader[*schema.Message], error) {
		sr, sw := schema.Pipe[*schema.Message](1)
		go func() {
			defer sw.Close()
			idx := 0
			sw.Send(&schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &idx, ID: "1"}}}, nil)
			sw.Send(&schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &idx, ID: "2"}}}, nil)
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
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			break
		}
	}
	sr.Close()
	deadline := time.After(2 * time.Second)
	for {
		if rec, ok := store.Get(slot.ID); ok && rec.Error != "" {
			return
		}
		select {
		case <-deadline:
			t.Fatal("expected error metric")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestWrap_NilModelName(t *testing.T) {
	m := instrument.Wrap(llm.NewFuncModel("", func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("x", nil), nil
	}, nil))
	if m.Name() != "" {
		t.Fatalf("name=%q", m.Name())
	}
	var nilM *instrument.Model
	if nilM.Name() != "" {
		t.Fatal("nil name")
	}
}
