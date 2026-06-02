package av

import (
	"strings"
	"sync"

	pmedi "github.com/LingByte/LingVoice/pkg/protocol/media"
)

// PassthroughTurn forwards ASR partial/final events unchanged.
type PassthroughTurn struct{}

func (PassthroughTurn) OnPartial(u pmedi.Utterance, _ bool) *pmedi.TurnDecision {
	if strings.TrimSpace(u.Text) == "" {
		return nil
	}
	return &pmedi.TurnDecision{EmitPartial: true}
}

func (PassthroughTurn) OnEndpoint(text string, _ bool) *pmedi.TurnDecision {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return &pmedi.TurnDecision{EmitFinal: true}
}

func (PassthroughTurn) Reset() {}

// EndpointTurnGate finalizes a turn when ASR signals end-of-utterance.
// Barge-in while assistant is playing is delegated to VAD (CapBargeIn).
type EndpointTurnGate struct {
	mu sync.Mutex
}

func (g *EndpointTurnGate) OnPartial(u pmedi.Utterance, assistantPlaying bool) *pmedi.TurnDecision {
	if strings.TrimSpace(u.Text) == "" {
		return nil
	}
	d := &pmedi.TurnDecision{EmitPartial: true}
	if assistantPlaying {
		// Partial uplink during playback may indicate barge-in; VAD confirms energy.
		_ = d
	}
	return d
}

func (g *EndpointTurnGate) OnEndpoint(text string, _ bool) *pmedi.TurnDecision {
	g.mu.Lock()
	defer g.mu.Unlock()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return &pmedi.TurnDecision{EmitFinal: true}
}

func (g *EndpointTurnGate) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
}
