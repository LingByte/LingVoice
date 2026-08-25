package media

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// makeRampPCM generates a simple ascending ramp of int16 samples.
func makeRampPCM(n int) []int16 {
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(i)
	}
	return out
}

func TestAudioSource_FromPCM(t *testing.T) {
	want := makeRampPCM(100)
	src := NewPCMAudioSource(int16ToBytes(want), 16000, 1)
	if src.SampleRate() != 16000 {
		t.Fatalf("sample rate = %d, want 16000", src.SampleRate())
	}
	if src.Channels() != 1 {
		t.Fatalf("channels = %d, want 1", src.Channels())
	}

	got, err := src.ReadSamples(100)
	if err != nil {
		t.Fatalf("ReadSamples: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestAudioSource_Loop(t *testing.T) {
	want := makeRampPCM(8)
	src := NewPCMAudioSource(int16ToBytes(want), 8000, 1)
	src.SetLoop(true)

	// Read all 8 samples.
	first, err := src.ReadSamples(8)
	if err != nil {
		t.Fatalf("first ReadSamples: %v", err)
	}
	if len(first) != 8 {
		t.Fatalf("first len = %d, want 8", len(first))
	}

	// Read past end: should wrap and return from the beginning.
	second, err := src.ReadSamples(4)
	if err != nil {
		t.Fatalf("second ReadSamples: %v", err)
	}
	if len(second) != 4 {
		t.Fatalf("second len = %d, want 4", len(second))
	}
	for i := 0; i < 4; i++ {
		if second[i] != want[i] {
			t.Fatalf("wrapped sample %d = %d, want %d", i, second[i], want[i])
		}
	}
}

func TestAudioSource_EOF(t *testing.T) {
	src := NewPCMAudioSource(int16ToBytes(makeRampPCM(10)), 8000, 1)
	// Do not enable loop.
	if _, err := src.ReadSamples(10); err != nil {
		t.Fatalf("first ReadSamples: %v", err)
	}
	_, err := src.ReadSamples(1)
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestAudioSource_Reset(t *testing.T) {
	want := makeRampPCM(20)
	src := NewPCMAudioSource(int16ToBytes(want), 8000, 1)

	if _, err := src.ReadSamples(5); err != nil {
		t.Fatalf("first ReadSamples: %v", err)
	}
	src.Reset()

	got, err := src.ReadSamples(20)
	if err != nil {
		t.Fatalf("post-reset ReadSamples: %v", err)
	}
	if len(got) != 20 {
		t.Fatalf("len = %d, want 20", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestAudioSource_Duration(t *testing.T) {
	// 8000 samples at 8000 Hz = 1 second.
	src := NewPCMAudioSource(int16ToBytes(makeRampPCM(8000)), 8000, 1)
	d := src.Duration()
	if d != 1.0 {
		t.Fatalf("duration = %f, want 1.0", d)
	}
}

func TestAudioSource_FromWAV(t *testing.T) {
	want := makeRampPCM(500)
	const sampleRate, channels = 16000, 1

	dir, err := os.MkdirTemp("", "audiosource_wav_*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "test.wav")
	if err := WriteWAVFile(path, want, sampleRate, channels); err != nil {
		t.Fatalf("WriteWAVFile: %v", err)
	}

	src, err := NewFileAudioSource(path)
	if err != nil {
		t.Fatalf("NewFileAudioSource: %v", err)
	}
	if src.SampleRate() != sampleRate {
		t.Fatalf("sample rate = %d, want %d", src.SampleRate(), sampleRate)
	}
	if src.Channels() != channels {
		t.Fatalf("channels = %d, want %d", src.Channels(), channels)
	}

	got, err := src.ReadSamples(len(want))
	if err != nil {
		t.Fatalf("ReadSamples: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestChannelAudioSource_PushRead(t *testing.T) {
	c := NewChannelAudioSource(16000, 1, 4)
	if c.SampleRate() != 16000 {
		t.Fatalf("sample rate = %d, want 16000", c.SampleRate())
	}
	if c.Channels() != 1 {
		t.Fatalf("channels = %d, want 1", c.Channels())
	}

	frame := makeRampPCM(10)
	if err := c.Push(frame); err != nil {
		t.Fatalf("Push: %v", err)
	}
	got, err := c.ReadSamples(10)
	if err != nil {
		t.Fatalf("ReadSamples: %v", err)
	}
	if len(got) != len(frame) {
		t.Fatalf("len = %d, want %d", len(got), len(frame))
	}
	for i := range frame {
		if got[i] != frame[i] {
			t.Fatalf("sample %d = %d, want %d", i, got[i], frame[i])
		}
	}
}

func TestChannelAudioSource_Close(t *testing.T) {
	c := NewChannelAudioSource(16000, 1, 4)
	c.Close()
	// Closing twice must not panic.
	c.Close()
	_, err := c.ReadSamples(10)
	if err != io.EOF {
		t.Fatalf("expected io.EOF after close, got %v", err)
	}
	// Push after close should error.
	if err := c.Push(makeRampPCM(4)); err == nil {
		t.Fatalf("expected error pushing after close")
	}
}

func TestChannelAudioSource_DropOnFull(t *testing.T) {
	c := NewChannelAudioSource(16000, 1, 2)
	// Fill the 2-slot buffer.
	if err := c.Push(makeRampPCM(4)); err != nil {
		t.Fatalf("Push 1: %v", err)
	}
	if err := c.Push(makeRampPCM(4)); err != nil {
		t.Fatalf("Push 2: %v", err)
	}
	// Third push should be dropped (non-blocking).
	if err := c.Push(makeRampPCM(4)); err == nil {
		t.Fatalf("expected drop error when channel full")
	}
}
