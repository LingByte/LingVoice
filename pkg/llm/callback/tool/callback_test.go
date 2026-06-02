package toolcb_test

import (
	"testing"

	toolcb "github.com/LingByte/LingVoice/pkg/llm/callback/tool"
)

func TestConvCallbacks(t *testing.T) {
	in := &toolcb.CallbackInput{Name: "add"}
	if toolcb.ConvCallbackInput(in) != in {
		t.Fatal("input ptr")
	}
	if toolcb.ConvCallbackInput("bad") != nil {
		t.Fatal("bad input")
	}
	out := &toolcb.CallbackOutput{Response: "ok"}
	if toolcb.ConvCallbackOutput(out) != out {
		t.Fatal("output ptr")
	}
	if toolcb.ConvCallbackOutput("bad") != nil {
		t.Fatal("bad output")
	}
}
