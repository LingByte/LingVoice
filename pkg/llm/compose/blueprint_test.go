package compose_test

import (
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
	pmedi "github.com/LingByte/LingVoice/pkg/protocol/media"
)

func TestGraphBlueprint_KernelOnly(t *testing.T) {
	g, err := (&compose.GraphBlueprint{
		Name:       "test",
		Caps:       pmedi.KernelCapabilities(),
		EchoPrefix: "p:",
	}).Build()
	if err != nil {
		t.Fatal(err)
	}
	cg, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := cg.Invoke(t.Context(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if st == nil {
		t.Fatal("nil state")
	}
}

func TestGraphBlueprint_WithScript(t *testing.T) {
	g, err := (&compose.GraphBlueprint{
		Caps: pmedi.PresetOutbound(),
	}).Build()
	if err != nil {
		t.Fatal(err)
	}
	if g == nil {
		t.Fatal("nil graph")
	}
}
