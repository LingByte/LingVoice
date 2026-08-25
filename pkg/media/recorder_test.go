package media

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// newTestRecorder creates a recorder in a temp dir and returns it along
// with the file path and a cleanup function.
func newTestRecorder(t *testing.T, sampleRate, channels int) (*Recorder, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "recorder-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(dir)
	})
	path := filepath.Join(dir, "out.wav")
	r, err := NewRecorder(path, sampleRate, channels)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	return r, path
}

// readWAVHeader reads and parses the 44-byte WAV header from a file.
func readWAVHeader(t *testing.T, path string) (sampleRate, channels, dataBytes int, totalSize int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	totalSize = len(data)
	if totalSize < 44 {
		t.Fatalf("file too small: %d bytes", totalSize)
	}
	if string(data[0:4]) != "RIFF" {
		t.Fatalf("bad RIFF marker: %q", data[0:4])
	}
	if string(data[8:12]) != "WAVE" {
		t.Fatalf("bad WAVE marker: %q", data[8:12])
	}
	if string(data[12:16]) != "fmt " {
		t.Fatalf("bad fmt marker: %q", data[12:16])
	}
	channels = int(binary.LittleEndian.Uint16(data[22:24]))
	sampleRate = int(binary.LittleEndian.Uint32(data[24:28]))
	if string(data[36:40]) != "data" {
		t.Fatalf("bad data marker: %q", data[36:40])
	}
	dataBytes = int(binary.LittleEndian.Uint32(data[40:44]))
	return
}

// TestRecorder_WriteAndClose writes some PCM data, closes, and verifies
// the file exists and has a correct WAV header.
func TestRecorder_WriteAndClose(t *testing.T) {
	r, path := newTestRecorder(t, 16000, 1)
	defer r.Close()

	// 100 samples of PCM16LE mono = 200 bytes
	data := make([]byte, 200)
	for i := range data {
		data[i] = byte(i % 256)
	}
	if err := r.Write(0, data); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if info.Size() != int64(44+200) {
		t.Fatalf("unexpected file size: got %d want %d", info.Size(), 44+200)
	}

	sr, ch, db, total := readWAVHeader(t, path)
	if sr != 16000 {
		t.Fatalf("sample rate: got %d want 16000", sr)
	}
	if ch != 1 {
		t.Fatalf("channels: got %d want 1", ch)
	}
	if db != 200 {
		t.Fatalf("data bytes: got %d want 200", db)
	}
	if total != 244 {
		t.Fatalf("total size: got %d want 244", total)
	}
}

// TestRecorder_OutOfOrder writes timestamps out of order and verifies
// the file contains them in ascending timestamp order.
func TestRecorder_OutOfOrder(t *testing.T) {
	r, path := newTestRecorder(t, 8000, 1)
	defer r.Close()

	// Each chunk: 4 bytes with a distinctive marker byte.
	// Write out of order: ts=20, ts=0, ts=10
	chunks := []struct {
		ts  uint32
		val byte
	}{
		{20, 0xCC},
		{0, 0xAA},
		{10, 0xBB},
	}
	for _, c := range chunks {
		data := []byte{c.val, 0x00, c.val, 0x00}
		if err := r.Write(c.ts, data); err != nil {
			t.Fatalf("Write ts=%d: %v", c.ts, err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	payload := raw[44:] // skip header
	want := []byte{0xAA, 0x00, 0xAA, 0x00, 0xBB, 0x00, 0xBB, 0x00, 0xCC, 0x00, 0xCC, 0x00}
	if len(payload) != len(want) {
		t.Fatalf("payload length: got %d want %d", len(payload), len(want))
	}
	for i, b := range want {
		if payload[i] != b {
			t.Fatalf("byte %d: got 0x%02X want 0x%02X", i, payload[i], b)
		}
	}
}

// TestRecorder_Stats writes packets and checks the stats.
func TestRecorder_Stats(t *testing.T) {
	r, _ := newTestRecorder(t, 16000, 1)
	defer r.Close()

	data := make([]byte, 100)
	for i := 0; i < 10; i++ {
		if err := r.Write(uint32(i*160), data); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	stats := r.Stats()
	if stats.TotalPackets != 10 {
		t.Fatalf("TotalPackets: got %d want 10", stats.TotalPackets)
	}
	// 10 packets × 100 bytes = 1000 bytes (all flushed since < 50 buffered and
	// timestamps are close, but flush only triggers on threshold/timestamp gap;
	// here they may still be buffered, so check either written or buffered).
	if stats.WrittenBytes+int64(stats.BufferedChunks*100) != 1000 {
		t.Fatalf("stats mismatch: written=%d buffered=%d", stats.WrittenBytes, stats.BufferedChunks)
	}
}

// TestRecorder_DoubleClose closes twice and verifies no error.
func TestRecorder_DoubleClose(t *testing.T) {
	r, _ := newTestRecorder(t, 16000, 1)

	if err := r.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestRecorder_WAVHeader writes known data, closes, re-reads the file,
// and verifies WAV header fields (sample rate, channels, data size).
func TestRecorder_WAVHeader(t *testing.T) {
	r, path := newTestRecorder(t, 48000, 2)
	defer r.Close()

	// 50 samples stereo PCM16LE = 50 * 2 * 2 = 200 bytes
	data := make([]byte, 200)
	if err := r.Write(0, data); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sr, ch, db, total := readWAVHeader(t, path)
	if sr != 48000 {
		t.Fatalf("sample rate: got %d want 48000", sr)
	}
	if ch != 2 {
		t.Fatalf("channels: got %d want 2", ch)
	}
	if db != 200 {
		t.Fatalf("data bytes: got %d want 200", db)
	}
	if total != 244 {
		t.Fatalf("total size: got %d want 244", total)
	}
}
