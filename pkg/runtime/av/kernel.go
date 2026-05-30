package av

import (
	"context"

	pmedi "github.com/LingByte/LingVoice/pkg/protocol/media"
	medi "github.com/LingByte/LingVoice/pkg/media"
)

// Kernel is the generic session orchestrator: profile + runtime + extensions.
type Kernel struct {
	Profile  RuntimeProfile
	Context  *pmedi.SessionContext
	Runtime  *SessionRuntime
	handoff  *HandoffExtension
}

// KernelConfig wires a MediaSession to the generic kernel.
type KernelConfig struct {
	Session   *medi.MediaSession
	Profile   RuntimeProfile
	Cognitive CognitiveRunner
	ASR       ASRProvider
	TTS       TTSProvider
}

// NewKernel builds a session from RuntimeProfile and optional providers.
func NewKernel(cfg KernelConfig) (*Kernel, error) {
	profile := cfg.Profile
	if profile.Name == "" {
		profile = KernelProfile()
	}
	if profile.TurnPolicy == nil {
		profile.TurnPolicy = PassthroughTurn{}
	}
	sessionID := ""
	if cfg.Session != nil {
		sessionID = cfg.Session.ID
	}
	k := &Kernel{
		Profile: profile,
		Context: pmedi.NewSessionContext(sessionID, profile.Caps),
		Runtime: NewSessionRuntime(cfg.Session, cfg.Cognitive, cfg.ASR, cfg.TTS),
	}
	k.Runtime.TurnPolicy = profile.TurnPolicy
	k.Runtime.SessionContext = k.Context

	for _, ext := range profile.Extensions {
		if err := ext.Attach(k); err != nil {
			return nil, err
		}
		if h, ok := ext.(*HandoffExtension); ok {
			k.handoff = h
		}
	}
	if profile.Caps.Has(pmedi.CapHandoff) && k.handoff == nil {
		k.handoff = NewHandoffExtension()
		_ = k.handoff.Attach(k)
		profile.Extensions = append(profile.Extensions, k.handoff)
	}

	k.wrapEventHandlers()
	return k, nil
}

func (k *Kernel) wrapEventHandlers() {
	rt := k.Runtime
	if rt == nil || rt.Scheduler == nil {
		return
	}
	prevRT := rt.Scheduler.OnRealtime
	prevCG := rt.Scheduler.OnCognitive
	rt.Scheduler.OnRealtime = func(ctx context.Context, ev pmedi.SessionEvent) {
		k.dispatchExtensions(ctx, ev)
		if prevRT != nil {
			prevRT(ctx, ev)
		}
	}
	rt.Scheduler.OnCognitive = func(ctx context.Context, ev pmedi.SessionEvent) {
		k.dispatchExtensions(ctx, ev)
		if k.shouldSuppressCognitive(ev) {
			return
		}
		if prevCG != nil {
			prevCG(ctx, ev)
		}
	}
}

func (k *Kernel) dispatchExtensions(ctx context.Context, ev pmedi.SessionEvent) {
	for _, ext := range k.Profile.Extensions {
		_ = ext.OnEvent(ctx, ev)
	}
}

func (k *Kernel) shouldSuppressCognitive(ev pmedi.SessionEvent) bool {
	if ev.Meta != nil {
		if v, ok := ev.Meta["suppress_cognitive"].(bool); ok && v {
			return true
		}
	}
	if k.handoff != nil && k.handoff.Active() && ev.Type == pmedi.EventUtteranceFinal {
		return true
	}
	if k.Context != nil && k.Context.Control() == pmedi.ControlHandoffStart {
		return true
	}
	return false
}

// Start launches cognitive worker and optionally MediaSession.Serve in a goroutine.
func (k *Kernel) Start(ctx context.Context, serveSession bool) {
	if k.Runtime == nil {
		return
	}
	k.Runtime.StartCognitiveWorker(ctx)
	if serveSession && k.Runtime.Session != nil {
		k.Runtime.Session.NotifyServeStarting()
		go func() { _ = k.Runtime.Session.Serve() }()
	}
}

// SetHandoff toggles human-agent mode (suppresses Graph invoke).
func (k *Kernel) SetHandoff(active bool) {
	if k.Context != nil {
		if active {
			k.Context.SetControl(pmedi.ControlHandoffStart)
		} else {
			k.Context.SetControl(pmedi.ControlHandoffEnd)
		}
	}
	if k.handoff != nil {
		k.handoff.active = active
	}
}
