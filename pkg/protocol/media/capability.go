package media

// Capability names an optional feature that extends the AV+LLM kernel.
type Capability string

const (
	CapAudioUplink   Capability = "audio.uplink"
	CapAudioDownlink Capability = "audio.downlink"
	CapVideoUplink   Capability = "video.uplink"
	CapVideoDownlink Capability = "video.downlink"

	CapASR Capability = "asr"
	CapTTS Capability = "tts"
	CapVAD Capability = "vad"

	CapTurn    Capability = "turn"
	CapBargeIn Capability = "barge_in"

	CapRAG          Capability = "rag"
	CapScript       Capability = "script"
	CapHandoff      Capability = "handoff"
	CapMultiTrack   Capability = "multitrack"
	CapAvatar       Capability = "avatar"
	CapBatchSummary Capability = "batch.summary"
)

// CapabilitySet is a set of enabled capabilities for one session or graph.
type CapabilitySet map[Capability]bool

// NewCapabilitySet returns a set with the given capabilities enabled.
func NewCapabilitySet(caps ...Capability) CapabilitySet {
	s := make(CapabilitySet, len(caps))
	for _, c := range caps {
		s[c] = true
	}
	return s
}

// Has reports whether cap is enabled.
func (s CapabilitySet) Has(cap Capability) bool {
	return s != nil && s[cap]
}

// Merge returns a new set containing all capabilities from a and b.
func (s CapabilitySet) Merge(other CapabilitySet) CapabilitySet {
	out := make(CapabilitySet)
	for k, v := range s {
		if v {
			out[k] = true
		}
	}
	for k, v := range other {
		if v {
			out[k] = true
		}
	}
	return out
}

// KernelCapabilities is the minimal set every voice session needs.
func KernelCapabilities() CapabilitySet {
	return NewCapabilitySet(
		CapAudioUplink, CapAudioDownlink,
		CapASR, CapTTS, CapTurn, CapBargeIn,
	)
}

// PresetOutbound: scripted outbound calls with barge-in.
func PresetOutbound() CapabilitySet {
	return KernelCapabilities().Merge(NewCapabilitySet(CapScript))
}

// PresetSupport: customer support with knowledge and human handoff.
func PresetSupport() CapabilitySet {
	return KernelCapabilities().Merge(NewCapabilitySet(CapRAG, CapHandoff))
}

// PresetMeeting: multi-participant capture with batch summarization.
func PresetMeeting() CapabilitySet {
	return NewCapabilitySet(
		CapMultiTrack, CapAudioUplink, CapASR,
		CapBatchSummary, CapTurn,
	)
}

// PresetAvatar: digital human with video downlink and lip-sync.
func PresetAvatar() CapabilitySet {
	return KernelCapabilities().Merge(NewCapabilitySet(
		CapVideoDownlink, CapAvatar,
	))
}
