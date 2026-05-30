package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/media"
	"github.com/LingByte/LingVoice/pkg/recognizer"
)

// RealASRComponent wraps a real SpeechRecognitionEngine (ASR provider) into an AVFlow component.
// It consumes audio packets and produces transcription text packets.
type RealASRComponent struct {
	BaseComponent
	engine recognizer.SpeechRecognitionEngine
	mu     sync.Mutex
}

// NewRealASRComponent creates a new RealASRComponent wrapping a SpeechRecognitionEngine.
func NewRealASRComponent(id string, engine recognizer.SpeechRecognitionEngine) *RealASRComponent {
	return &RealASRComponent{
		BaseComponent: NewBaseComponent(id, "RealASR",
			[]string{"audio_in"},
			[]string{"text_out"}),
		engine: engine,
	}
}

// Process implements the Component interface.
// It reads audio packets, sends them to the ASR engine, and streams transcription results.
func (r *RealASRComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["audio_in"]
	out := outputs["text_out"]

	// Initialize the ASR engine with callbacks
	r.engine.Init(
		// SpeechRecognitionResult callback
		func(text string, isLast bool, duration time.Duration, uuid string) {
			if text == "" {
				return
			}
			// Send transcription to downstream
			select {
			case out <- NewPacket(PacketTypeText, &media.TextPacket{
				Text:          text,
				IsTranscribed: true,
				IsPartial:     !isLast,
				IsEnd:         isLast,
			}):
			case <-ctx.Done():
			}
		},
		// RecognitionError callback
		func(err error, isFatal bool) {
			if isFatal {
				// Log fatal error and potentially restart
				// In production, implement proper error handling and recovery
			} else {
				// Non-fatal error, restart client
				r.engine.RestartClient()
			}
		},
	)

	// Connect and start receiving
	if err := r.engine.ConnAndReceive(""); err != nil {
		return fmt.Errorf("asr connect error: %w", err)
	}

	// Main processing loop
	for {
		select {
		case <-ctx.Done():
			// Cleanup on context cancellation
			_ = r.engine.SendEnd()
			_ = r.engine.StopConn()
			return nil

		case p, ok := <-in:
			if !ok {
				// Input channel closed, finalize ASR
				_ = r.engine.SendEnd()
				_ = r.engine.StopConn()
				return nil
			}

			if p.Type == PacketTypeAudio {
				rawPkt, ok := p.Data.(*media.AudioPacket)
				if !ok {
					continue
				}

				// Send audio bytes to ASR engine
				if err := r.engine.SendAudioBytes(rawPkt.Payload); err != nil {
					// On error, attempt to restart the client
					r.engine.RestartClient()
				}

				// If this is an end packet, signal end of audio
				if rawPkt.IsEndPacket {
					_ = r.engine.SendEnd()
				}
			}
		}
	}
}

// GetEngine returns the underlying SpeechRecognitionEngine.
func (r *RealASRComponent) GetEngine() recognizer.SpeechRecognitionEngine {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.engine
}
