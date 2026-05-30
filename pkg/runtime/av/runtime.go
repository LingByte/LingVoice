package av

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	pmedi "github.com/LingByte/LingVoice/pkg/protocol/media"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
	medi "github.com/LingByte/LingVoice/pkg/media"
)

// CognitiveRunner executes one user turn through a compose Graph.
type CognitiveRunner interface {
	RunTurn(ctx context.Context, utterance string) (assistantText string, err error)
}

// GraphRunner adapts a compiled compose graph to CognitiveRunner.
type GraphRunner struct {
	Compiled *compose.CompiledGraph
}

// NewGraphRunner compiles g once for repeated turn execution.
func NewGraphRunner(g *compose.Graph) (*GraphRunner, error) {
	if g == nil {
		return nil, fmt.Errorf("av: nil graph")
	}
	cg, err := g.Compile()
	if err != nil {
		return nil, err
	}
	return &GraphRunner{Compiled: cg}, nil
}

// RunTurn invokes the graph with the user utterance.
func (g *GraphRunner) RunTurn(ctx context.Context, utterance string) (string, error) {
	if g == nil || g.Compiled == nil {
		return "", fmt.Errorf("av: nil graph runner")
	}
	st, _, err := g.Compiled.Invoke(ctx, []*schema.Message{schema.UserMessage(utterance)},
		compose.WithStateModifier(func(_ context.Context, gs *compose.GraphState) error {
			if gs.Vars == nil {
				gs.Vars = map[string]any{}
			}
			gs.Vars[pmedi.ChannelText] = utterance
			return nil
		}))
	if err != nil {
		return "", err
	}
	if st == nil {
		return "", nil
	}
	if t, ok := st.Vars["assistant_text"].(string); ok && t != "" {
		return t, nil
	}
	if len(st.Messages) == 0 {
		return "", nil
	}
	last := st.Messages[len(st.Messages)-1]
	if last == nil {
		return "", nil
	}
	return last.PlainText(), nil
}

// ASRProvider streams or batch-transcribes uplink audio.
type ASRProvider interface {
	FeedAudio(ctx context.Context, pcm []byte) error
	OnPartial(fn func(text string))
	OnFinal(fn func(text string))
	Reset()
}

// TTSProvider synthesizes assistant text to PCM frames delivered via callback.
type TTSProvider interface {
	Speak(ctx context.Context, text string, onFrame func(pcm []byte, first, last bool) error) error
	Cancel()
}

// SessionRuntime wires MediaSession processors to dual-loop scheduling and LLM.
type SessionRuntime struct {
	Session        *medi.MediaSession
	SessionContext *pmedi.SessionContext
	Scheduler      *DualLoopScheduler
	TurnPolicy     pmedi.TurnPolicy
	ASR            ASRProvider
	TTS            TTSProvider
	Cognitive      CognitiveRunner
	DialogID       string
	sequence       atomic.Int32
	cogCancel      context.CancelFunc
	cogMu          sync.Mutex
}

// NewSessionRuntime attaches orchestration to an existing MediaSession.
func NewSessionRuntime(session *medi.MediaSession, cognitive CognitiveRunner, asr ASRProvider, tts TTSProvider) *SessionRuntime {
	rt := &SessionRuntime{
		Session:   session,
		Scheduler: NewDualLoopScheduler(),
		ASR:       asr,
		TTS:       tts,
		Cognitive: cognitive,
	}
	rt.wireASRCallbacks()
	rt.registerBridgeProcessor()
	return rt
}

func (rt *SessionRuntime) wireASRCallbacks() {
	if rt.ASR == nil {
		return
	}
	rt.ASR.OnPartial(func(text string) {
		rt.handleASRText(text, true)
	})
	rt.ASR.OnFinal(func(text string) {
		rt.handleASRText(text, false)
	})
}

func (rt *SessionRuntime) handleASRText(text string, partial bool) {
	seq := int(rt.sequence.Add(1))
	u := pmedi.Utterance{
		SessionID: rt.sessionID(),
		DialogID:  rt.DialogID,
		Text:      text,
		Partial:   partial,
		Final:     !partial,
		Sequence:  seq,
	}
	playing := rt.Scheduler != nil && rt.Scheduler.IsPlaying()
	tp := rt.TurnPolicy
	if tp == nil {
		tp = PassthroughTurn{}
	}
	var decision *pmedi.TurnDecision
	if partial {
		decision = tp.OnPartial(u, playing)
	} else {
		decision = tp.OnEndpoint(text, playing)
	}
	rt.applyTurnDecision(u, decision, partial, text)
}

func (rt *SessionRuntime) applyTurnDecision(u pmedi.Utterance, d *pmedi.TurnDecision, partial bool, text string) {
	if d == nil {
		if partial {
			rt.emitPartial(u)
		} else if text != "" {
			rt.emitFinal(u)
		}
		return
	}
	if d.SignalBargeIn {
		rt.SignalBargeIn()
	}
	if d.EmitPartial || (partial && text != "") {
		rt.emitPartial(u)
	}
	if d.EmitFinal && text != "" {
		u.Final = true
		u.Partial = false
		rt.emitFinal(u)
	}
}

func (rt *SessionRuntime) emitPartial(u pmedi.Utterance) {
	rt.Scheduler.Emit(pmedi.SessionEvent{
		Type:      pmedi.EventUtterancePartial,
		SessionID: rt.sessionID(),
		Utterance: &u,
	})
}

func (rt *SessionRuntime) emitFinal(u pmedi.Utterance) {
	rt.Scheduler.Emit(pmedi.SessionEvent{
		Type:      pmedi.EventUtteranceFinal,
		SessionID: rt.sessionID(),
		Utterance: &u,
	})
	if rt.TurnPolicy != nil {
		rt.TurnPolicy.Reset()
	}
}

func (rt *SessionRuntime) sessionID() string {
	if rt.Session == nil {
		return ""
	}
	return rt.Session.ID
}

func (rt *SessionRuntime) registerBridgeProcessor() {
	if rt.Session == nil {
		return
	}
	rt.Scheduler.OnCognitive = func(ctx context.Context, ev pmedi.SessionEvent) {
		rt.handleCognitiveEvent(rt.Session.GetContext(), ev)
	}
	rt.Scheduler.OnRealtime = rt.handleRealtimeEvent

	proc := medi.NewPacketProcessor("av-bridge", medi.PriorityHigh, func(ctx context.Context, s *medi.MediaSession, packet medi.MediaPacket) error {
		switch p := packet.(type) {
		case *medi.AudioPacket:
			if p == nil || p.IsSynthesized {
				return nil
			}
			if rt.ASR != nil {
				return rt.ASR.FeedAudio(ctx, p.Payload)
			}
		case *medi.TextPacket:
			if p == nil {
				return nil
			}
			u := pmedi.Utterance{
				SessionID: rt.sessionID(),
				Text:      p.Text,
				Sequence:  p.Sequence,
			}
			playing := rt.Scheduler.IsPlaying()
			tp := rt.TurnPolicy
			if tp == nil {
				tp = PassthroughTurn{}
			}
			if p.IsPartial {
				u.Partial = true
				rt.applyTurnDecision(u, tp.OnPartial(u, playing), true, p.Text)
			} else {
				rt.applyTurnDecision(u, tp.OnEndpoint(p.Text, playing), false, p.Text)
			}
		}
		return nil
	})
	rt.Session.RegisterProcessor(proc)
}

func (rt *SessionRuntime) handleRealtimeEvent(ctx context.Context, ev pmedi.SessionEvent) {
	switch ev.Type {
	case pmedi.EventBargeIn:
		rt.cancelCognitive()
		if rt.TTS != nil {
			rt.TTS.Cancel()
		}
		rt.Scheduler.SetPlaying(false)
		if rt.Session != nil {
			rt.Session.EmitState(rt, medi.Interruption)
		}
	}
}

func (rt *SessionRuntime) handleCognitiveEvent(ctx context.Context, ev pmedi.SessionEvent) {
	switch ev.Type {
	case pmedi.EventUtteranceFinal:
		if ev.Utterance == nil || ev.Utterance.Text == "" {
			return
		}
		rt.runCognitiveTurn(ctx, ev.Utterance.Text)
	case pmedi.EventHangup:
		rt.cancelCognitive()
	}
}

func (rt *SessionRuntime) runCognitiveTurn(parent context.Context, userText string) {
	rt.cancelCognitive()
	ctx, cancel := context.WithCancel(parent)
	rt.cogMu.Lock()
	rt.cogCancel = cancel
	rt.cogMu.Unlock()

	go func() {
		defer cancel()
		if rt.Cognitive == nil {
			return
		}
		reply, err := rt.Cognitive.RunTurn(ctx, userText)
		if err != nil || reply == "" {
			return
		}
		rt.playReply(ctx, reply)
	}()
}

func (rt *SessionRuntime) playReply(ctx context.Context, text string) {
	if rt.TTS == nil || rt.Session == nil {
		return
	}
	rt.Scheduler.SetPlaying(true)
	defer rt.Scheduler.SetPlaying(false)

	playID := fmt.Sprintf("play-%d", rt.sequence.Add(1))
	err := rt.TTS.Speak(ctx, text, func(pcm []byte, first, last bool) error {
		rt.Session.SendToOutput(rt, &medi.AudioPacket{
			PlayID:        playID,
			Payload:       pcm,
			IsFirstPacket: first,
			IsEndPacket:   last,
			IsSynthesized: true,
			SourceText:    text,
		})
		return nil
	})
	if err != nil && ctx.Err() == nil {
		rt.Session.CauseError(rt, err)
	}
}

func (rt *SessionRuntime) cancelCognitive() {
	rt.cogMu.Lock()
	defer rt.cogMu.Unlock()
	if rt.cogCancel != nil {
		rt.cogCancel()
		rt.cogCancel = nil
	}
}

// StartCognitiveWorker launches the cognitive mailbox drain loop.
func (rt *SessionRuntime) StartCognitiveWorker(ctx context.Context) {
	go rt.Scheduler.RunCognitiveLoop(ctx)
}

// SignalBargeIn enqueues a barge-in for the realtime loop.
func (rt *SessionRuntime) SignalBargeIn() {
	rt.Scheduler.Emit(pmedi.SessionEvent{
		Type:      pmedi.EventBargeIn,
		SessionID: rt.sessionID(),
	})
}
