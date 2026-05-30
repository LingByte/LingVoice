package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// FaceDetector defines the interface for face detection models
type FaceDetector interface {
	// Detect performs face detection on a video frame
	Detect(frame *VideoFrame) (*FaceDetectionResult, error)

	// Config returns the detector configuration
	Config() FaceDetectionConfig

	// Close closes the detector
	Close() error
}

// FaceDetectionConfig represents face detection configuration
type FaceDetectionConfig struct {
	// Model name (e.g., "retinaface", "mtcnn", "ssd")
	ModelName string

	// Confidence threshold (0.0 to 1.0)
	ConfidenceThreshold float32

	// Whether to detect landmarks (eyes, nose, mouth, etc.)
	DetectLandmarks bool

	// Whether to track faces across frames
	EnableTracking bool

	// Max faces to detect per frame
	MaxFaces int

	// Whether to use GPU acceleration
	UseGPU bool
}

// FaceDetectionComponent detects faces in video frames
type FaceDetectionComponent struct {
	BaseComponent
	detector FaceDetector
	trackID int32
	mu      sync.Mutex
}

// NewFaceDetectionComponent creates a new FaceDetectionComponent
func NewFaceDetectionComponent(id string, detector FaceDetector) *FaceDetectionComponent {
	return &FaceDetectionComponent{
		BaseComponent: NewBaseComponent(id, "FaceDetection",
			[]string{"video_in"},
			[]string{"face_out"}),
		detector: detector,
		trackID: 0,
	}
}

// Process implements the Component interface
func (f *FaceDetectionComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["video_in"]
	out := outputs["face_out"]

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

				// Perform face detection
				result, err := f.detector.Detect(frame)
				if err != nil {
					// Log error but continue processing
					continue
				}

				// Assign track IDs if tracking is enabled
				if f.detector.Config().EnableTracking {
					f.mu.Lock()
					for i := range result.Faces {
						result.Faces[i].TrackID = f.trackID
						f.trackID++
					}
					f.mu.Unlock()
				}

				// Send face detection results downstream
				select {
				case out <- NewPacket(PacketTypeGeneric, result):
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

// GetDetector returns the underlying face detector
func (f *FaceDetectionComponent) GetDetector() FaceDetector {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.detector
}

// MockFaceDetector is a mock implementation for testing
type MockFaceDetector struct {
	config FaceDetectionConfig
}

// NewMockFaceDetector creates a new mock face detector
func NewMockFaceDetector(config FaceDetectionConfig) *MockFaceDetector {
	return &MockFaceDetector{
		config: config,
	}
}

// Detect performs mock face detection
func (m *MockFaceDetector) Detect(frame *VideoFrame) (*FaceDetectionResult, error) {
	if frame == nil {
		return nil, fmt.Errorf("frame is nil")
	}

	startTime := time.Now()

	// Generate mock face detections
	faces := []FaceDetection{
		{
			Box: BoundingBox{
				X:      150,
				Y:      100,
				Width:  150,
				Height: 200,
			},
			Confidence: 0.98,
			Landmarks: map[string][2]float32{
				"left_eye":   {180, 140},
				"right_eye":  {270, 140},
				"nose":       {225, 170},
				"left_mouth": {190, 210},
				"right_mouth": {260, 210},
			},
			Metadata: map[string]interface{}{
				"age_range": "25-35",
				"gender":    "female",
			},
		},
		{
			Box: BoundingBox{
				X:      600,
				Y:      150,
				Width:  140,
				Height: 190,
			},
			Confidence: 0.95,
			Landmarks: map[string][2]float32{
				"left_eye":   {625, 185},
				"right_eye":  {710, 185},
				"nose":       {667, 215},
				"left_mouth": {635, 250},
				"right_mouth": {700, 250},
			},
			Metadata: map[string]interface{}{
				"age_range": "30-40",
				"gender":    "male",
			},
		},
	}

	// Filter by confidence threshold
	filtered := []FaceDetection{}
	for _, face := range faces {
		if face.Confidence >= m.config.ConfidenceThreshold {
			// Remove landmarks if not requested
			if !m.config.DetectLandmarks {
				face.Landmarks = nil
			}
			filtered = append(filtered, face)
		}
	}

	// Limit to max faces
	if len(filtered) > m.config.MaxFaces {
		filtered = filtered[:m.config.MaxFaces]
	}

	processingTime := int32(time.Since(startTime).Milliseconds())

	return &FaceDetectionResult{
		Faces:          filtered,
		FrameNumber:    frame.FrameNumber,
		Timestamp:      frame.Timestamp,
		ProcessingTime: processingTime,
	}, nil
}

// Config returns the detector configuration
func (m *MockFaceDetector) Config() FaceDetectionConfig {
	return m.config
}

// Close closes the detector
func (m *MockFaceDetector) Close() error {
	return nil
}
