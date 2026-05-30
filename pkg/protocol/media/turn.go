package media

// TurnDecision is returned by TurnPolicy to drive mailbox emissions.
type TurnDecision struct {
	EmitPartial bool
	EmitFinal   bool
	SignalBargeIn bool
	// SuppressCognitive skips Graph invoke even when EmitFinal is true (e.g. handoff mode).
	SuppressCognitive bool
}

// TurnPolicy decides when partial ASR becomes a cognitive trigger.
// Implementations are swappable; scenarios differ only by policy parameters.
type TurnPolicy interface {
	OnPartial(u Utterance, assistantPlaying bool) *TurnDecision
	OnEndpoint(text string, assistantPlaying bool) *TurnDecision
	Reset()
}
