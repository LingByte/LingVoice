package main

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/LingByte/LingVoice/pkg/avflow"
	"github.com/LingByte/LingVoice/pkg/media"
	"github.com/LingByte/LingVoice/pkg/recognizer"
	"github.com/LingByte/LingVoice/pkg/runtime/av"
	"github.com/LingByte/LingVoice/pkg/synthesizer"
)

// MockASREngine is a simple mock implementation of SpeechRecognitionEngine for demo purposes.
type MockASREngine struct {
	id string
}

func NewMockASREngine(id string) *MockASREngine {
	return &MockASREngine{id: id}
}

func (m *MockASREngine) ID() string   { return m.id }
func (m *MockASREngine) Type() string { return "local" }

func (m *MockASREngine) Init(resultCallback recognizer.SpeechRecognitionResult, errorCallback recognizer.RecognitionError) {
	// Store callbacks if needed
}

func (m *MockASREngine) Vendor() string                       { return "mock-asr" }
func (m *MockASREngine) ConnAndReceive(dialogId string) error { return nil }
func (m *MockASREngine) Activity() bool                       { return true }
func (m *MockASREngine) RestartClient()                       {}

func (m *MockASREngine) SendAudioBytes(data []byte) error {
	// Mock: just echo the data as text
	return nil
}

func (m *MockASREngine) SendEnd() error  { return nil }
func (m *MockASREngine) StopConn() error { return nil }

// MockTTSEngine is a simple mock implementation of AudioSynthesisEngine for demo purposes.
type MockTTSEngine struct {
	id string
}

func NewMockTTSEngine(id string) *MockTTSEngine {
	return &MockTTSEngine{id: id}
}

func (m *MockTTSEngine) ID() string   { return m.id }
func (m *MockTTSEngine) Type() string { return "local" }

func (m *MockTTSEngine) Provider() synthesizer.TTSProvider { return "local" }

func (m *MockTTSEngine) Format() media.StreamFormat {
	return media.StreamFormat{
		SampleRate:    16000,
		Channels:      1,
		BitDepth:      16,
		FrameDuration: 20,
	}
}

func (m *MockTTSEngine) CacheKey(text string) string {
	return "mock-tts-" + text
}

func (m *MockTTSEngine) Synthesize(ctx context.Context, handler synthesizer.AudioSynthesisHandler, text string) error {
	// Mock: simulate TTS by sending fake audio chunks
	mockAudio := []byte("[TTS:" + text + "]")
	chunkSize := 32

	for i := 0; i < len(mockAudio); i += chunkSize {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		end := i + chunkSize
		if end > len(mockAudio) {
			end = len(mockAudio)
		}

		handler.OnMessage(mockAudio[i:end])
		time.Sleep(10 * time.Millisecond) // Simulate synthesis delay
	}

	return nil
}

func (m *MockTTSEngine) Close() error { return nil }

// buildAudioLLMGraph constructs an AVFlow graph with real ASR/TTS components.
func buildAudioLLMGraph(asrEngine recognizer.SpeechRecognitionEngine, ttsEngine synthesizer.AudioSynthesisEngine) (*avflow.Graph, error) {
	// Create transport for mic/speaker
	transport := av.NewChanTransport("demo-transport", "duplex", 64)

	// Create components
	mic := avflow.NewMicComponent("mic", transport)
	vad := avflow.NewVADComponent("vad", 0.5)
	asr := avflow.NewRealASRComponent("asr", asrEngine)
	llm := avflow.NewLLMComponent("llm", "LLM: ", func(ctx context.Context, prompt string) (<-chan string, error) {
		// Simple echo LLM: prepend "LLM: " to user input
		ch := make(chan string, 1)
		ch <- prompt
		close(ch)
		return ch, nil
	})
	tts := avflow.NewRealTTSComponent("tts", ttsEngine)
	speaker := avflow.NewSpeakerComponent("speaker", transport)

	// Build graph
	g := avflow.NewGraph("audio-llm-graph")
	g.AddComponent(mic)
	g.AddComponent(vad)
	g.AddComponent(asr)
	g.AddComponent(llm)
	g.AddComponent(tts)
	g.AddComponent(speaker)

	// Connect ports: Mic -> VAD -> ASR -> LLM -> TTS -> Speaker
	g.Connect("mic", "audio_out", "vad", "audio_in")
	g.Connect("vad", "audio_out", "asr", "audio_in")
	g.Connect("vad", "control_out", "tts", "control_in") // Barge-in signal
	g.Connect("asr", "text_out", "llm", "text_in")
	g.Connect("llm", "text_out", "tts", "text_in")
	g.Connect("tts", "audio_out", "speaker", "audio_in")

	return g, nil
}

func main() {
	fmt.Println("=== AVFlow ASR/TTS Integration Demo ===")
	fmt.Println()

	// Create mock ASR and TTS engines
	asrEngine := NewMockASREngine("mock-asr-1")
	ttsEngine := NewMockTTSEngine("mock-tts-1")

	fmt.Printf("ASR Engine: %s (Type: %v)\n", asrEngine.Vendor(), asrEngine.Type())
	fmt.Printf("TTS Engine: %s (Type: %v)\n", ttsEngine.Provider(), ttsEngine.Type())
	fmt.Println()

	// Build the audio+LLM graph
	graph, err := buildAudioLLMGraph(asrEngine, ttsEngine)
	if err != nil {
		log.Fatalf("Failed to build graph: %v", err)
	}

	fmt.Println("Graph structure:")
	fmt.Println("  Mic -> VAD -> ASR -> LLM -> TTS -> Speaker")
	fmt.Println()

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Run the graph
	fmt.Println("Starting graph execution...")
	if err := graph.Run(ctx); err != nil {
		log.Fatalf("Graph execution failed: %v", err)
	}

	fmt.Println("Graph execution completed successfully!")
	fmt.Println()
	fmt.Println("=== Demo Complete ===")
}
