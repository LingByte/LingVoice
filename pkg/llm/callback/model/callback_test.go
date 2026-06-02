package modelcb_test

import (
	"testing"

	modelcb "github.com/LingByte/LingVoice/pkg/llm/callback/model"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestConvCallbacks(t *testing.T) {
	in := &modelcb.CallbackInput{Model: "m"}
	if modelcb.ConvCallbackInput(in) != in {
		t.Fatal("input ptr")
	}
	if modelcb.ConvCallbackInput([]*schema.Message{schema.UserMessage("x")}) == nil {
		t.Fatal("messages input")
	}
	if modelcb.ConvCallbackInput("bad") != nil {
		t.Fatal("bad input")
	}
	msg := schema.AssistantMessage("ok", nil)
	out := modelcb.ConvCallbackOutput(msg)
	if out == nil || out.Message != msg {
		t.Fatal("message output")
	}
	if modelcb.ConvCallbackOutput(&modelcb.CallbackOutput{}) == nil {
		t.Fatal("output ptr")
	}
}
