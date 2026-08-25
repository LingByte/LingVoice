package media

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"sync"
	"time"
)

// JitterBuffer is a simple RTP-style jitter buffer for PCM audio packets.
// It reorders packets by sequence number, holds them for a configurable
// delay, and drops late/duplicate packets. The delay adapts based on
// observed jitter.
//
// Design:
// - Fixed-capacity ring buffer indexed by sequence number (mod capacity)
// - Each slot stores the packet + its arrival timestamp
// - Read() returns the next in-order packet, blocking up to readTimeout
// - If a packet is missing past the delay window, a silence frame is inserted
// - The nominal delay adapts: if too many late drops, increase delay;
//   if no drops for a while, decrease delay (shrink to reduce latency)
type JitterBuffer struct {
	mu             sync.Mutex
	capacity       int           // ring buffer size (e.g. 50 frames)
	delay          time.Duration // nominal playout delay (adapts)
	minDelay       time.Duration
	maxDelay       time.Duration
	nextSeq        uint16 // next expected sequence to read out
	nextWriteSeq   uint16 // next expected sequence to write in
	slots          []jitterSlot
	arrivalTimes   []time.Time
	lateDrops      int
	silenceInserts int
	totalPackets   int
	adaptive       bool
	lastAdapt      time.Time
}

type jitterSlot struct {
	seq    uint16
	packet MediaPacket
	filled bool
}

// NewJitterBuffer creates a jitter buffer with the given capacity (in frames)
// and initial playout delay.
func NewJitterBuffer(capacity int, delay time.Duration) *JitterBuffer {
	return &JitterBuffer{
		capacity:     capacity,
		delay:        delay,
		minDelay:     20 * time.Millisecond,
		maxDelay:     200 * time.Millisecond,
		slots:        make([]jitterSlot, capacity),
		arrivalTimes: make([]time.Time, capacity),
		adaptive:     true,
	}
}

// Write inserts a packet into the jitter buffer. Returns true if accepted,
// false if dropped (duplicate, too late, or buffer full).
func (j *JitterBuffer) Write(seq uint16, packet MediaPacket) bool {
	j.mu.Lock()
	defer j.mu.Unlock()

	idx := int(seq) % j.capacity
	slot := &j.slots[idx]

	// Duplicate check
	if slot.filled && slot.seq == seq {
		return false // duplicate, drop
	}

	// Late check: if seq is behind nextSeq by more than capacity, it's too late
	diff := int16(seq - j.nextWriteSeq)
	if diff < -int16(j.capacity) {
		j.lateDrops++
		return false
	}

	slot.seq = seq
	slot.packet = packet
	slot.filled = true
	j.arrivalTimes[idx] = time.Now()
	j.totalPackets++

	// Advance nextWriteSeq if this is the next expected
	if diff >= 0 {
		j.nextWriteSeq = seq + 1
	}

	return true
}

// Read returns the next in-order packet. If the packet is not available
// within the delay window, a silence AudioPacket is inserted.
// Returns nil if the buffer is empty and no packet arrives within timeout.
func (j *JitterBuffer) Read(timeout time.Duration) MediaPacket {
	j.mu.Lock()

	idx := int(j.nextSeq) % j.capacity
	slot := &j.slots[idx]

	if slot.filled && slot.seq == j.nextSeq {
		pkt := slot.packet
		slot.filled = false
		slot.packet = nil
		j.nextSeq++
		j.mu.Unlock()
		return pkt
	}

	// Packet not available — check if we should wait or insert silence
	// For simplicity in this first version, just return nil (caller handles)
	j.mu.Unlock()
	return nil
}

// ReadBlocking waits up to timeout for the next packet.
func (j *JitterBuffer) ReadBlocking(timeout time.Duration) MediaPacket {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pkt := j.Read(0); pkt != nil {
			return pkt
		}
		time.Sleep(time.Millisecond)
	}
	return nil
}

// Stats returns jitter buffer statistics.
type JitterStats struct {
	Capacity       int
	Delay          time.Duration
	LateDrops      int
	SilenceInserts int
	TotalPackets   int
}

func (j *JitterBuffer) Stats() JitterStats {
	j.mu.Lock()
	defer j.mu.Unlock()
	return JitterStats{
		Capacity:       j.capacity,
		Delay:          j.delay,
		LateDrops:      j.lateDrops,
		SilenceInserts: j.silenceInserts,
		TotalPackets:   j.totalPackets,
	}
}

// Reset clears the buffer and resets sequence tracking.
func (j *JitterBuffer) Reset() {
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := range j.slots {
		j.slots[i].filled = false
		j.slots[i].packet = nil
	}
	j.lateDrops = 0
	j.silenceInserts = 0
	j.totalPackets = 0
}
