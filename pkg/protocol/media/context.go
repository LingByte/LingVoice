package media

// TrackKind identifies a media track direction and modality.
type TrackKind string

const (
	TrackAudioUplink   TrackKind = "audio.uplink"
	TrackAudioDownlink TrackKind = "audio.downlink"
	TrackVideoUplink   TrackKind = "video.uplink"
	TrackVideoDownlink TrackKind = "video.downlink"
)

// TrackRef binds a transport track to a participant.
type TrackRef struct {
	ID            string    `json:"id"`
	Kind          TrackKind `json:"kind"`
	ParticipantID string    `json:"participantId,omitempty"`
	Label         string    `json:"label,omitempty"`
}

// ControlSignal is a control-plane command on the session (handoff, cancel, etc.).
type ControlSignal string

const (
	ControlCancelGraph  ControlSignal = "cancel.graph"
	ControlHandoffStart ControlSignal = "handoff.start"
	ControlHandoffEnd   ControlSignal = "handoff.end"
	ControlScriptNext   ControlSignal = "script.next"
	ControlScriptJump   ControlSignal = "script.jump"
)

// SessionContext holds cross-turn session state shared by runtime and graph.
type SessionContext struct {
	SessionID string         `json:"sessionId"`
	DialogID  string         `json:"dialogId,omitempty"`
	Vars      map[string]any `json:"vars,omitempty"`
	Tracks    map[string]TrackRef `json:"tracks,omitempty"`
	Caps      CapabilitySet  `json:"-"`
}

// NewSessionContext creates a context for sessionID with the given capabilities.
func NewSessionContext(sessionID string, caps CapabilitySet) *SessionContext {
	return &SessionContext{
		SessionID: sessionID,
		Vars:      map[string]any{},
		Tracks:    map[string]TrackRef{},
		Caps:      caps,
	}
}

// SetControl stores a control signal in Vars[ChannelControl].
func (c *SessionContext) SetControl(sig ControlSignal) {
	if c == nil {
		return
	}
	if c.Vars == nil {
		c.Vars = map[string]any{}
	}
	c.Vars[ChannelControl] = string(sig)
}

// Control returns the latest control signal, if any.
func (c *SessionContext) Control() ControlSignal {
	if c == nil || c.Vars == nil {
		return ""
	}
	s, _ := c.Vars[ChannelControl].(string)
	return ControlSignal(s)
}
