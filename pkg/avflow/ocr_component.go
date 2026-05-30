package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// OCREngine defines the interface for optical character recognition
type OCREngine interface {
	// Recognize performs OCR on a video frame
	Recognize(frame *VideoFrame) (*OCRResult, error)

	// Config returns the OCR configuration
	Config() OCRConfig

	// Close closes the OCR engine
	Close() error
}

// OCRConfig represents OCR configuration
type OCRConfig struct {
	// Model name (e.g., "tesseract", "paddleocr", "easyocr")
	ModelName string

	// Confidence threshold (0.0 to 1.0)
	ConfidenceThreshold float32

	// Languages to recognize
	Languages []string

	// Whether to use GPU acceleration
	UseGPU bool
}

// OCRComponent performs optical character recognition on video frames
type OCRComponent struct {
	BaseComponent
	engine OCREngine
	mu     sync.Mutex
}

// NewOCRComponent creates a new OCRComponent
func NewOCRComponent(id string, engine OCREngine) *OCRComponent {
	return &OCRComponent{
		BaseComponent: NewBaseComponent(id, "OCR",
			[]string{"video_in"},
			[]string{"text_out"}),
		engine: engine,
	}
}

// Process implements the Component interface
func (o *OCRComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["video_in"]
	out := outputs["text_out"]

	for {
		select {
		case <-ctx.Done():
			return nil

		case p, ok := <-in:
			if !ok {
				return nil
			}

			if p.Type == PacketTypeVideo {
				frame, ok := p.Data.(*VideoFrame)
				if !ok {
					continue
				}

				// Perform OCR
				result, err := o.engine.Recognize(frame)
				if err != nil {
					// Log error but continue processing
					continue
				}

				// Send OCR results downstream
				select {
				case out <- NewPacket(PacketTypeGeneric, result):
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

// GetEngine returns the underlying OCR engine
func (o *OCRComponent) GetEngine() OCREngine {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.engine
}

// MockOCREngine is a mock implementation for testing
type MockOCREngine struct {
	config OCRConfig
}

// NewMockOCREngine creates a new mock OCR engine
func NewMockOCREngine(config OCRConfig) *MockOCREngine {
	return &MockOCREngine{
		config: config,
	}
}

// Recognize performs mock OCR
func (m *MockOCREngine) Recognize(frame *VideoFrame) (*OCRResult, error) {
	if frame == nil {
		return nil, fmt.Errorf("frame is nil")
	}

	startTime := time.Now()

	// Generate mock OCR results
	textRegions := []TextRegion{
		{
			Text: "Hello World",
			Box: BoundingBox{
				X:      50,
				Y:      50,
				Width:  200,
				Height: 50,
			},
			Confidence: 0.95,
		},
		{
			Text: "LingVoice Framework",
			Box: BoundingBox{
				X:      50,
				Y:      120,
				Width:  300,
				Height: 50,
			},
			Confidence: 0.92,
		},
		{
			Text: "AI Audio/Video Orchestration",
			Box: BoundingBox{
				X:      50,
				Y:      190,
				Width:  400,
				Height: 50,
			},
			Confidence: 0.88,
		},
	}

	// Filter by confidence threshold
	filtered := []TextRegion{}
	fullText := ""
	for _, region := range textRegions {
		if region.Confidence >= m.config.ConfidenceThreshold {
			filtered = append(filtered, region)
			if fullText != "" {
				fullText += " "
			}
			fullText += region.Text
		}
	}

	processingTime := int32(time.Since(startTime).Milliseconds())

	return &OCRResult{
		Text:           fullText,
		TextRegions:    filtered,
		Confidence:     0.92,
		FrameNumber:    frame.FrameNumber,
		Timestamp:      frame.Timestamp,
		ProcessingTime: processingTime,
	}, nil
}

// Config returns the OCR configuration
func (m *MockOCREngine) Config() OCRConfig {
	return m.config
}

// Close closes the OCR engine
func (m *MockOCREngine) Close() error {
	return nil
}
