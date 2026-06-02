package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// PoseEstimator defines the interface for pose estimation models
type PoseEstimator interface {
	// Estimate performs pose estimation on a video frame
	Estimate(frame *VideoFrame) (*PoseEstimationResult, error)

	// Config returns the estimator configuration
	Config() PoseEstimationConfig

	// Close closes the estimator
	Close() error
}

// PoseEstimationConfig represents pose estimation configuration
type PoseEstimationConfig struct {
	// Model name (e.g., "openpose", "mediapipe", "alphapose")
	ModelName string

	// Confidence threshold (0.0 to 1.0)
	ConfidenceThreshold float32

	// Number of keypoints to detect
	NumKeypoints int

	// Whether to use GPU acceleration
	UseGPU bool
}

// Keypoint represents a detected keypoint
type Keypoint struct {
	// Keypoint name (e.g., "nose", "left_shoulder", "right_knee")
	Name string

	// X coordinate
	X float32

	// Y coordinate
	Y float32

	// Confidence score
	Confidence float32
}

// Skeleton represents a detected skeleton/pose
type Skeleton struct {
	// Keypoints
	Keypoints []Keypoint

	// Confidence score
	Confidence float32

	// Person ID for tracking
	PersonID int32
}

// PoseEstimationResult represents pose estimation results
type PoseEstimationResult struct {
	// Detected skeletons
	Skeletons []Skeleton

	// Frame metadata
	FrameNumber int64
	Timestamp   int64

	// Processing time in milliseconds
	ProcessingTime int32
}

// PoseEstimationComponent estimates human poses in video frames
type PoseEstimationComponent struct {
	BaseComponent
	estimator PoseEstimator
	personID  int32
	mu        sync.Mutex
}

// NewPoseEstimationComponent creates a new PoseEstimationComponent
func NewPoseEstimationComponent(id string, estimator PoseEstimator) *PoseEstimationComponent {
	return &PoseEstimationComponent{
		BaseComponent: NewBaseComponent(id, "PoseEstimation",
			[]string{"video_in"},
			[]string{"pose_out"}),
		estimator: estimator,
		personID:  0,
	}
}

// Process implements the Component interface
func (p *PoseEstimationComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["video_in"]
	out := outputs["pose_out"]

	for {
		select {
		case <-ctx.Done():
			return nil

		case pkt, ok := <-in:
			if !ok {
				return nil
			}

			if pkt.Type == PacketTypeVideo {
				frame, ok := pkt.Data.(*VideoFrame)
				if !ok {
					continue
				}

				// Perform pose estimation
				result, err := p.estimator.Estimate(frame)
				if err != nil {
					// Log error but continue processing
					continue
				}

				// Assign person IDs
				p.mu.Lock()
				for i := range result.Skeletons {
					result.Skeletons[i].PersonID = p.personID
					p.personID++
				}
				p.mu.Unlock()

				// Send pose estimation results downstream
				select {
				case out <- NewPacket(PacketTypeGeneric, result):
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

// GetEstimator returns the underlying pose estimator
func (p *PoseEstimationComponent) GetEstimator() PoseEstimator {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.estimator
}

// MockPoseEstimator is a mock implementation for testing
type MockPoseEstimator struct {
	config PoseEstimationConfig
}

// NewMockPoseEstimator creates a new mock pose estimator
func NewMockPoseEstimator(config PoseEstimationConfig) *MockPoseEstimator {
	return &MockPoseEstimator{
		config: config,
	}
}

// Estimate performs mock pose estimation
func (m *MockPoseEstimator) Estimate(frame *VideoFrame) (*PoseEstimationResult, error) {
	if frame == nil {
		return nil, fmt.Errorf("frame is nil")
	}

	startTime := time.Now()

	// Generate mock skeleton data (COCO 17-point format)
	keypoints := []Keypoint{
		{Name: "nose", X: 320, Y: 240, Confidence: 0.98},
		{Name: "left_eye", X: 300, Y: 220, Confidence: 0.97},
		{Name: "right_eye", X: 340, Y: 220, Confidence: 0.97},
		{Name: "left_ear", X: 280, Y: 210, Confidence: 0.96},
		{Name: "right_ear", X: 360, Y: 210, Confidence: 0.96},
		{Name: "left_shoulder", X: 260, Y: 320, Confidence: 0.95},
		{Name: "right_shoulder", X: 380, Y: 320, Confidence: 0.95},
		{Name: "left_elbow", X: 240, Y: 400, Confidence: 0.92},
		{Name: "right_elbow", X: 400, Y: 400, Confidence: 0.92},
		{Name: "left_wrist", X: 230, Y: 480, Confidence: 0.90},
		{Name: "right_wrist", X: 410, Y: 480, Confidence: 0.90},
		{Name: "left_hip", X: 280, Y: 500, Confidence: 0.94},
		{Name: "right_hip", X: 360, Y: 500, Confidence: 0.94},
		{Name: "left_knee", X: 270, Y: 600, Confidence: 0.93},
		{Name: "right_knee", X: 370, Y: 600, Confidence: 0.93},
		{Name: "left_ankle", X: 260, Y: 680, Confidence: 0.91},
		{Name: "right_ankle", X: 380, Y: 680, Confidence: 0.91},
	}

	// Filter by confidence threshold
	filtered := []Keypoint{}
	for _, kp := range keypoints {
		if kp.Confidence >= m.config.ConfidenceThreshold {
			filtered = append(filtered, kp)
		}
	}

	skeleton := Skeleton{
		Keypoints:  filtered,
		Confidence: 0.94,
	}

	processingTime := int32(time.Since(startTime).Milliseconds())

	return &PoseEstimationResult{
		Skeletons:      []Skeleton{skeleton},
		FrameNumber:    frame.FrameNumber,
		Timestamp:      frame.Timestamp,
		ProcessingTime: processingTime,
	}, nil
}

// Config returns the estimator configuration
func (m *MockPoseEstimator) Config() PoseEstimationConfig {
	return m.config
}

// Close closes the estimator
func (m *MockPoseEstimator) Close() error {
	return nil
}
