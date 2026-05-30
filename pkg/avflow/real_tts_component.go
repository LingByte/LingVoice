package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"

	"github.com/LingByte/LingVoice/pkg/media"
	"github.com/LingByte/LingVoice/pkg/synthesizer"
)

// RealTTSComponent wraps a real AudioSynthesisEngine (TTS provider) into an AVFlow component.
// It consumes text packets and produces synthesized audio packets.
// It also respects barge-in control signals to interrupt synthesis.
type RealTTSComponent struct {
	BaseComponent
	engine     synthesizer.AudioSynthesisEngine
	cancelChan chan struct{}
	cancelMu   sync.Mutex
}

// NewRealTTSComponent creates a new RealTTSComponent wrapping an AudioSynthesisEngine.
func NewRealTTSComponent(id string, engine synthesizer.AudioSynthesisEngine) *RealTTSComponent {
	return &RealTTSComponent{
		BaseComponent: NewBaseComponent(id, "RealTTS",
			[]string{"text_in", "control_in"},
			[]string{"audio_out", "playback_state_out"}),
		engine:     engine,
		cancelChan: make(chan struct{}),
	}
}

// Process implements the Component interface.
// It reads text packets, synthesizes them using the TTS engine, and streams audio results.
func (t *RealTTSComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	inText := inputs["text_in"]
	inCtrl := inputs["control_in"]
	outAudio := outputs["audio_out"]
	outState := outputs["playback_state_out"]

	// Run control signal loop (handles barge-in to stop TTS synthesizing)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case p, ok := <-inCtrl:
				if !ok {
					return
				}
				if p.Type == PacketTypeControl && p.Data == "barge-in" {
					t.cancelMu.Lock()
					close(t.cancelChan)
					t.cancelChan = make(chan struct{})
					t.cancelMu.Unlock()

					// Signal playback stop due to barge-in
					select {
					case outState <- NewPacket(PacketTypeControl, false):
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	// Main text processing loop
	for {
		select {
		case <-ctx.Done():
			return nil

		case p, ok := <-inText:
			if !ok {
				return nil
			}

			if p.Type == PacketTypeText {
				textPkt, ok := p.Data.(*media.TextPacket)
				if !ok {
					continue
				}

				// Skip end markers
				if textPkt.IsEnd {
					continue
				}

				// Notify playback start
				select {
				case outState <- NewPacket(PacketTypeControl, true):
				case <-ctx.Done():
					return nil
				}

				// Get current cancel channel
				t.cancelMu.Lock()
				currentCancel := t.cancelChan
				t.cancelMu.Unlock()

				// Create a handler to receive synthesis events
				handler := &synthesisHandler{
					outAudio:      outAudio,
					ctx:           ctx,
					currentCancel: currentCancel,
				}

				// Synthesize text using the TTS engine
				err := t.engine.Synthesize(ctx, handler, textPkt.Text)
				if err != nil {
					return fmt.Errorf("tts synthesis error: %w", err)
				}

				// Notify playback stop once complete
				select {
				case outState <- NewPacket(PacketTypeControl, false):
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

// GetEngine returns the underlying AudioSynthesisEngine.
func (t *RealTTSComponent) GetEngine() synthesizer.AudioSynthesisEngine {
	return t.engine
}

// synthesisHandler implements synthesizer.AudioSynthesisHandler to stream audio to AVFlow.
type synthesisHandler struct {
	outAudio      chan<- *Packet
	ctx           context.Context
	currentCancel <-chan struct{}
}

// OnMessage receives synthesized audio data from the TTS engine.
func (h *synthesisHandler) OnMessage(data []byte) {
	if len(data) == 0 {
		return
	}

	// Check for barge-in or context cancellation
	select {
	case <-h.ctx.Done():
		return
	case <-h.currentCancel:
		return
	default:
	}

	// Send audio packet downstream
	select {
	case h.outAudio <- NewPacket(PacketTypeAudio, &media.AudioPacket{
		Payload:       data,
		IsSynthesized: true,
	}):
	case <-h.ctx.Done():
	}
}

// OnTimestamp receives timing information from the TTS engine.
func (h *synthesisHandler) OnTimestamp(timestamp synthesizer.SentenceTimestamp) {
	// Currently not used in AVFlow, but required by interface
}
