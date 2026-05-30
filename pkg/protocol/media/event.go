package media

import "time"

// SessionEventType classifies orchestration events crossing the realtime/cognitive boundary.
type SessionEventType string

const (
	EventUtterancePartial SessionEventType = "utterance.partial"
	EventUtteranceFinal   SessionEventType = "utterance.final"
	EventTurnStart        SessionEventType = "turn.start"
	EventTurnEnd          SessionEventType = "turn.end"
	EventBargeIn          SessionEventType = "barge_in"
	EventPlaybackStart    SessionEventType = "playback.start"
	EventPlaybackStop     SessionEventType = "playback.stop"
	EventHangup           SessionEventType = "hangup"
)

// Utterance is one ASR text segment (partial or final).
type Utterance struct {
	SessionID string `json:"sessionId"`
	DialogID  string `json:"dialogId,omitempty"`
	Text      string `json:"text"`
	Partial   bool   `json:"partial"`
	Final     bool   `json:"final"`
	Sequence  int    `json:"sequence"`
}

// PlaybackCue requests downlink audio synthesis or carries synthesized PCM.
type PlaybackCue struct {
	SessionID string `json:"sessionId"`
	PlayID    string `json:"playId,omitempty"`
	Text      string `json:"text,omitempty"`
	AudioPCM  []byte `json:"-"`
	First     bool   `json:"first"`
	Last      bool   `json:"last"`
	Cancel    bool   `json:"cancel,omitempty"`
}

// SessionEvent is the mailbox unit between realtime and cognitive loops.
type SessionEvent struct {
	Type      SessionEventType `json:"type"`
	Time      time.Time        `json:"time"`
	SessionID string           `json:"sessionId"`
	Utterance *Utterance       `json:"utterance,omitempty"`
	Playback  *PlaybackCue     `json:"playback,omitempty"`
	Meta      map[string]any   `json:"meta,omitempty"`
}

// LoopKind identifies which scheduler loop owns an event handler.
type LoopKind int

const (
	LoopRealtime LoopKind = iota
	LoopCognitive
)

// Graph channel keys used by compose media nodes.
const (
	ChannelText    = "text"
	ChannelEvent   = "event"
	ChannelAudio   = "audio"
	ChannelControl = "control"
)
