package av

import (
	"context"
	"sync"
	"time"

	pmedi "github.com/LingByte/LingVoice/pkg/protocol/media"
)

// Mailbox buffers SessionEvents between realtime and cognitive loops.
type Mailbox struct {
	mu     sync.Mutex
	events []pmedi.SessionEvent
	notify chan struct{}
}

// NewMailbox creates an unbounded in-memory mailbox with coalesced wakeups.
func NewMailbox() *Mailbox {
	return &Mailbox{notify: make(chan struct{}, 1)}
}

// Push appends an event and signals waiters.
func (m *Mailbox) Push(ev pmedi.SessionEvent) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.events = append(m.events, ev)
	m.mu.Unlock()
	select {
	case m.notify <- struct{}{}:
	default:
	}
}

// Drain returns and clears all pending events.
func (m *Mailbox) Drain() []pmedi.SessionEvent {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.events) == 0 {
		return nil
	}
	out := make([]pmedi.SessionEvent, len(m.events))
	copy(out, m.events)
	m.events = m.events[:0]
	return out
}

// Wait blocks until ctx ends or an event arrives.
func (m *Mailbox) Wait(ctx context.Context) error {
	if m == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.notify:
		return nil
	}
}

// DualLoopScheduler coordinates realtime (media) and cognitive (LLM) loops.
type DualLoopScheduler struct {
	Mailbox *Mailbox

	OnRealtime  func(ctx context.Context, ev pmedi.SessionEvent)
	OnCognitive func(ctx context.Context, ev pmedi.SessionEvent)

	playingMu sync.RWMutex
	playing   bool
}

// NewDualLoopScheduler builds a scheduler with an empty mailbox.
func NewDualLoopScheduler() *DualLoopScheduler {
	return &DualLoopScheduler{Mailbox: NewMailbox()}
}

// SetPlaying tracks downlink synthesis for VAD gating.
func (s *DualLoopScheduler) SetPlaying(playing bool) {
	if s == nil {
		return
	}
	s.playingMu.Lock()
	changed := s.playing != playing
	s.playing = playing
	s.playingMu.Unlock()
	if !changed {
		return
	}
	evType := pmedi.EventPlaybackStop
	if playing {
		evType = pmedi.EventPlaybackStart
	}
	s.Mailbox.Push(pmedi.SessionEvent{Type: evType, Time: time.Now()})
}

// IsPlaying reports whether assistant audio is currently playing.
func (s *DualLoopScheduler) IsPlaying() bool {
	if s == nil {
		return false
	}
	s.playingMu.RLock()
	defer s.playingMu.RUnlock()
	return s.playing
}

// Emit publishes an event to the mailbox and dispatches to the appropriate loop.
func (s *DualLoopScheduler) Emit(ev pmedi.SessionEvent) {
	s.emit(ev)
}

func (s *DualLoopScheduler) emit(ev pmedi.SessionEvent) {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	s.Mailbox.Push(ev)
	loop := loopFor(ev.Type)
	ctx := context.Background()
	switch loop {
	case pmedi.LoopRealtime:
		if s.OnRealtime != nil {
			s.OnRealtime(ctx, ev)
		}
	case pmedi.LoopCognitive:
		if s.OnCognitive != nil {
			s.OnCognitive(ctx, ev)
		}
	}
}

func loopFor(t pmedi.SessionEventType) pmedi.LoopKind {
	switch t {
	case pmedi.EventUtterancePartial, pmedi.EventBargeIn, pmedi.EventPlaybackStart, pmedi.EventPlaybackStop:
		return pmedi.LoopRealtime
	default:
		return pmedi.LoopCognitive
	}
}

// RunCognitiveLoop drains the mailbox and invokes OnCognitive until ctx ends.
func (s *DualLoopScheduler) RunCognitiveLoop(ctx context.Context) {
	if s == nil {
		return
	}
	for {
		for _, ev := range s.Mailbox.Drain() {
			if loopFor(ev.Type) != pmedi.LoopCognitive {
				continue
			}
			if s.OnCognitive != nil {
				s.OnCognitive(ctx, ev)
			}
		}
		if err := s.Mailbox.Wait(ctx); err != nil {
			return
		}
	}
}
