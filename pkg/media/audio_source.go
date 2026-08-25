package media

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
)

// AudioSource provides pre-decoded PCM audio for playback in a media session.
// Inspired by RustPBX's audio_source.rs: all I/O happens at construction time;
// ReadSamples() only copies from an in-memory buffer (zero hot-path I/O).
type AudioSource struct {
	mu         sync.Mutex
	pcmCache   []int16 // pre-decoded mono PCM samples
	pcmPos     int     // current read position
	sampleRate int
	channels   int
	loop       bool
}

// NewFileAudioSource loads a WAV file and pre-decodes it to PCM16 in memory.
// After construction, ReadSamples() is zero-I/O.
func NewFileAudioSource(path string) (*AudioSource, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("audio_source: cannot open %s: %w", path, err)
	}
	defer f.Close()

	sampleRate, channels, samples, err := decodeWAV(f)
	if err != nil {
		return nil, fmt.Errorf("audio_source: cannot decode WAV: %w", err)
	}

	return &AudioSource{
		pcmCache:   samples,
		sampleRate: sampleRate,
		channels:   channels,
	}, nil
}

// NewPCMAudioSource creates an audio source from raw PCM16LE data in memory.
// Useful for TTS injection or programmatically generated audio.
func NewPCMAudioSource(data []byte, sampleRate, channels int) *AudioSource {
	samples := bytesToInt16(data)
	return &AudioSource{
		pcmCache:   samples,
		sampleRate: sampleRate,
		channels:   channels,
	}
}

// NewChannelAudioSource creates a streaming audio source backed by a channel.
// External apps push PCM16LE frames into the channel; ReadSamples pulls from it.
// This is the TTS injection pattern.
type ChannelAudioSource struct {
	input      chan []int16
	sampleRate int
	channels   int
	closed     bool
	mu         sync.Mutex
}

func NewChannelAudioSource(sampleRate, channels, bufferFrames int) *ChannelAudioSource {
	return &ChannelAudioSource{
		input:      make(chan []int16, bufferFrames),
		sampleRate: sampleRate,
		channels:   channels,
	}
}

func (c *ChannelAudioSource) Push(samples []int16) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return fmt.Errorf("audio_source: channel closed")
	}
	select {
	case c.input <- samples:
		return nil
	default:
		return fmt.Errorf("audio_source: channel full, frame dropped")
	}
}

func (c *ChannelAudioSource) Close() {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.input)
	}
	c.mu.Unlock()
}

func (c *ChannelAudioSource) ReadSamples(n int) ([]int16, error) {
	select {
	case samples, ok := <-c.input:
		if !ok {
			return nil, io.EOF
		}
		return samples, nil
	}
}

func (c *ChannelAudioSource) SampleRate() int { return c.sampleRate }
func (c *ChannelAudioSource) Channels() int   { return c.channels }

// SetLoop enables/disables loop playback.
func (a *AudioSource) SetLoop(loop bool) {
	a.mu.Lock()
	a.loop = loop
	a.mu.Unlock()
}

// ReadSamples returns up to n samples from the pre-decoded buffer.
// If loop is enabled, wraps around; otherwise returns io.EOF at end.
func (a *AudioSource) ReadSamples(n int) ([]int16, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.pcmPos >= len(a.pcmCache) {
		if a.loop {
			a.pcmPos = 0
		} else {
			return nil, io.EOF
		}
	}

	end := a.pcmPos + n
	if end > len(a.pcmCache) {
		end = len(a.pcmCache)
	}
	samples := a.pcmCache[a.pcmPos:end]
	a.pcmPos = end
	return samples, nil
}

// ReadBytes returns up to n bytes of PCM16LE data.
func (a *AudioSource) ReadBytes(n int) ([]byte, error) {
	samples, err := a.ReadSamples(n / 2)
	if err != nil {
		return nil, err
	}
	return int16ToBytes(samples), nil
}

// SampleRate returns the source sample rate.
func (a *AudioSource) SampleRate() int { return a.sampleRate }

// Channels returns the number of channels.
func (a *AudioSource) Channels() int { return a.channels }

// Duration returns total duration in seconds.
func (a *AudioSource) Duration() float64 {
	if a.sampleRate == 0 {
		return 0
	}
	return float64(len(a.pcmCache)) / float64(a.sampleRate)
}

// Reset rewinds to the beginning.
func (a *AudioSource) Reset() {
	a.mu.Lock()
	a.pcmPos = 0
	a.mu.Unlock()
}

// decodeWAV reads a WAV file and returns sample rate, channels, and PCM16 samples.
func decodeWAV(r io.Reader) (sampleRate, channels int, samples []int16, err error) {
	header := make([]byte, 44)
	if _, err = io.ReadFull(r, header); err != nil {
		return 0, 0, nil, fmt.Errorf("cannot read WAV header: %w", err)
	}

	// Validate RIFF header
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return 0, 0, nil, fmt.Errorf("not a WAV file")
	}
	if string(header[12:16]) != "fmt " {
		return 0, 0, nil, fmt.Errorf("missing fmt chunk")
	}

	channels = int(binary.LittleEndian.Uint16(header[22:24]))
	sampleRate = int(binary.LittleEndian.Uint32(header[24:28]))
	bitsPerSample := int(binary.LittleEndian.Uint16(header[34:36]))

	if bitsPerSample != 16 {
		return 0, 0, nil, fmt.Errorf("unsupported bits per sample: %d (only 16 supported)", bitsPerSample)
	}

	// Find data chunk
	dataSize := int(binary.LittleEndian.Uint32(header[40:44]))
	if dataSize == 0 {
		// Read rest of file
		allData, err := io.ReadAll(r)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("cannot read data: %w", err)
		}
		dataSize = len(allData)
		// Re-read: we already consumed 44 bytes, so allData is the data chunk
		samples = bytesToInt16(allData)
		return sampleRate, channels, samples, nil
	}

	data := make([]byte, dataSize)
	if _, err = io.ReadFull(r, data); err != nil {
		return 0, 0, nil, fmt.Errorf("cannot read data chunk: %w", err)
	}

	samples = bytesToInt16(data)
	return sampleRate, channels, samples, nil
}

// WriteWAVFile writes PCM16 samples to a WAV file (helper for testing).
func WriteWAVFile(path string, samples []int16, sampleRate, channels int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	dataSize := len(samples) * 2
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+dataSize))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	byteRate := sampleRate * channels * 2
	binary.LittleEndian.PutUint32(header[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(header[32:34], uint16(channels*2))
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataSize))

	if _, err := f.Write(header); err != nil {
		return err
	}
	if _, err := f.Write(int16ToBytes(samples)); err != nil {
		return err
	}
	return nil
}
