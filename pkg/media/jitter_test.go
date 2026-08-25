package media

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"testing"
	"time"
)

// makePkt creates an AudioPacket with the given sequence number and payload.
func makePkt(seq int, payload byte) *AudioPacket {
	return &AudioPacket{
		Sequence: seq,
		Payload:  []byte{payload},
	}
}

func TestJitterBuffer_InOrder(t *testing.T) {
	jb := NewJitterBuffer(50, 20*time.Millisecond)

	for i := 0; i < 4; i++ {
		if !jb.Write(uint16(i), makePkt(i, byte('A'+i))) {
			t.Fatalf("Write(%d) returned false, want true", i)
		}
	}

	for i := 0; i < 4; i++ {
		pkt := jb.Read(0)
		if pkt == nil {
			t.Fatalf("Read(%d) returned nil, want a packet", i)
		}
		ap, ok := pkt.(*AudioPacket)
		if !ok {
			t.Fatalf("Read(%d) returned non-AudioPacket: %T", i, pkt)
		}
		if ap.Sequence != i {
			t.Errorf("Read(%d) sequence = %d, want %d", i, ap.Sequence, i)
		}
		if len(ap.Payload) != 1 || ap.Payload[0] != byte('A'+i) {
			t.Errorf("Read(%d) payload = %v, want [%q]", i, ap.Payload, byte('A'+i))
		}
	}
}

func TestJitterBuffer_OutOfOrder(t *testing.T) {
	jb := NewJitterBuffer(50, 20*time.Millisecond)

	// Write out of order: 0, 2, 1, 3
	order := []int{0, 2, 1, 3}
	for _, s := range order {
		if !jb.Write(uint16(s), makePkt(s, byte('A'+s))) {
			t.Fatalf("Write(%d) returned false, want true", s)
		}
	}

	// Expect to read back in order: 0, 1, 2, 3
	for i := 0; i < 4; i++ {
		pkt := jb.Read(0)
		if pkt == nil {
			t.Fatalf("Read(%d) returned nil, want a packet", i)
		}
		ap, ok := pkt.(*AudioPacket)
		if !ok {
			t.Fatalf("Read(%d) returned non-AudioPacket: %T", i, pkt)
		}
		if ap.Sequence != i {
			t.Errorf("Read(%d) sequence = %d, want %d", i, ap.Sequence, i)
		}
	}
}

func TestJitterBuffer_Duplicate(t *testing.T) {
	jb := NewJitterBuffer(50, 20*time.Millisecond)

	if !jb.Write(0, makePkt(0, 'A')) {
		t.Fatal("Write(0) first time returned false, want true")
	}
	if jb.Write(0, makePkt(0, 'B')) {
		t.Fatal("Write(0) second time returned true, want false (duplicate)")
	}

	pkt := jb.Read(0)
	if pkt == nil {
		t.Fatal("Read returned nil, want the first packet")
	}
	ap, ok := pkt.(*AudioPacket)
	if !ok {
		t.Fatalf("Read returned non-AudioPacket: %T", pkt)
	}
	if len(ap.Payload) != 1 || ap.Payload[0] != 'A' {
		t.Errorf("Read payload = %v, want ['A'] (first write should win)", ap.Payload)
	}
}

func TestJitterBuffer_LateDrop(t *testing.T) {
	jb := NewJitterBuffer(50, 20*time.Millisecond)

	// Write 0, 1, 2 to advance nextWriteSeq
	for i := 0; i < 3; i++ {
		if !jb.Write(uint16(i), makePkt(i, byte('A'+i))) {
			t.Fatalf("Write(%d) returned false, want true", i)
		}
	}

	// Try to write a very old sequence number (behind by more than capacity)
	// nextWriteSeq is now 3. seq = 3 - 50 - 1 = -48 => uint16 wraps to 65487
	// That's ahead, not behind. Instead use a seq that is clearly behind.
	// With capacity 50, a diff < -50 is a late drop. We need seq such that
	// int16(seq - nextWriteSeq) < -50. nextWriteSeq=3, so seq must be
	// 3 - 51 = -48 which wraps to 65487. int16(65487 - 3) = int16(65484) = 52 (positive).
	// So wrapping doesn't give a negative. To force a late drop we need a
	// genuinely old seq that hasn't wrapped past. Use a large nextWriteSeq.
	// Simpler: advance nextWriteSeq far ahead, then write a small seq.
	jb2 := NewJitterBuffer(50, 20*time.Millisecond)
	// Write seq 100 to set nextWriteSeq to 101
	if !jb2.Write(100, makePkt(100, 'Z')) {
		t.Fatal("Write(100) returned false, want true")
	}
	// Now write seq 0 — diff = int16(0 - 101) = -101 < -50 => late drop
	if jb2.Write(0, makePkt(0, 'A')) {
		t.Fatal("Write(0) returned true, want false (late drop)")
	}

	stats := jb2.Stats()
	if stats.LateDrops != 1 {
		t.Errorf("LateDrops = %d, want 1", stats.LateDrops)
	}
}

func TestJitterBuffer_Stats(t *testing.T) {
	jb := NewJitterBuffer(50, 20*time.Millisecond)

	for i := 0; i < 5; i++ {
		jb.Write(uint16(i), makePkt(i, byte('A'+i)))
	}

	stats := jb.Stats()
	if stats.Capacity != 50 {
		t.Errorf("Stats Capacity = %d, want 50", stats.Capacity)
	}
	if stats.Delay != 20*time.Millisecond {
		t.Errorf("Stats Delay = %v, want 20ms", stats.Delay)
	}
	if stats.TotalPackets != 5 {
		t.Errorf("Stats TotalPackets = %d, want 5", stats.TotalPackets)
	}
	if stats.LateDrops != 0 {
		t.Errorf("Stats LateDrops = %d, want 0", stats.LateDrops)
	}
}

func TestJitterBuffer_Reset(t *testing.T) {
	jb := NewJitterBuffer(50, 20*time.Millisecond)

	for i := 0; i < 4; i++ {
		jb.Write(uint16(i), makePkt(i, byte('A'+i)))
	}

	jb.Reset()

	stats := jb.Stats()
	if stats.TotalPackets != 0 {
		t.Errorf("After Reset, TotalPackets = %d, want 0", stats.TotalPackets)
	}
	if stats.LateDrops != 0 {
		t.Errorf("After Reset, LateDrops = %d, want 0", stats.LateDrops)
	}
	if stats.SilenceInserts != 0 {
		t.Errorf("After Reset, SilenceInserts = %d, want 0", stats.SilenceInserts)
	}

	// After reset, reading should return nil (slots cleared)
	if pkt := jb.Read(0); pkt != nil {
		t.Errorf("After Reset, Read returned %v, want nil", pkt)
	}
}
