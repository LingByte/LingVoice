package media

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"sync/atomic"
)

// RelayMode controls whether the session bypasses decode→encode for same-codec
// legs. Inspired by RustPBX's RewriteRelay fast-path: when both input and output
// use the same codec (e.g. PCMU→PCMU), packets can be forwarded directly without
// decoding to PCM and re-encoding, saving CPU and avoiding generational loss.
//
// When relay is active:
//   - Input packets are NOT decoded (decoder is skipped)
//   - Output packets are NOT encoded (encoder is skipped)
//   - Filters still run (they operate on the raw codec payload)
//   - Processors still run (but see the raw codec packet, not PCM)
//
// Relay is automatically disabled when:
//   - Input and output codecs differ
//   - A processor or filter needs PCM (opt-in via NeedsPCM())
//   - The caller explicitly disables it via SetRelayMode(false)

// RelayState tracks whether same-codec fast-path is active.
type RelayState struct {
	enabled    atomic.Bool   // user wants relay
	active     atomic.Bool   // relay is actually active (enabled + codec match)
	inputCodec atomic.Value  // string
	outputCodec atomic.Value // string
}

// NewRelayState creates a relay state with relay enabled by default.
func NewRelayState() *RelayState {
	rs := &RelayState{}
	rs.enabled.Store(true)
	return rs
}

// SetEnabled enables/disables relay. Call before Serve().
func (r *RelayState) SetEnabled(enabled bool) {
	r.enabled.Store(enabled)
	r.updateActive()
}

// IsEnabled returns whether relay is user-enabled.
func (r *RelayState) IsEnabled() bool { return r.enabled.Load() }

// IsActive returns whether relay is currently active (enabled + codec match).
func (r *RelayState) IsActive() bool { return r.active.Load() }

// SetInputCodec sets the input transport codec for relay negotiation.
func (r *RelayState) SetInputCodec(codec string) {
	r.inputCodec.Store(codec)
	r.updateActive()
}

// SetOutputCodec sets the output transport codec for relay negotiation.
func (r *RelayState) SetOutputCodec(codec string) {
	r.outputCodec.Store(codec)
	r.updateActive()
}

// updateActive recomputes whether relay can run.
func (r *RelayState) updateActive() {
	if !r.enabled.Load() {
		r.active.Store(false)
		return
	}
	in, ok1 := r.inputCodec.Load().(string)
	out, ok2 := r.outputCodec.Load().(string)
	if !ok1 || !ok2 || in == "" || out == "" {
		r.active.Store(false)
		return
	}
	r.active.Store(in == out)
}

// relayPacketPool reuses AudioPacket objects in the relay fast-path to avoid
// per-packet allocation. The payload slice is NOT pooled (it's owned by the
// transport), only the AudioPacket wrapper.
var relayPacketPool = NewPacketPool()

// PacketPool is a lightweight sync.Pool for AudioPacket objects.
type PacketPool struct {
	pool chan *AudioPacket
}

// NewPacketPool creates a packet pool with the given capacity.
// Default capacity is 64.
func NewPacketPool() *PacketPool {
	return &PacketPool{pool: make(chan *AudioPacket, 64)}
}

// Get retrieves a reset AudioPacket from the pool or allocates a new one.
func (p *PacketPool) Get() *AudioPacket {
	select {
	case pkt := <-p.pool:
		pkt.PlayID = ""
		pkt.Sequence = 0
		pkt.Payload = nil
		pkt.RTPSamples = 0
		pkt.IsFirstPacket = false
		pkt.IsEndPacket = false
		pkt.IsSynthesized = false
		pkt.IsSilence = false
		pkt.SourceText = ""
		return pkt
	default:
		return &AudioPacket{}
	}
}

// Put returns an AudioPacket to the pool. The payload is cleared to avoid
// retaining references to transport-owned buffers.
func (p *PacketPool) Put(pkt *AudioPacket) {
	if pkt == nil {
		return
	}
	pkt.Payload = nil
	select {
	case p.pool <- pkt:
	default: // pool full, let GC collect
	}
}
