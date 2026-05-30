package av

import pmedi "github.com/LingByte/LingVoice/pkg/protocol/media"

// RuntimeProfile configures the generic kernel for one session.
type RuntimeProfile struct {
	Name         string
	Caps         pmedi.CapabilitySet
	TurnPolicy   pmedi.TurnPolicy
	Extensions   []Extension
}

// KernelProfile returns the default voice kernel (all scenarios build on this).
func KernelProfile() RuntimeProfile {
	return RuntimeProfile{
		Name:       "kernel",
		Caps:       pmedi.KernelCapabilities(),
		TurnPolicy: PassthroughTurn{},
	}
}

// WithCapabilities returns a copy of p with merged capabilities.
func (p RuntimeProfile) WithCapabilities(caps ...pmedi.Capability) RuntimeProfile {
	p.Caps = p.Caps.Merge(pmedi.NewCapabilitySet(caps...))
	return p
}

// WithTurnPolicy returns a copy of p with the given turn policy.
func (p RuntimeProfile) WithTurnPolicy(tp pmedi.TurnPolicy) RuntimeProfile {
	p.TurnPolicy = tp
	return p
}

// WithExtensions returns a copy of p with additional extensions appended.
func (p RuntimeProfile) WithExtensions(exts ...Extension) RuntimeProfile {
	p.Extensions = append(append([]Extension(nil), p.Extensions...), exts...)
	return p
}

func (p RuntimeProfile) withName(name string) RuntimeProfile {
	p.Name = name
	return p
}

// ProfileOutbound presets script + barge-in on the kernel.
func ProfileOutbound() RuntimeProfile {
	return KernelProfile().
		WithCapabilities(pmedi.CapScript).
		WithTurnPolicy(&EndpointTurnGate{}).
		withName("outbound")
}

// ProfileSupport presets RAG + handoff on the kernel.
func ProfileSupport() RuntimeProfile {
	return KernelProfile().
		WithCapabilities(pmedi.CapRAG, pmedi.CapHandoff).
		WithTurnPolicy(&EndpointTurnGate{}).
		withName("support")
}

// ProfileMeeting presets multi-track capture + batch summary (no TTS in kernel).
func ProfileMeeting() RuntimeProfile {
	return RuntimeProfile{
		Name:       "meeting",
		Caps:       pmedi.PresetMeeting(),
		TurnPolicy: &EndpointTurnGate{},
	}
}

// ProfileAvatar presets video downlink + avatar lip-sync on the kernel.
func ProfileAvatar() RuntimeProfile {
	return KernelProfile().
		WithCapabilities(pmedi.CapVideoDownlink, pmedi.CapAvatar).
		WithTurnPolicy(&EndpointTurnGate{})
}
