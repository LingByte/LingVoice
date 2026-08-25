package media

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// helper: build a PCM16 frame of `n` samples all set to `v`.
func toneFrame(n int, v int16) []byte {
	s := make([]int16, n)
	for i := range s {
		s[i] = v
	}
	return int16ToBytes(s)
}

// helper: read int16 samples from a MediaPacket payload.
func samplesFromPacket(pkt MediaPacket) []int16 {
	ap, ok := pkt.(*AudioPacket)
	if !ok || ap == nil {
		return nil
	}
	return bytesToInt16(ap.Payload)
}

func TestConferenceMixer_N1Mix(t *testing.T) {
	const frameSize = 160
	mixer := NewConferenceMixer(8000, frameSize)

	p1 := &MixerParticipant{ID: "A", Input: make(chan MediaPacket, 8), Output: make(chan MediaPacket, 8), Gain: 1.0}
	p2 := &MixerParticipant{ID: "B", Input: make(chan MediaPacket, 8), Output: make(chan MediaPacket, 8), Gain: 1.0}
	p3 := &MixerParticipant{ID: "C", Input: make(chan MediaPacket, 8), Output: make(chan MediaPacket, 8), Gain: 1.0}
	mixer.AddParticipant(p1)
	mixer.AddParticipant(p2)
	mixer.AddParticipant(p3)

	// Each participant sends a distinct constant tone.
	p1.Input <- &AudioPacket{Payload: toneFrame(frameSize, 1000)}
	p2.Input <- &AudioPacket{Payload: toneFrame(frameSize, 2000)}
	p3.Input <- &AudioPacket{Payload: toneFrame(frameSize, 3000)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mixer.Run(ctx)

	// Wait for one tick of output from each participant.
	readWithTimeout := func(ch chan MediaPacket) []int16 {
		select {
		case pkt := <-ch:
			return samplesFromPacket(pkt)
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("timed out waiting for output")
			return nil
		}
	}

	outA := readWithTimeout(p1.Output) // should be B+C = 2000+3000 = 5000
	outB := readWithTimeout(p2.Output) // should be A+C = 1000+3000 = 4000
	outC := readWithTimeout(p3.Output) // should be A+B = 1000+2000 = 3000

	checkConst := func(name string, s []int16, want int16) {
		t.Helper()
		if len(s) != frameSize {
			t.Fatalf("%s: len=%d want %d", name, len(s), frameSize)
		}
		for i, v := range s {
			if v != want {
				t.Fatalf("%s: sample[%d]=%d want %d", name, i, v, want)
			}
		}
	}
	checkConst("A", outA, 5000)
	checkConst("B", outB, 4000)
	checkConst("C", outC, 3000)
}

func TestConferenceMixer_AddRemove(t *testing.T) {
	mixer := NewConferenceMixer(8000, 160)

	if got := mixer.ParticipantCount(); got != 0 {
		t.Fatalf("initial count=%d want 0", got)
	}

	p1 := &MixerParticipant{ID: "A", Input: make(chan MediaPacket, 8), Output: make(chan MediaPacket, 8)}
	p2 := &MixerParticipant{ID: "B", Input: make(chan MediaPacket, 8), Output: make(chan MediaPacket, 8)}
	mixer.AddParticipant(p1)
	mixer.AddParticipant(p2)
	if got := mixer.ParticipantCount(); got != 2 {
		t.Fatalf("after add count=%d want 2", got)
	}

	mixer.RemoveParticipant("A")
	if got := mixer.ParticipantCount(); got != 1 {
		t.Fatalf("after remove count=%d want 1", got)
	}

	mixer.RemoveParticipant("B")
	if got := mixer.ParticipantCount(); got != 0 {
		t.Fatalf("after remove all count=%d want 0", got)
	}

	// Removing non-existent should be a no-op.
	mixer.RemoveParticipant("nope")
	if got := mixer.ParticipantCount(); got != 0 {
		t.Fatalf("after remove non-existent count=%d want 0", got)
	}
}

func TestConferenceMixer_EmptyTick(t *testing.T) {
	mixer := NewConferenceMixer(8000, 160)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		mixer.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// ok: exited cleanly on ctx cancel
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("mixer did not exit after ctx cancel")
	}

	if mixer.IsActive() {
		t.Fatalf("IsActive should be false after Run returns")
	}
	if mixer.DroppedFrames() != 0 {
		t.Fatalf("DroppedFrames=%d want 0", mixer.DroppedFrames())
	}
}

func TestConferenceMixer_DroppedFrames(t *testing.T) {
	const frameSize = 160
	mixer := NewConferenceMixer(8000, frameSize)

	// Output channel capacity 1 so it fills quickly.
	p1 := &MixerParticipant{ID: "A", Input: make(chan MediaPacket, 8), Output: make(chan MediaPacket, 1), Gain: 1.0}
	p2 := &MixerParticipant{ID: "B", Input: make(chan MediaPacket, 8), Output: make(chan MediaPacket, 1), Gain: 1.0}
	mixer.AddParticipant(p1)
	mixer.AddParticipant(p2)

	// Pre-fill the output channels so the first send is already blocked.
	p1.Output <- &AudioPacket{Payload: toneFrame(frameSize, 0)}
	p2.Output <- &AudioPacket{Payload: toneFrame(frameSize, 0)}

	// Queue several input frames so multiple ticks produce drops.
	for i := 0; i < 4; i++ {
		p1.Input <- &AudioPacket{Payload: toneFrame(frameSize, 100)}
		p2.Input <- &AudioPacket{Payload: toneFrame(frameSize, 100)}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mixer.Run(ctx)

	// Wait long enough for a few ticks to run (20ms each).
	time.Sleep(120 * time.Millisecond)

	if got := mixer.DroppedFrames(); got == 0 {
		t.Fatalf("DroppedFrames=0, expected > 0 due to full output queues")
	}
}

func TestConferenceMixer_Saturation(t *testing.T) {
	const frameSize = 160
	mixer := NewConferenceMixer(8000, frameSize)

	p1 := &MixerParticipant{ID: "A", Input: make(chan MediaPacket, 8), Output: make(chan MediaPacket, 8), Gain: 1.0}
	p2 := &MixerParticipant{ID: "B", Input: make(chan MediaPacket, 8), Output: make(chan MediaPacket, 8), Gain: 1.0}
	mixer.AddParticipant(p1)
	mixer.AddParticipant(p2)

	// Both send full-scale positive samples; sum would overflow int16.
	fullScale := int16(32767)
	p1.Input <- &AudioPacket{Payload: toneFrame(frameSize, fullScale)}
	p2.Input <- &AudioPacket{Payload: toneFrame(frameSize, fullScale)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mixer.Run(ctx)

	var out []int16
	select {
	case pkt := <-p1.Output:
		out = samplesFromPacket(pkt)
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("timed out waiting for output")
	}

	if len(out) != frameSize {
		t.Fatalf("len=%d want %d", len(out), frameSize)
	}
	for i, v := range out {
		// Should be clamped to max int16, not overflowed/wrapped.
		if v != 32767 {
			t.Fatalf("sample[%d]=%d want 32767 (clamped)", i, v)
		}
	}

	// Verify negative saturation too.
	p1.Input <- &AudioPacket{Payload: toneFrame(frameSize, -32768)}
	p2.Input <- &AudioPacket{Payload: toneFrame(frameSize, -32768)}

	select {
	case pkt := <-p1.Output:
		out = samplesFromPacket(pkt)
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("timed out waiting for negative output")
	}
	for i, v := range out {
		if v != -32768 {
			t.Fatalf("neg sample[%d]=%d want -32768 (clamped)", i, v)
		}
	}

	// Sanity: payload round-trips through bytesToInt16/int16ToBytes.
	orig := []int16{32767, -32768, 0, 1234}
	rt := bytesToInt16(int16ToBytes(orig))
	if !bytes.Equal(int16ToBytes(orig), int16ToBytes(rt)) {
		t.Fatalf("round-trip mismatch")
	}
}
