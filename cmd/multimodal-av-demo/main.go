package main

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/LingByte/LingVoice/pkg/avflow"
	"github.com/LingByte/LingVoice/pkg/runtime/av"
)

func main() {
	fmt.Println("=== LingVoice Multimodal Audio+Video Demo ===")
	fmt.Println()

	// Step 1: Create video configuration
	videoConfig := avflow.VideoConfig{
		Width:       1280,
		Height:      720,
		FrameRate:   30,
		PixelFormat: avflow.PixelFormatYUV420P,
		Codec:       avflow.CodecH264,
		Bitrate:     2500,
		BufferSize:  64,
	}

	fmt.Printf("Video Config: %dx%d @ %d fps\n", videoConfig.Width, videoConfig.Height, videoConfig.FrameRate)
	fmt.Println()

	// Step 2: Create video capture and render
	videoCapture := avflow.NewMockVideoCaptureSource(videoConfig)
	videoRender := avflow.NewMockVideoRenderTarget(videoConfig)

	// Step 3: Create object detector
	objectDetectorConfig := avflow.ObjectDetectionConfig{
		ModelName:               "yolov8",
		ConfidenceThreshold:     0.5,
		NMSThreshold:            0.45,
		MaxDetections:           100,
		UseGPU:                  false,
	}
	objectDetector := avflow.NewMockObjectDetector(objectDetectorConfig)

	// Step 4: Create face detector
	faceDetectorConfig := avflow.FaceDetectionConfig{
		ModelName:           "retinaface",
		ConfidenceThreshold: 0.7,
		DetectLandmarks:     true,
		EnableTracking:      true,
		MaxFaces:            10,
		UseGPU:              false,
	}
	faceDetector := avflow.NewMockFaceDetector(faceDetectorConfig)

	// Step 5: Create emotion detector
	emotionDetectorConfig := avflow.EmotionDetectionConfig{
		ModelName:           "affectnet",
		ConfidenceThreshold: 0.6,
		Emotions:            []string{"happy", "sad", "angry", "neutral", "surprised", "disgusted", "fearful"},
		UseGPU:              false,
	}
	emotionDetector := avflow.NewMockEmotionDetector(emotionDetectorConfig)

	// Step 6: Create transport for audio
	audioTransport := av.NewChanTransport("demo-audio", "duplex", 64)

	// Step 7: Create AVFlow components
	fmt.Println("Creating AVFlow components...")

	// Video components
	videoCaptureComp := avflow.NewVideoCaptureComponent("video-capture", videoCapture)
	videoRenderComp := avflow.NewVideoRenderComponent("video-render", videoRender)
	objectDetectComp := avflow.NewObjectDetectionComponent("object-detect", objectDetector)
	faceDetectComp := avflow.NewFaceDetectionComponent("face-detect", faceDetector)
	emotionDetectComp := avflow.NewEmotionDetectionComponent("emotion-detect", emotionDetector)

	// Audio components
	micComp := avflow.NewMicComponent("mic", audioTransport)
	vadComp := avflow.NewVADComponent("vad", 0.5)
	speakerComp := avflow.NewSpeakerComponent("speaker", audioTransport)

	// LLM component (context-aware based on video analysis)
	llmComp := avflow.NewLLMComponent("llm", "Assistant: ", func(ctx context.Context, prompt string) (<-chan string, error) {
		// Simple LLM that responds based on input
		ch := make(chan string, 1)
		ch <- fmt.Sprintf("I understand you said: '%s'. Based on the video analysis, I can see people with different emotions.", prompt)
		close(ch)
		return ch, nil
	})

	fmt.Println("✓ Components created")
	fmt.Println()

	// Step 8: Build the graph
	fmt.Println("Building AVFlow graph...")

	g := avflow.NewGraph("multimodal-av-pipeline")
	g.WithBufferSize(64)

	// Add all components
	g.AddComponent(videoCaptureComp)
	g.AddComponent(videoRenderComp)
	g.AddComponent(objectDetectComp)
	g.AddComponent(faceDetectComp)
	g.AddComponent(emotionDetectComp)
	g.AddComponent(micComp)
	g.AddComponent(vadComp)
	g.AddComponent(speakerComp)
	g.AddComponent(llmComp)

	// Connect video pipeline
	// VideoCapture → ObjectDetect → VideoRender
	g.Connect("video-capture", "video_out", "object-detect", "video_in")
	g.Connect("object-detect", "detection_out", "video-render", "video_in")

	// VideoCapture → FaceDetect → EmotionDetect
	g.Connect("video-capture", "video_out", "face-detect", "video_in")
	g.Connect("face-detect", "face_out", "emotion-detect", "face_in")

	// Connect audio pipeline
	// Mic → VAD → Speaker
	g.Connect("mic", "audio_out", "vad", "audio_in")
	g.Connect("vad", "audio_out", "speaker", "audio_in")

	// Connect audio to LLM (simple text placeholder)
	// In a real system, you would have ASR → LLM → TTS

	fmt.Println("✓ Graph built with connections:")
	fmt.Println("  Video: Capture → ObjectDetect → Render")
	fmt.Println("  Video: Capture → FaceDetect → EmotionDetect")
	fmt.Println("  Audio: Mic → VAD → Speaker")
	fmt.Println()

	// Step 9: Run the graph
	fmt.Println("Starting graph execution...")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	startTime := time.Now()

	if err := g.Run(ctx); err != nil {
		log.Fatalf("Graph execution failed: %v", err)
	}

	elapsed := time.Since(startTime)

	fmt.Println()
	fmt.Println("=== Execution Complete ===")
	fmt.Printf("Duration: %v\n", elapsed)
	fmt.Printf("Video Frames Rendered: %d\n", videoRender.GetFrameCount())
	fmt.Println()
	fmt.Println("✓ Multimodal audio+video pipeline executed successfully!")
	fmt.Println()
	fmt.Println("=== Demo Complete ===")
}
