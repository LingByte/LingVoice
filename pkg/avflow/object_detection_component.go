package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ObjectDetector defines the interface for object detection models
type ObjectDetector interface {
	// Detect performs object detection on a video frame
	Detect(frame *VideoFrame) (*ObjectDetectionResult, error)

	// Config returns the detector configuration
	Config() ObjectDetectionConfig

	// Close closes the detector
	Close() error
}

// ObjectDetectionConfig represents object detection configuration
type ObjectDetectionConfig struct {
	// Model name (e.g., "yolov8", "yolov5", "faster-rcnn")
	ModelName string

	// Confidence threshold (0.0 to 1.0)
	ConfidenceThreshold float32

	// NMS (Non-Maximum Suppression) threshold
	NMSThreshold float32

	// Classes to detect (empty means all classes)
	Classes []string

	// Max detections per frame
	MaxDetections int

	// Whether to use GPU acceleration
	UseGPU bool
}

// ObjectDetectionComponent detects objects in video frames
type ObjectDetectionComponent struct {
	BaseComponent
	detector ObjectDetector
	mu       sync.Mutex
}

// NewObjectDetectionComponent creates a new ObjectDetectionComponent
func NewObjectDetectionComponent(id string, detector ObjectDetector) *ObjectDetectionComponent {
	return &ObjectDetectionComponent{
		BaseComponent: NewBaseComponent(id, "ObjectDetection",
			[]string{"video_in"},
			[]string{"detection_out"}),
		detector: detector,
	}
}

// Process implements the Component interface
func (o *ObjectDetectionComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["video_in"]
	out := outputs["detection_out"]

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

				// Perform detection
				result, err := o.detector.Detect(frame)
				if err != nil {
					// Log error but continue processing
					continue
				}

				// Send detection results downstream
				select {
				case out <- NewPacket(PacketTypeGeneric, result):
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

// GetDetector returns the underlying object detector
func (o *ObjectDetectionComponent) GetDetector() ObjectDetector {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.detector
}

// MockObjectDetector is a mock implementation for testing
type MockObjectDetector struct {
	config ObjectDetectionConfig
}

// NewMockObjectDetector creates a new mock object detector
func NewMockObjectDetector(config ObjectDetectionConfig) *MockObjectDetector {
	return &MockObjectDetector{
		config: config,
	}
}

// Detect performs mock object detection
func (m *MockObjectDetector) Detect(frame *VideoFrame) (*ObjectDetectionResult, error) {
	if frame == nil {
		return nil, fmt.Errorf("frame is nil")
	}

	startTime := time.Now()

	// Generate mock detections
	detections := []Detection{
		{
			Box: BoundingBox{
				X:      100,
				Y:      100,
				Width:  200,
				Height: 300,
			},
			Label:      "person",
			Confidence: 0.95,
			Metadata: map[string]interface{}{
				"pose": "standing",
			},
		},
		{
			Box: BoundingBox{
				X:      400,
				Y:      150,
				Width:  150,
				Height: 200,
			},
			Label:      "car",
			Confidence: 0.87,
			Metadata: map[string]interface{}{
				"color": "red",
			},
		},
		{
			Box: BoundingBox{
				X:      50,
				Y:      50,
				Width:  100,
				Height: 100,
			},
			Label:      "dog",
			Confidence: 0.92,
			Metadata: map[string]interface{}{
				"breed": "labrador",
			},
		},
	}

	// Filter by confidence threshold
	filtered := []Detection{}
	for _, det := range detections {
		if det.Confidence >= m.config.ConfidenceThreshold {
			filtered = append(filtered, det)
		}
	}

	// Limit to max detections
	if len(filtered) > m.config.MaxDetections {
		filtered = filtered[:m.config.MaxDetections]
	}

	processingTime := int32(time.Since(startTime).Milliseconds())

	return &ObjectDetectionResult{
		Detections:    filtered,
		FrameNumber:   frame.FrameNumber,
		Timestamp:     frame.Timestamp,
		ProcessingTime: processingTime,
	}, nil
}

// Config returns the detector configuration
func (m *MockObjectDetector) Config() ObjectDetectionConfig {
	return m.config
}

// Close closes the detector
func (m *MockObjectDetector) Close() error {
	return nil
}
