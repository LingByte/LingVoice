// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package media

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRelayState_SameCodec_Active(t *testing.T) {
	rs := NewRelayState()
	rs.SetInputCodec("pcmu")
	rs.SetOutputCodec("pcmu")
	if !rs.IsActive() {
		t.Fatal("relay should be active when codecs match")
	}
	if !rs.IsEnabled() {
		t.Fatal("relay should be enabled by default")
	}
}

func TestRelayState_DifferentCodec_Inactive(t *testing.T) {
	rs := NewRelayState()
	rs.SetInputCodec("pcmu")
	rs.SetOutputCodec("pcma")
	if rs.IsActive() {
		t.Fatal("relay should NOT be active when codecs differ")
	}
}

func TestRelayState_Disabled(t *testing.T) {
	rs := NewRelayState()
	rs.SetInputCodec("pcmu")
	rs.SetOutputCodec("pcmu")
	rs.SetEnabled(false)
	if rs.IsActive() {
		t.Fatal("relay should NOT be active when disabled")
	}
	if rs.IsEnabled() {
		t.Fatal("relay should be disabled")
	}
}

func TestRelayState_EmptyCodec_Inactive(t *testing.T) {
	rs := NewRelayState()
	if rs.IsActive() {
		t.Fatal("relay should NOT be active with empty codecs")
	}
	rs.SetInputCodec("pcmu")
	if rs.IsActive() {
		t.Fatal("relay should NOT be active with only input codec set")
	}
}

func TestRelayState_ToggleAtRuntime(t *testing.T) {
	rs := NewRelayState()
	rs.SetInputCodec("pcmu")
	rs.SetOutputCodec("pcmu")
	if !rs.IsActive() {
		t.Fatal("should start active")
	}
	rs.SetEnabled(false)
	if rs.IsActive() {
		t.Fatal("should be inactive after disable")
	}
	rs.SetEnabled(true)
	if !rs.IsActive() {
		t.Fatal("should be active after re-enable")
	}
}

func TestPacketPool_GetPut(t *testing.T) {
	p := NewPacketPool()
	pkt := p.Get()
	if pkt == nil {
		t.Fatal("Get returned nil")
	}
	pkt.Payload = []byte{1, 2, 3}
	pkt.Sequence = 42
	p.Put(pkt)

	pkt2 := p.Get()
	// Should be reset
	if pkt2.Sequence != 0 {
		t.Fatalf("Sequence not reset: %d", pkt2.Sequence)
	}
	if pkt2.Payload != nil {
		t.Fatal("Payload not cleared")
	}
}

func TestPacketPool_NilPut(t *testing.T) {
	p := NewPacketPool()
	p.Put(nil) // should not panic
}

// mockTransport for session relay tests
type mockTransport struct {
	codec   CodecConfig
	sentMu  sync.Mutex
	sent    []MediaPacket
	nextCh  chan MediaPacket
	closed  atomic.Bool
	ctx     context.Context
}

func newMockTransport(codec string, sampleRate int) *mockTransport {
	return &mockTransport{
		codec:  CodecConfig{Codec: codec, SampleRate: sampleRate, Channels: 1, BitDepth: 16},
		nextCh: make(chan MediaPacket, 16),
		ctx:    context.Background(),
	}
}

func (m *mockTransport) Close() error { m.closed.Store(true); return nil }
func (m *mockTransport) String() string { return "mock" }
func (m *mockTransport) Attach(s *MediaSession) {}
func (m *mockTransport) Next(ctx context.Context) (MediaPacket, error) {
	select {
	case pkt, ok := <-m.nextCh:
		if !ok {
			return nil, io.EOF
		}
		return pkt, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (m *mockTransport) Send(ctx context.Context, packet MediaPacket) (int, error) {
	m.sentMu.Lock()
	m.sent = append(m.sent, packet)
	m.sentMu.Unlock()
	return 1, nil
}
func (m *mockTransport) SentPackets() []MediaPacket {
	m.sentMu.Lock()
	defer m.sentMu.Unlock()
	out := make([]MediaPacket, len(m.sent))
	copy(out, m.sent)
	return out
}
func (m *mockTransport) Codec() CodecConfig { return m.codec }

func TestSession_RelaySameCodec_SkipsDecodeEncode(t *testing.T) {
	s := NewDefaultSession()
	in := newMockTransport("pcmu", 8000)
	out := newMockTransport("pcmu", 8000)

	// Set dummy encoder/decoder that would corrupt data if called
	var called atomic.Bool
	s.Encode(func(pkt MediaPacket) ([]MediaPacket, error) {
		called.Store(true)
		return []MediaPacket{pkt}, nil
	})
	s.Decode(func(pkt MediaPacket) ([]MediaPacket, error) {
		called.Store(true)
		return []MediaPacket{pkt}, nil
	})

	s.Input(in)
	s.Output(out)

	if !s.IsRelayActive() {
		t.Fatal("relay should be active for same codec")
	}

	// Start session in goroutine
	go func() { _ = s.Serve() }()
	time.Sleep(50 * time.Millisecond)

	// Send a packet
	pkt := &AudioPacket{Payload: []byte{0x01, 0x02, 0x03, 0x04}}
	in.nextCh <- pkt

	time.Sleep(50 * time.Millisecond)
	_ = s.Close()
	time.Sleep(50 * time.Millisecond)

	if called.Load() {
		t.Fatal("encoder/decoder should NOT be called in relay mode")
	}
	if len(out.SentPackets()) == 0 {
		t.Fatal("packet should have been sent to output")
	}
}

func TestSession_RelayDifferentCodec_DecodesAndEncodes(t *testing.T) {
	s := NewDefaultSession()
	in := newMockTransport("pcmu", 8000)
	out := newMockTransport("pcma", 8000)

	var decodeCalled atomic.Bool
	var encodeCalled atomic.Bool
	s.Decode(func(pkt MediaPacket) ([]MediaPacket, error) {
		decodeCalled.Store(true)
		return []MediaPacket{pkt}, nil
	})
	s.Encode(func(pkt MediaPacket) ([]MediaPacket, error) {
		encodeCalled.Store(true)
		return []MediaPacket{pkt}, nil
	})

	s.Input(in)
	s.Output(out)

	if s.IsRelayActive() {
		t.Fatal("relay should NOT be active for different codecs")
	}

	go func() { _ = s.Serve() }()
	time.Sleep(50 * time.Millisecond)

	pkt := &AudioPacket{Payload: []byte{0x01, 0x02, 0x03, 0x04}}
	in.nextCh <- pkt

	time.Sleep(50 * time.Millisecond)
	_ = s.Close()
	time.Sleep(50 * time.Millisecond)

	if !decodeCalled.Load() {
		t.Fatal("decoder should be called when relay is inactive")
	}
	if !encodeCalled.Load() {
		t.Fatal("encoder should be called when relay is inactive")
	}
}

func TestSession_DroppedPacketsCounter(t *testing.T) {
	s := NewDefaultSession()
	out := newMockTransport("pcmu", 8000)

	// Small queue to force drops
	s.QueueSize = 2
	s.initEventBus()
	s.Output(out)

	// Manually send packets without starting the output loop
	tl := s.outputs[0]
	for i := 0; i < 10; i++ {
		tl.trySendPacket(&AudioPacket{Payload: []byte{byte(i)}})
	}

	dropped := s.DroppedPackets()
	if dropped == 0 {
		t.Fatal("should have dropped packets when queue is full")
	}
	if dropped > 8 {
		t.Fatalf("unexpected drop count: %d", dropped)
	}
}
