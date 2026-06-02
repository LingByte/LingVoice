package session_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/session"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestConversation_AppendAndPending(t *testing.T) {
	c := &session.Conversation{ID: "c1", Vars: map[string]any{"x": 1}}
	c.AppendUser("hi")
	c.AppendAssistant(schema.AssistantMessage("ok", nil))
	if len(c.Messages) != 2 {
		t.Fatalf("msgs=%d", len(c.Messages))
	}
	c.SetPending("cp1", map[string]string{"k": "v"})
	if !c.HasPending() {
		t.Fatal("expected pending")
	}
	cl := c.Clone()
	if cl.ID != c.ID || cl.Pending == nil {
		t.Fatal("clone failed")
	}
	c.ClearPending()
	if c.HasPending() {
		t.Fatal("expected cleared")
	}
}

func TestMemoryStore_GetSaveGetOrCreate(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	if err := store.Save(ctx, &session.Conversation{ID: "s1", Vars: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(ctx, "s1")
	if err != nil || !ok || got.ID != "s1" {
		t.Fatalf("get=%+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, _ := store.Get(ctx, "missing"); ok {
		t.Fatal("expected missing")
	}
	c := store.GetOrCreate(ctx, "new")
	if c.ID != "new" {
		t.Fatalf("id=%q", c.ID)
	}
	if err := store.Save(ctx, nil); err == nil {
		t.Fatal("expected error for nil conv")
	}
	if err := store.Save(ctx, &session.Conversation{}); err == nil {
		t.Fatal("expected error for empty id")
	}
}
