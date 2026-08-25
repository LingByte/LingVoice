package media

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Recorder records audio packets to a WAV file. Packets are buffered by
// RTP timestamp and flushed periodically (default 200ms) to ensure
// correct ordering even with out-of-order arrival.
//
// Design (inspired by RustPBX recorder.rs):
// - BTreeMap-like ordering by timestamp (we use a map + sort on flush)
// - 200ms flush interval balances latency and write efficiency
// - Two-pass WAV writing: placeholder header → stream data → update header
// - Thread-safe: Write can be called from any goroutine
type Recorder struct {
	mu            sync.Mutex
	file          *os.File
	sampleRate    int
	channels      int
	buffer        map[uint32][]byte // timestamp → payload (PCM16LE)
	nextFlushTs   uint32
	flushInterval time.Duration
	writtenBytes  int64
	closed        atomic.Bool
	totalPackets  atomic.Int64
}

// NewRecorder creates a recorder that writes WAV to the given path.
// The file is created immediately with a placeholder header.
func NewRecorder(path string, sampleRate, channels int) (*Recorder, error) {
	dir := filepath.Dir(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("recorder: cannot create dir: %w", err)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("recorder: cannot create file: %w", err)
	}

	r := &Recorder{
		file:          f,
		sampleRate:    sampleRate,
		channels:      channels,
		buffer:        make(map[uint32][]byte),
		flushInterval: 200 * time.Millisecond,
	}

	// Write placeholder WAV header (44 bytes)
	// Will be updated on Close with actual data size
	if err := r.writeWAVHeader(0); err != nil {
		f.Close()
		return nil, err
	}

	return r, nil
}

// Write adds a PCM16LE audio chunk with the given RTP timestamp.
// Data is buffered and flushed periodically.
func (r *Recorder) Write(timestamp uint32, data []byte) error {
	if r.closed.Load() {
		return fmt.Errorf("recorder: closed")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// Store in buffer (copy to avoid retaining caller's slice)
	buf := make([]byte, len(data))
	copy(buf, data)
	r.buffer[timestamp] = buf
	r.totalPackets.Add(1)

	// Check if it's time to flush
	if len(r.buffer) >= 50 || timestamp-r.nextFlushTs > uint32(r.sampleRate/5) {
		if err := r.flush(); err != nil {
			return err
		}
		r.nextFlushTs = timestamp
	}

	return nil
}

// flush writes buffered chunks to file in timestamp order.
func (r *Recorder) flush() error {
	if len(r.buffer) == 0 {
		return nil
	}

	// Sort timestamps
	timestamps := make([]uint32, 0, len(r.buffer))
	for ts := range r.buffer {
		timestamps = append(timestamps, ts)
	}
	sort.Slice(timestamps, func(i, j int) bool {
		return timestamps[i] < timestamps[j]
	})

	// Write in order
	for _, ts := range timestamps {
		data := r.buffer[ts]
		n, err := r.file.Write(data)
		if err != nil {
			return fmt.Errorf("recorder: write error: %w", err)
		}
		r.writtenBytes += int64(n)
		delete(r.buffer, ts)
	}

	return nil
}

// Close flushes remaining data, updates the WAV header, and closes the file.
func (r *Recorder) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil // already closed
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Final flush
	if err := r.flush(); err != nil {
		r.file.Close()
		return err
	}

	// Update WAV header with actual data size
	if err := r.updateWAVHeader(); err != nil {
		r.file.Close()
		return err
	}

	return r.file.Close()
}

// writeWAVHeader writes a standard 44-byte WAV header with placeholder data size.
func (r *Recorder) writeWAVHeader(dataSize uint32) error {
	header := make([]byte, 44)

	// RIFF header
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], 36+dataSize)
	copy(header[8:12], "WAVE")

	// fmt chunk
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(header[20:22], 1)  // PCM format
	binary.LittleEndian.PutUint16(header[22:24], uint16(r.channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(r.sampleRate))
	byteRate := r.sampleRate * r.channels * 2
	binary.LittleEndian.PutUint32(header[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(header[32:34], uint16(r.channels*2)) // block align
	binary.LittleEndian.PutUint16(header[34:36], 16)                   // bits per sample

	// data chunk
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataSize)

	_, err := r.file.Write(header)
	return err
}

// updateWAVHeader seeks back to the header and updates the size fields.
func (r *Recorder) updateWAVHeader() error {
	if _, err := r.file.Seek(0, 0); err != nil {
		return fmt.Errorf("recorder: seek error: %w", err)
	}
	return r.writeWAVHeader(uint32(r.writtenBytes))
}

// RecorderStats holds recorder metrics.
type RecorderStats struct {
	WrittenBytes   int64
	TotalPackets   int64
	BufferedChunks int
}

func (r *Recorder) Stats() RecorderStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RecorderStats{
		WrittenBytes:   r.writtenBytes,
		TotalPackets:   r.totalPackets.Load(),
		BufferedChunks: len(r.buffer),
	}
}
