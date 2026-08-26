package control

import (
	"testing"
	"time"
)

// ─── TurnManager 测试 ──────────────────────────────────────────────────────────

func TestTurnState_String(t *testing.T) {
	assertStr := func(s TurnState, want string) {
		if got := s.String(); got != want {
			t.Errorf("TurnState(%d).String() = %q, want %q", s, got, want)
		}
	}
	assertStr(TurnIdle, "idle")
	assertStr(TurnListening, "listening")
	assertStr(TurnEnding, "ending")
	assertStr(TurnState(99), "unknown")
}

func TestTurnEvent_String(t *testing.T) {
	assertStr := func(e TurnEvent, want string) {
		if got := e.String(); got != want {
			t.Errorf("TurnEvent(%d).String() = %q, want %q", e, got, want)
		}
	}
	assertStr(TurnEventSpeechStart, "speech-start")
	assertStr(TurnEventSpeechEnd, "speech-end")
	assertStr(TurnEventTurnTimeout, "turn-timeout")
	assertStr(TurnEvent(99), "unknown")
}

func TestDefaultTurnConfig(t *testing.T) {
	cfg := DefaultTurnConfig()
	if cfg.SpeechStartFrames != 3 {
		t.Errorf("SpeechStartFrames = %d, want 3", cfg.SpeechStartFrames)
	}
	if cfg.SilenceEndFrames != 25 {
		t.Errorf("SilenceEndFrames = %d, want 25", cfg.SilenceEndFrames)
	}
	if cfg.MinTurnDuration != 200*time.Millisecond {
		t.Errorf("MinTurnDuration = %v, want 200ms", cfg.MinTurnDuration)
	}
	if cfg.MaxTurnDuration != 30*time.Second {
		t.Errorf("MaxTurnDuration = %v, want 30s", cfg.MaxTurnDuration)
	}
}

func TestNewTurnManager_Defaults(t *testing.T) {
	tm := NewTurnManager(TurnConfig{}, nil)
	if tm.State() != TurnIdle {
		t.Errorf("initial state = %v, want idle", tm.State())
	}
	if tm.IsListening() {
		t.Error("should not be listening initially")
	}
}

func TestTurnManager_SpeechStart(t *testing.T) {
	cfg := TurnConfig{
		SpeechStartFrames: 3,
		SilenceEndFrames:  25,
		MinTurnDuration:   1 * time.Millisecond,
		MaxTurnDuration:   10 * time.Second,
	}
	tm := NewTurnManager(cfg, nil)

	// 前 2 帧语音不触发
	tm.Process(true)
	tm.Process(true)
	if tm.State() != TurnIdle {
		t.Errorf("after 2 speech frames, state = %v, want idle", tm.State())
	}

	// 第 3 帧语音触发 SpeechStart
	events := tm.Process(true)
	if tm.State() != TurnListening {
		t.Errorf("after 3 speech frames, state = %v, want listening", tm.State())
	}
	found := false
	for _, e := range events {
		if e == TurnEventSpeechStart {
			found = true
		}
	}
	if !found {
		t.Error("expected TurnEventSpeechStart in events")
	}
}

func TestTurnManager_SpeechEnd(t *testing.T) {
	cfg := TurnConfig{
		SpeechStartFrames: 1,
		SilenceEndFrames:  2,
		MinTurnDuration:   1 * time.Nanosecond, // 不限制最小时长
		MaxTurnDuration:   10 * time.Second,
	}
	tm := NewTurnManager(cfg, nil)

	// 触发 SpeechStart
	tm.Process(true)
	if tm.State() != TurnListening {
		t.Fatalf("state = %v, want listening", tm.State())
	}

	// 2 帧静音触发 SpeechEnd
	tm.Process(false)
	events := tm.Process(false)
	found := false
	for _, e := range events {
		if e == TurnEventSpeechEnd {
			found = true
		}
	}
	if !found {
		t.Error("expected TurnEventSpeechEnd in events")
	}
	if tm.State() != TurnIdle {
		t.Errorf("after speech end, state = %v, want idle", tm.State())
	}
}

func TestTurnManager_SilenceResetsCounter(t *testing.T) {
	cfg := TurnConfig{
		SpeechStartFrames: 3,
		SilenceEndFrames:  25,
		MinTurnDuration:   1 * time.Millisecond,
		MaxTurnDuration:   10 * time.Second,
	}
	tm := NewTurnManager(cfg, nil)

	// 2 帧语音 + 1 帧静音 → 重置
	tm.Process(true)
	tm.Process(true)
	tm.Process(false)
	if tm.State() != TurnIdle {
		t.Errorf("state = %v, want idle", tm.State())
	}

	// 再 2 帧语音不触发（计数器已重置）
	tm.Process(true)
	tm.Process(true)
	if tm.State() != TurnIdle {
		t.Errorf("state = %v, want idle (counter was reset)", tm.State())
	}

	// 第 3 帧触发
	tm.Process(true)
	if tm.State() != TurnListening {
		t.Errorf("state = %v, want listening", tm.State())
	}
}

func TestTurnManager_Reset(t *testing.T) {
	cfg := TurnConfig{
		SpeechStartFrames: 1,
		SilenceEndFrames:  25,
		MinTurnDuration:   1 * time.Millisecond,
		MaxTurnDuration:   10 * time.Second,
	}
	tm := NewTurnManager(cfg, nil)

	// 进入 Listening
	tm.Process(true)
	if tm.State() != TurnListening {
		t.Fatalf("state = %v, want listening", tm.State())
	}

	tm.Reset()
	if tm.State() != TurnIdle {
		t.Errorf("after Reset, state = %v, want idle", tm.State())
	}
}

func TestTurnManager_Callback(t *testing.T) {
	cfg := TurnConfig{
		SpeechStartFrames: 1,
		SilenceEndFrames:  1,
		MinTurnDuration:   1 * time.Nanosecond,
		MaxTurnDuration:   10 * time.Second,
	}

	var receivedEvents []TurnEvent
	tm := NewTurnManager(cfg, func(e TurnEvent) {
		receivedEvents = append(receivedEvents, e)
	})

	tm.Process(true)  // SpeechStart
	tm.Process(false) // SpeechEnd

	if len(receivedEvents) < 2 {
		t.Fatalf("expected at least 2 callback events, got %d", len(receivedEvents))
	}
	if receivedEvents[0] != TurnEventSpeechStart {
		t.Errorf("first event = %v, want speech-start", receivedEvents[0])
	}
}

func TestTurnManager_TurnDuration(t *testing.T) {
	cfg := TurnConfig{
		SpeechStartFrames: 1,
		SilenceEndFrames:  25,
		MinTurnDuration:   1 * time.Millisecond,
		MaxTurnDuration:   10 * time.Second,
	}
	tm := NewTurnManager(cfg, nil)

	// Idle 时 duration = 0
	if d := tm.TurnDuration(); d != 0 {
		t.Errorf("TurnDuration in idle = %v, want 0", d)
	}

	// 进入 Listening
	tm.Process(true)
	if d := tm.TurnDuration(); d <= 0 {
		t.Errorf("TurnDuration in listening = %v, want > 0", d)
	}
}

func TestTurnManager_MinTurnDuration(t *testing.T) {
	cfg := TurnConfig{
		SpeechStartFrames: 1,
		SilenceEndFrames:  1,
		MinTurnDuration:   1 * time.Hour, // 极大值，确保不满足
		MaxTurnDuration:   10 * time.Second,
	}
	tm := NewTurnManager(cfg, nil)

	// 触发 SpeechStart
	tm.Process(true)
	// 立即静音 → 不满足 MinTurnDuration，不产生 SpeechEnd
	events := tm.Process(false)
	for _, e := range events {
		if e == TurnEventSpeechEnd {
			t.Error("should not get SpeechEnd when turn duration < MinTurnDuration")
		}
	}
	// 但状态仍回到 Idle
	if tm.State() != TurnIdle {
		t.Errorf("state = %v, want idle", tm.State())
	}
}

// ─── SessionState 测试 ─────────────────────────────────────────────────────────

func TestSessionState_String(t *testing.T) {
	assertStr := func(s SessionState, want string) {
		if got := s.String(); got != want {
			t.Errorf("SessionState(%d).String() = %q, want %q", s, got, want)
		}
	}
	assertStr(StateIdle, "idle")
	assertStr(StateListening, "listening")
	assertStr(StateThinking, "thinking")
	assertStr(StateSpeaking, "speaking")
	assertStr(SessionState(99), "unknown")
}

func TestDefaultSessionConfig(t *testing.T) {
	cfg := DefaultSessionConfig()
	if cfg.SampleRate != 16000 {
		t.Errorf("SampleRate = %d, want 16000", cfg.SampleRate)
	}
	if cfg.FrameMs != 20 {
		t.Errorf("FrameMs = %d, want 20", cfg.FrameMs)
	}
	if cfg.Channels != 1 {
		t.Errorf("Channels = %d, want 1", cfg.Channels)
	}
}
