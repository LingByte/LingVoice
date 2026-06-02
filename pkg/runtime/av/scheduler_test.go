package av

import (
	"context"
	"testing"
	"time"

	pmedi "github.com/LingByte/LingVoice/pkg/protocol/media"
)

func TestDualLoopScheduler_MailboxDrain(t *testing.T) {
	s := NewDualLoopScheduler()
	s.Emit(pmedi.SessionEvent{
		Type: pmedi.EventUtteranceFinal,
		Utterance: &pmedi.Utterance{Text: "hi", Final: true},
	})
	evs := s.Mailbox.Drain()
	if len(evs) != 1 || evs[0].Utterance.Text != "hi" {
		t.Fatalf("drain: %+v", evs)
	}
}

func TestDualLoopScheduler_CognitiveLoop(t *testing.T) {
	s := NewDualLoopScheduler()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var got string
	s.OnCognitive = func(ctx context.Context, ev pmedi.SessionEvent) {
		if ev.Utterance != nil {
			got = ev.Utterance.Text
		}
	}
	go s.RunCognitiveLoop(ctx)

	s.Emit(pmedi.SessionEvent{
		Type:      pmedi.EventUtteranceFinal,
		Utterance: &pmedi.Utterance{Text: "hello", Final: true},
	})

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && got == "" {
		time.Sleep(5 * time.Millisecond)
	}
	if got != "hello" {
		t.Fatalf("cognitive handler got %q", got)
	}
}

func TestDualLoopScheduler_SetPlayingNoRecursion(t *testing.T) {
	s := NewDualLoopScheduler()
	calls := 0
	s.OnRealtime = func(ctx context.Context, ev pmedi.SessionEvent) {
		calls++
	}
	s.SetPlaying(true)
	s.SetPlaying(true)
	if calls != 0 {
		t.Fatalf("SetPlaying should not invoke realtime handler, calls=%d", calls)
	}
	if !s.IsPlaying() {
		t.Fatal("expected playing")
	}
}
