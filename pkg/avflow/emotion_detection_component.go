package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// EmotionDetector defines the interface for emotion detection models
type EmotionDetector interface {
	// Detect performs emotion detection on face detections
	Detect(faceResult *FaceDetectionResult) ([]EmotionResult, error)

	// Config returns the detector configuration
	Config() EmotionDetectionConfig

	// Close closes the detector
	Close() error
}

// EmotionDetectionConfig represents emotion detection configuration
type EmotionDetectionConfig struct {
	// Model name (e.g., "fer2013", "affectnet", "vggface")
	ModelName string

	// Confidence threshold (0.0 to 1.0)
	ConfidenceThreshold float32

	// Emotions to detect
	Emotions []string // e.g., ["happy", "sad", "angry", "neutral", "surprised", "disgusted", "fearful"]

	// Whether to use GPU acceleration
	UseGPU bool
}

// EmotionDetectionComponent detects emotions from face detections
type EmotionDetectionComponent struct {
	BaseComponent
	detector EmotionDetector
	mu       sync.Mutex
}

// NewEmotionDetectionComponent creates a new EmotionDetectionComponent
func NewEmotionDetectionComponent(id string, detector EmotionDetector) *EmotionDetectionComponent {
	return &EmotionDetectionComponent{
		BaseComponent: NewBaseComponent(id, "EmotionDetection",
			[]string{"face_in"},
			[]string{"emotion_out"}),
		detector: detector,
	}
}

// Process implements the Component interface
func (e *EmotionDetectionComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["face_in"]
	out := outputs["emotion_out"]

	for {
		select {
		case <-ctx.Done():
			return nil

		case p, ok := <-in:
			if !ok {
				return nil
			}

			if p.Type == PacketTypeGeneric {
				faceResult, ok := p.Data.(*FaceDetectionResult)
				if !ok {
					continue
				}

				// Perform emotion detection
				emotions, err := e.detector.Detect(faceResult)
				if err != nil {
					// Log error but continue processing
					continue
				}

				// Send emotion results downstream
				for _, emotion := range emotions {
					select {
					case out <- NewPacket(PacketTypeGeneric, emotion):
					case <-ctx.Done():
						return nil
					}
				}
			}
		}
	}
}

// GetDetector returns the underlying emotion detector
func (e *EmotionDetectionComponent) GetDetector() EmotionDetector {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.detector
}

// MockEmotionDetector is a mock implementation for testing
type MockEmotionDetector struct {
	config EmotionDetectionConfig
}

// NewMockEmotionDetector creates a new mock emotion detector
func NewMockEmotionDetector(config EmotionDetectionConfig) *MockEmotionDetector {
	return &MockEmotionDetector{
		config: config,
	}
}

// Detect performs mock emotion detection
func (m *MockEmotionDetector) Detect(faceResult *FaceDetectionResult) ([]EmotionResult, error) {
	if faceResult == nil {
		return nil, fmt.Errorf("face result is nil")
	}

	var emotions []EmotionResult

	// Generate mock emotion results for each detected face
	emotionScores := []map[string]float32{
		{
			"happy":     0.85,
			"sad":       0.05,
			"angry":     0.02,
			"neutral":   0.05,
			"surprised": 0.02,
			"disgusted": 0.01,
			"fearful":   0.00,
		},
		{
			"happy":     0.10,
			"sad":       0.15,
			"angry":     0.60,
			"neutral":   0.10,
			"surprised": 0.03,
			"disgusted": 0.02,
			"fearful":   0.00,
		},
	}

	for i, face := range faceResult.Faces {
		if i >= len(emotionScores) {
			break
		}

		scores := emotionScores[i]

		// Find primary emotion
		primaryEmotion := ""
		maxScore := float32(0.0)
		for emotion, score := range scores {
			if score > maxScore && score >= m.config.ConfidenceThreshold {
				maxScore = score
				primaryEmotion = emotion
			}
		}

		if primaryEmotion != "" {
			emotions = append(emotions, EmotionResult{
				Emotion:   primaryEmotion,
				Scores:    scores,
				FaceID:    face.TrackID,
				Timestamp: time.Now(),
			})
		}
	}

	return emotions, nil
}

// Config returns the detector configuration
func (m *MockEmotionDetector) Config() EmotionDetectionConfig {
	return m.config
}

// Close closes the detector
func (m *MockEmotionDetector) Close() error {
	return nil
}
