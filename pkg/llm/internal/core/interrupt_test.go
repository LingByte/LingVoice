package core_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/internal/core"
)

func TestInterrupt_Basics(t *testing.T) {
	err := core.Interrupt(context.Background(), "pause")
	if !core.IsInterrupt(err) {
		t.Fatal("expected interrupt")
	}
	ie, ok := core.AsInterrupt(err)
	if !ok || ie.ID == "" || ie.Info != "pause" {
		t.Fatalf("ie=%+v", ie)
	}
	if ie.Error() != "interrupt" {
		t.Fatalf("err=%q", ie.Error())
	}

	err2 := core.StatefulInterrupt(context.Background(), "info", "state")
	ie2, ok := core.AsInterrupt(err2)
	if !ok || ie2.State != "state" {
		t.Fatalf("ie2=%+v", ie2)
	}

	var nilIE *core.InterruptError
	if nilIE.Error() != "interrupt" {
		t.Fatal("nil interrupt error string")
	}
	ie3 := &core.InterruptError{Node: "tools"}
	if ie3.Error() != `interrupt at "tools"` {
		t.Fatalf("err=%q", ie3.Error())
	}
}

func TestAddress_AndContext(t *testing.T) {
	ctx := core.WithAddress(context.Background(), "graph")
	ctx = core.WithAddress(ctx, "node-a")
	addr := core.CurrentAddress(ctx)
	if addr.String() != "graph/node-a" {
		t.Fatalf("addr=%q", addr.String())
	}
	if (core.Address{}).String() != "" {
		t.Fatal("empty address")
	}

	ctx = core.WithToolCallID(ctx, "call-1")
	if core.ToolCallID(ctx) != "call-1" {
		t.Fatal("tool call id")
	}

	data := map[string]any{"k": "v"}
	ctx = core.WithResumeData(ctx, data)
	if got := core.ResumeData(ctx); got["k"] != "v" {
		t.Fatalf("resume=%v", got)
	}
	if core.WithResumeData(context.Background(), nil) == nil {
		t.Fatal("expected ctx")
	}
}

func TestNewInterruptID_Unique(t *testing.T) {
	a := core.NewInterruptID()
	b := core.NewInterruptID()
	if a == "" || a == b {
		t.Fatalf("ids=%q %q", a, b)
	}
}
