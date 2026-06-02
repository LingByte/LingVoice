package main

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/LingByte/LingVoice/pkg/avflow"
	"github.com/LingByte/LingVoice/pkg/media"
	"github.com/LingByte/LingVoice/pkg/runtime/av"
)

func main() {
	utterance := flag.String("say", "你好 统一编排工具", "simulated user utterance")
	bargeInWord := flag.String("barge-in-say", "不要说了", "utterance to inject to test barge-in mid-playback")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Println("==================================================================")
	fmt.Println("  AVFlow: Unified AI Voice & LLM Orchestration Component Graph  ")
	fmt.Println("==================================================================")

	// 1. Initialize Mock Inputs/Outputs (Local Microphone & Speaker Transports)
	rx := av.NewChanTransport("mic", media.DirectionInput, 16)
	tx := av.NewChanTransport("speaker", media.DirectionOutput, 16)

	// Create unified media session
	session := media.NewDefaultSession().
		Context(ctx).
		Input(rx).
		Output(tx)
	defer session.Close()

	// 2. Initialize the Unified Component Graph
	g := avflow.NewGraph("AI-Voice-Session")

	// 3. Instantiate Components (Everything is a Component!)
	micNode := avflow.NewMicComponent("mic-input", rx)
	speakerNode := avflow.NewSpeakerComponent("speaker-output", tx)
	vadNode := avflow.NewVADComponent("vad-gating", 1000.0) // RMS VAD Threshold
	asrNode := avflow.NewASRComponent("asr-transcriber")
	ttsNode := avflow.NewTTSComponent("tts-synthesizer")

	// Custom LLM Handler Component (simulate an intelligent streaming LLM agent)
	llmNode := avflow.NewLLMComponent("llm-agent", "助手：", func(c context.Context, prompt string) (<-chan string, error) {
		ch := make(chan string, 4)
		go func() {
			defer close(ch)
			words := []string{"我已经", "收到", "您说的话：", `"` + prompt + `"`, "。这是一个基于全新 AVFlow 的统一编排示例！"}
			for _, w := range words {
				select {
				case <-c.Done():
					return
				case ch <- w:
					time.Sleep(150 * time.Millisecond) // Simulated stream pace
				}
			}
		}()
		return ch, nil
	})

	// 4. Assemble Components into the Graph
	g.AddComponent(micNode).
		AddComponent(speakerNode).
		AddComponent(vadNode).
		AddComponent(asrNode).
		AddComponent(llmNode).
		AddComponent(ttsNode)

	// 5. Connect Port Channels (Define Unified Data Flow Topology!)
	// Standard Forward audio-to-text-to-LLM-to-TTS-to-speaker path
	g.Connect("mic-input", "audio_out", "vad-gating", "audio_in")
	g.Connect("vad-gating", "audio_out", "asr-transcriber", "audio_in")
	g.Connect("asr-transcriber", "text_out", "llm-agent", "text_in")
	g.Connect("llm-agent", "text_out", "tts-synthesizer", "text_in")
	g.Connect("tts-synthesizer", "audio_out", "speaker-output", "audio_in")

	// Feedback controls (The secret sauce of our Component Graph!)
	// Feedback 1: Playback State Loop. Inform VAD whether TTS is speaking so it knows to monitor for barge-in
	g.Connect("tts-synthesizer", "playback_state_out", "vad-gating", "playback_state_in")
	// Feedback 2: Barge-In Loop. Inform TTS synthesizer to cancel active stream when VAD detects speech
	g.Connect("vad-gating", "control_out", "tts-synthesizer", "control_in")

	// 6. Compile and Run the Graph in a non-blocking goroutine
	gCtx, cancelGraph := context.WithCancel(ctx)
	defer cancelGraph()

	errChan := make(chan error, 1)
	go func() {
		errChan <- g.Run(gCtx)
	}()

	// 7. Simulate User Interaction
	time.Sleep(200 * time.Millisecond) // Wait for worker startup
	fmt.Printf("\n[Simulation] User starts speaking: %q\n", *utterance)

	// Inject speech packets
	for _, chunk := range chunkString(*utterance, 8) {
		rx.Inject(&media.AudioPacket{Payload: []byte(chunk)})
		time.Sleep(20 * time.Millisecond)
	}

	// Trigger final transcription flush (end of user turn)
	rx.Inject(&media.AudioPacket{
		IsEndPacket: true,
	})

	// 8. Capture and Output the Synthesized Speaker Frames
	doneChan := make(chan struct{})
	go func() {
		defer close(doneChan)
		for {
			select {
			case <-ctx.Done():
				return
			case pkt, ok := <-tx.Out():
				if !ok {
					return
				}
				if ap, ok := pkt.(*media.AudioPacket); ok && ap.IsSynthesized {
					fmt.Printf("[Downlink Speaker] Playback: %q\n", string(ap.Payload))

					// Let's test barge-in midway!
					// If Speaker plays "收到", simulate user interrupting with "不要说了"
					if strings.Contains(string(ap.Payload), "收到") && *bargeInWord != "" {
						fmt.Printf("\n[Simulation] USER BARGES IN: %q (Interrupting synthesized playback!)\n", *bargeInWord)
						// Simulate microphone energy spiking (barge-in event)
						// VAD expects high energy values. Let's send a standard loud PCM audio packet.
						// High values in bytes (e.g. 0x7F, 0x7F) represent highly loud PCM frames
						rx.Inject(&media.AudioPacket{
							Payload: []byte{0x7F, 0x7F, 0x7F, 0x7F, 0x7F, 0x7F, 0x7F, 0x7F},
						})
						// Also inject the barge-in speech content for subsequent turn
						for _, chunk := range chunkString(*bargeInWord, 8) {
							rx.Inject(&media.AudioPacket{Payload: []byte(chunk)})
							time.Sleep(20 * time.Millisecond)
						}
						rx.Inject(&media.AudioPacket{
							IsEndPacket: true,
						})
						*bargeInWord = "" // Only barge-in once
					}
				}
			}
		}
	}()

	// Wait for completion, timeout, or cancellation
	select {
	case <-ctx.Done():
		fmt.Println("\nProcess interrupted by system signal.")
	case err := <-errChan:
		if err != nil {
			fmt.Printf("\nGraph execution failed: %v\n", err)
		}
	case <-time.After(6 * time.Second):
		fmt.Println("\nSimulation session finished successfully.")
	}

	cancelGraph()
	<-doneChan
	fmt.Println("==================================================================")
	fmt.Println("                     AVFlow Session Closed                        ")
	fmt.Println("==================================================================")
}

func chunkString(s string, n int) []string {
	if n <= 0 {
		return []string{s}
	}
	runes := []rune(s)
	var out []string
	for i := 0; i < len(runes); i += n {
		end := i + n
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}
