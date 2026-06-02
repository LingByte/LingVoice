package compose_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestChain_Invoke(t *testing.T) {
	model := llm.NewFuncModel("test/m", func(ctx context.Context, input []*schema.Message, opts llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("done", nil), nil
	}, nil)

	chain := compose.NewChain("demo",
		compose.PromptStep{Role: schema.System, Content: "be brief"},
		&compose.ChatModelStep{Model: model},
	)
	st, err := chain.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Messages) != 3 {
		t.Fatalf("messages=%d", len(st.Messages))
	}
	if st.LastOutput.Content != "done" {
		t.Fatalf("last=%q", st.LastOutput.Content)
	}
}

func TestChain_RunsRecording(t *testing.T) {
	model := llm.NewFuncModel("test/runs", func(ctx context.Context, input []*schema.Message, opts llm.Options) (*schema.Message, error) {
		return schema.AssistantMessage("ok", nil), nil
	}, nil)

	chain := compose.NewChain("runs",
		&compose.ChatModelStep{Model: model},
		&compose.ChatModelStep{Model: model},
	)
	st, err := chain.Invoke(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	runs := compose.RunsFromState(st)
	if len(runs) != 2 {
		t.Fatalf("runs=%d", len(runs))
	}
	if runs[0].Model != "test/runs" || runs[0].Step != "ChatModel" {
		t.Fatalf("run0=%+v", runs[0])
	}
}
