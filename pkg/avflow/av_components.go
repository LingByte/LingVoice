package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/media"
	"github.com/LingByte/LingVoice/pkg/media/vad"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// MicComponent reads media packets from a media.MediaTransport and streams them to "audio_out".
type MicComponent struct {
	BaseComponent
	transport media.MediaTransport
}

// NewMicComponent builds a new MicComponent.
func NewMicComponent(id string, transport media.MediaTransport) *MicComponent {
	return &MicComponent{
		BaseComponent: NewBaseComponent(id, "MicInput", nil, []string{"audio_out"}),
		transport:     transport,
	}
}

func (m *MicComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	out := outputs["audio_out"]
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			pkt, err := m.transport.Next(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return fmt.Errorf("mic next error: %w", err)
			}
			if pkt != nil {
				select {
				case out <- NewPacket(PacketTypeAudio, pkt):
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

// SpeakerComponent consumes audio packets from "audio_in" and sends them to a media.MediaTransport.
type SpeakerComponent struct {
	BaseComponent
	transport media.MediaTransport
}

// NewSpeakerComponent builds a new SpeakerComponent.
func NewSpeakerComponent(id string, transport media.MediaTransport) *SpeakerComponent {
	return &SpeakerComponent{
		BaseComponent: NewBaseComponent(id, "SpeakerOutput", []string{"audio_in"}, nil),
		transport:     transport,
	}
}

func (s *SpeakerComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["audio_in"]
	for {
		select {
		case <-ctx.Done():
			return nil
		case p, ok := <-in:
			if !ok {
				return nil
			}
			if p.Type == PacketTypeAudio {
				rawPkt, ok := p.Data.(media.MediaPacket)
				if !ok {
					// Also support raw bytes as media packet wrapper if needed
					if rawBytes, isBytes := p.Data.([]byte); isBytes {
						rawPkt = &media.AudioPacket{
							Payload:       rawBytes,
							IsSynthesized: true,
						}
					}
				}
				if rawPkt != nil {
					_, err := s.transport.Send(ctx, rawPkt)
					if err != nil {
						return fmt.Errorf("speaker send error: %w", err)
					}
				}
			}
		}
	}
}

// VADComponent monitors incoming AudioPackets on "audio_in".
// If energy exceeds threshold, it routes them to "audio_out" and fires barge-in control packets to "control_out".
type VADComponent struct {
	BaseComponent
	detector *vad.Detector
	mu       sync.Mutex
	playing  bool // State track of whether synthesis playback is active
}

// NewVADComponent builds a VADComponent with a built-in RMS detector.
func NewVADComponent(id string, threshold float64) *VADComponent {
	d := vad.NewDetector()
	if threshold > 0 {
		d.SetThreshold(threshold)
	}
	return &VADComponent{
		BaseComponent: NewBaseComponent(id, "VAD", []string{"audio_in", "playback_state_in"}, []string{"audio_out", "control_out"}),
		detector:      d,
	}
}

func (v *VADComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	inAudio := inputs["audio_in"]
	inState := inputs["playback_state_in"]
	outAudio := outputs["audio_out"]
	outCtrl := outputs["control_out"]

	// Run state monitoring in a separate loop
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case p, ok := <-inState:
				if !ok {
					return
				}
				if p.Type == PacketTypeControl || p.Type == PacketTypeGeneric {
					if playing, ok := p.Data.(bool); ok {
						v.mu.Lock()
						v.playing = playing
						v.mu.Unlock()
					}
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case p, ok := <-inAudio:
			if !ok {
				return nil
			}
			if p.Type == PacketTypeAudio {
				rawPkt, ok := p.Data.(*media.AudioPacket)
				if !ok {
					continue
				}

				v.mu.Lock()
				isSynthPlaying := v.playing
				v.mu.Unlock()

				// Run barge-in detection
				bargeIn := v.detector.CheckBargeIn(rawPkt.Payload, isSynthPlaying)
				if bargeIn {
					// Send interrupt signal to downstream
					select {
					case outCtrl <- NewPacket(PacketTypeControl, "barge-in"):
					case <-ctx.Done():
						return nil
					}
				}

				// Forward non-silent packets to ASR
				select {
				case outAudio <- p:
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

// ASRComponent aggregates real-time AudioPackets on "audio_in" and produces transcription TextPackets on "text_out".
type ASRComponent struct {
	BaseComponent
	mu  sync.Mutex
	buf strings.Builder
}

// NewASRComponent builds an ASRComponent.
func NewASRComponent(id string) *ASRComponent {
	return &ASRComponent{
		BaseComponent: NewBaseComponent(id, "ASR", []string{"audio_in"}, []string{"text_out"}),
	}
}

func (a *ASRComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["audio_in"]
	out := outputs["text_out"]

	for {
		select {
		case <-ctx.Done():
			return nil
		case p, ok := <-in:
			if !ok {
				return nil
			}
			if p.Type == PacketTypeAudio {
				rawPkt, ok := p.Data.(*media.AudioPacket)
				if !ok {
					continue
				}

				// If it's a marker/end packet, flush final transcript
				if rawPkt.IsEndPacket {
					a.mu.Lock()
					text := a.buf.String()
					a.buf.Reset()
					a.mu.Unlock()

					if text != "" {
						select {
						case out <- NewPacket(PacketTypeText, &media.TextPacket{
							Text:          text,
							IsTranscribed: true,
							IsEnd:         true,
						}):
						case <-ctx.Done():
							return nil
						}
					}
					continue
				}

				// Fake ASR: convert UTF-8 payloads directly from PCM in demo mode
				payloadStr := strings.TrimSpace(string(rawPkt.Payload))
				if payloadStr != "" {
					a.mu.Lock()
					a.buf.WriteString(payloadStr)
					current := a.buf.String()
					a.mu.Unlock()

					// Send partial transcription
					select {
					case out <- NewPacket(PacketTypeText, &media.TextPacket{
						Text:          current,
						IsTranscribed: true,
						IsPartial:     true,
					}):
					case <-ctx.Done():
						return nil
					}
				}
			} else if p.Type == PacketTypeText {
				// Directly forward pre-transcribed text packets (useful for hybrid flows)
				select {
				case out <- p:
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

// LLMComponent consumes transcribed TextPackets on "text_in", runs an LLM generator, and streams response TextPackets to "text_out".
type LLMComponent struct {
	BaseComponent
	promptPrefix string
	// User can supply a custom LLM handler function
	handler func(ctx context.Context, prompt string) (<-chan string, error)
}

// NewLLMComponent builds a new LLMComponent.
func NewLLMComponent(id string, prefix string, handler func(context.Context, string) (<-chan string, error)) *LLMComponent {
	if prefix == "" {
		prefix = "助手："
	}
	return &LLMComponent{
		BaseComponent: NewBaseComponent(id, "LLM", []string{"text_in"}, []string{"text_out"}),
		promptPrefix:  prefix,
		handler:       handler,
	}
}

func (l *LLMComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["text_in"]
	out := outputs["text_out"]

	for {
		select {
		case <-ctx.Done():
			return nil
		case p, ok := <-in:
			if !ok {
				return nil
			}
			if p.Type == PacketTypeText {
				textPkt, ok := p.Data.(*media.TextPacket)
				if !ok {
					continue
				}
				// Wait until we have the final utterance transcription before triggering the cognitive LLM
				if textPkt.IsPartial || textPkt.Text == "" {
					continue
				}

				// Execute model
				var respChan <-chan string
				var err error
				if l.handler != nil {
					respChan, err = l.handler(ctx, textPkt.Text)
				} else {
					// Fallback to simple echo mock model
					ch := make(chan string, 1)
					ch <- l.promptPrefix + textPkt.Text
					close(ch)
					respChan = ch
				}

				if err != nil {
					return fmt.Errorf("llm generate error: %w", err)
				}

				// Stream response words/characters to downstream
				seq := 1
				for word := range respChan {
					select {
					case <-ctx.Done():
						return nil
					case out <- NewPacket(PacketTypeText, &media.TextPacket{
						Text:           word,
						IsLLMGenerated: true,
						IsPartial:      true,
						Sequence:       seq,
					}):
						seq++
					}
				}

				// Signal end of LLM turn
				select {
				case <-ctx.Done():
					return nil
				case out <- NewPacket(PacketTypeText, &media.TextPacket{
					IsLLMGenerated: true,
					IsEnd:          true,
					Sequence:       seq,
				}):
				}
			}
		}
	}
}

// TTSComponent consumes TextPackets on "text_in" and produces synthesized AudioPackets on "audio_out" and status on "playback_state_out".
type TTSComponent struct {
	BaseComponent
	cancelChan chan struct{}
	cancelMu   sync.Mutex
}

// NewTTSComponent builds a TTSComponent.
func NewTTSComponent(id string) *TTSComponent {
	return &TTSComponent{
		BaseComponent: NewBaseComponent(id, "TTS", []string{"text_in", "control_in"}, []string{"audio_out", "playback_state_out"}),
		cancelChan:    make(chan struct{}),
	}
}

func (t *TTSComponent) Process(
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

				if textPkt.IsEnd {
					continue
				}

				// Notify playback start
				select {
				case outState <- NewPacket(PacketTypeControl, true):
				case <-ctx.Done():
					return nil
				}

				// Synthesize text
				text := textPkt.Text
				synthBytes := []byte("[TTS:" + text + "]")
				chunkSz := 64

				t.cancelMu.Lock()
				currentCancel := t.cancelChan
				t.cancelMu.Unlock()

				interrupted := false
				for i := 0; i < len(synthBytes); i += chunkSz {
					// Check for barge-in or context cancellation
					select {
					case <-ctx.Done():
						return nil
					case <-currentCancel:
						interrupted = true
						break
					default:
					}

					if interrupted {
						break
					}

					end := i + chunkSz
					if end > len(synthBytes) {
						end = len(synthBytes)
					}

					// Send Audio Frame
					select {
					case outAudio <- NewPacket(PacketTypeAudio, &media.AudioPacket{
						Payload:       synthBytes[i:end],
						IsSynthesized: true,
						IsFirstPacket: i == 0,
						IsEndPacket:   end == len(synthBytes),
					}):
					case <-ctx.Done():
						return nil
					}

					time.Sleep(30 * time.Millisecond) // Simulated audio pace
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

// ConvertSchemaToMessages merges the given history with new text packet into a standard list of Eino schema.Messages.
func ConvertSchemaToMessages(history []*schema.Message, newText string) []*schema.Message {
	return append(history, schema.UserMessage(newText))
}
