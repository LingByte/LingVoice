package compose_test

import (
	"context"
	"io"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type mockStreamModel struct {
	round int
}

func (m *mockStreamModel) Name() string { return "mock/stream" }

func (m *mockStreamModel) Generate(_ context.Context, _ []*schema.Message, _ ...llm.Option) (*schema.Message, error) {
	m.round++
	if m.round == 1 {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "c1", Type: "function",
			Function: schema.FunctionCall{Name: "add", Arguments: `{"a":1,"b":1}`},
		}}), nil
	}
	return schema.AssistantMessage("stream done", nil), nil
}

func (m *mockStreamModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...llm.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](4)
	go func() {
		defer sw.Close()
		if out.Content != "" {
			sw.Send(schema.AssistantMessage("stream ", nil), nil)
			sw.Send(schema.AssistantMessage("done", nil), nil)
		} else {
			sw.Send(out, nil)
		}
	}()
	return sr, nil
}

func (m *mockStreamModel) WithTools(_ []*schema.ToolInfo) (llm.ToolCallingChatModel, error) {
	return m, nil
}

func drainFrames(sr *compose.StreamFrameReader) (int, error) {
	n := 0
	for {
		f, err := sr.Recv()
		if err != nil {
			if err == io.EOF {
				return n, nil
			}
			return n, err
		}
		if f != nil {
			n++
			if f.Done {
				return n, nil
			}
		}
	}
}
