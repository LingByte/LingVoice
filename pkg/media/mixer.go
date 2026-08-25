package media

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// MixerParticipant is a single leg in a conference mix.
type MixerParticipant struct {
	ID     string
	Input  chan MediaPacket // inbound audio from this participant (caller sends here)
	Output chan MediaPacket // mixed audio for this participant (mixer sends here)
	Gain   float64          // per-participant input gain (1.0 = unity)
}

// ConferenceMixer implements N-way audio mixing. Each participant receives
// the sum of all OTHER participants' audio (N-1 mix), scaled by per-route gains.
// Uses tick-based 20ms processing with non-blocking sends.
type ConferenceMixer struct {
	mu            sync.RWMutex
	participants  map[string]*MixerParticipant
	frameSize     int // samples per frame (e.g. 160 for 20ms@8kHz, 320 for 20ms@16kHz)
	sampleRate    int
	tickInterval  time.Duration
	active        atomic.Bool
	droppedFrames atomic.Int64
}

// NewConferenceMixer creates a mixer for the given sample rate and frame duration.
// frameSize = sampleRate * frameMs / 1000 (e.g. 320 for 20ms@16kHz).
func NewConferenceMixer(sampleRate, frameSize int) *ConferenceMixer {
	return &ConferenceMixer{
		participants: make(map[string]*MixerParticipant),
		frameSize:    frameSize,
		sampleRate:   sampleRate,
		tickInterval: time.Duration(float64(frameSize)/float64(sampleRate)*1e9) * time.Nanosecond,
	}
}

// AddParticipant joins a participant to the conference.
// Input channel should be buffered (e.g. capacity 8 frames).
// Output channel should be buffered (e.g. capacity 8 frames).
func (m *ConferenceMixer) AddParticipant(p *MixerParticipant) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.participants[p.ID] = p
}

// RemoveParticipant leaves the conference.
func (m *ConferenceMixer) RemoveParticipant(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.participants, id)
}

// ParticipantCount returns the current number of participants (lock-free read).
func (m *ConferenceMixer) ParticipantCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.participants)
}

// DroppedFrames returns total frames dropped due to full output queues.
func (m *ConferenceMixer) DroppedFrames() int64 {
	return m.droppedFrames.Load()
}

// Run starts the mixing loop. Blocks until ctx is canceled.
// Each tick: read one frame from each participant's Input, mix N-1, send to each Output.
func (m *ConferenceMixer) Run(ctx context.Context) {
	m.active.Store(true)
	defer m.active.Store(false)

	ticker := time.NewTicker(m.tickInterval)
	defer ticker.Stop()

	// Accumulate frames per participant for this tick
	type frameData struct {
		id      string
		samples []int16
		gain    float64
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		m.mu.RLock()
		if len(m.participants) == 0 {
			m.mu.RUnlock()
			continue
		}

		// Read one frame from each participant (non-blocking, skip if empty)
		frames := make([]frameData, 0, len(m.participants))
		for id, p := range m.participants {
			select {
			case pkt := <-p.Input:
				if ap, ok := pkt.(*AudioPacket); ok && ap != nil {
					samples := bytesToInt16(ap.Payload)
					if len(samples) > 0 {
						frames = append(frames, frameData{
							id:      id,
							samples: samples,
							gain:    p.Gain,
						})
					}
				}
			default:
				// No frame available from this participant this tick
			}
		}
		m.mu.RUnlock()

		if len(frames) == 0 {
			continue
		}

		// Mix N-1: for each participant, sum all OTHER participants' frames
		m.mu.RLock()
		for _, p := range m.participants {
			mixed := make([]int16, m.frameSize)
			for _, f := range frames {
				if f.id == p.ID {
					continue // N-1: exclude self
				}
				gain := f.gain
				if gain == 0 {
					gain = 1.0
				}
				// Mix with saturation
				for i := 0; i < len(mixed) && i < len(f.samples); i++ {
					val := float64(mixed[i]) + float64(f.samples[i])*gain
					if val > 32767 {
						val = 32767
					} else if val < -32768 {
						val = -32768
					}
					mixed[i] = int16(val)
				}
			}

			// Non-blocking send (drop if output full)
			outPkt := &AudioPacket{
				Payload:       int16ToBytes(mixed),
				IsSynthesized: true,
			}
			select {
			case p.Output <- outPkt:
			default:
				m.droppedFrames.Add(1)
			}
		}
		m.mu.RUnlock()
	}
}

// IsActive returns whether the mixer loop is running.
func (m *ConferenceMixer) IsActive() bool {
	return m.active.Load()
}

// bytesToInt16 converts little-endian PCM16 bytes to int16 samples.
func bytesToInt16(b []byte) []int16 {
	n := len(b) / 2
	if n == 0 {
		return nil
	}
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(b[i*2]) | int16(b[i*2+1])<<8
	}
	return out
}

// int16ToBytes converts int16 samples to little-endian PCM16 bytes.
func int16ToBytes(s []int16) []byte {
	out := make([]byte, len(s)*2)
	for i, v := range s {
		out[i*2] = byte(v)
		out[i*2+1] = byte(v >> 8)
	}
	return out
}
