package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// VideoCaptureSource defines the interface for video capture sources
type VideoCaptureSource interface {
	// Start begins capturing video
	Start(ctx context.Context) error

	// Next returns the next video frame
	Next(ctx context.Context) (*VideoFrame, error)

	// Stop stops capturing video
	Stop() error

	// Config returns the video configuration
	Config() VideoConfig

	// Close closes the capture source
	Close() error
}

// VideoCaptureComponent reads video frames from a capture source and streams them
type VideoCaptureComponent struct {
	BaseComponent
	source VideoCaptureSource
	mu     sync.Mutex
}

// NewVideoCaptureComponent creates a new VideoCaptureComponent
func NewVideoCaptureComponent(id string, source VideoCaptureSource) *VideoCaptureComponent {
	return &VideoCaptureComponent{
		BaseComponent: NewBaseComponent(id, "VideoCapture", nil, []string{"video_out"}),
		source:        source,
	}
}

// Process implements the Component interface
func (v *VideoCaptureComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	out := outputs["video_out"]

	// Start the capture source
	if err := v.source.Start(ctx); err != nil {
		return fmt.Errorf("failed to start video capture: %w", err)
	}
	defer v.source.Stop()

	frameCount := int64(0)

	for {
		select {
		case <-ctx.Done():
			return nil

		default:
			frame, err := v.source.Next(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				// Log error but continue capturing
				continue
			}

			if frame == nil {
				continue
			}

			frameCount++
			frame.FrameNumber = frameCount

			// Send video packet downstream
			select {
			case out <- NewPacket(PacketTypeVideo, frame):
			case <-ctx.Done():
				return nil
			}
		}
	}
}

// GetSource returns the underlying video capture source
func (v *VideoCaptureComponent) GetSource() VideoCaptureSource {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.source
}

// MockVideoCaptureSource is a mock implementation for testing
type MockVideoCaptureSource struct {
	config     VideoConfig
	frameCount int64
	running    bool
	mu         sync.Mutex
}

// NewMockVideoCaptureSource creates a new mock video capture source
func NewMockVideoCaptureSource(config VideoConfig) *MockVideoCaptureSource {
	return &MockVideoCaptureSource{
		config: config,
	}
}

// Start begins capturing (mock implementation)
func (m *MockVideoCaptureSource) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return errors.New("capture already running")
	}

	m.running = true
	m.frameCount = 0
	return nil
}

// Next returns the next frame (mock implementation)
func (m *MockVideoCaptureSource) Next(ctx context.Context) (*VideoFrame, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil, errors.New("capture not running")
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Simulate frame capture delay
	time.Sleep(time.Duration(1000/m.config.FrameRate) * time.Millisecond)

	m.frameCount++

	// Create mock frame data
	frameSize := m.config.Width * m.config.Height * 3 / 2 // YUV420P
	frameData := make([]byte, frameSize)

	// Fill with mock data
	for i := range frameData {
		frameData[i] = byte((m.frameCount + int64(i)) % 256)
	}

	return &VideoFrame{
		Data:        frameData,
		Width:       m.config.Width,
		Height:      m.config.Height,
		PixelFormat: string(m.config.PixelFormat),
		Codec:       string(m.config.Codec),
		Timestamp:   time.Now().UnixMilli(),
		FrameNumber: m.frameCount,
		IsKeyFrame:  m.frameCount%30 == 0, // Keyframe every 30 frames
		Duration:    int32(1000 / m.config.FrameRate),
	}, nil
}

// Stop stops capturing (mock implementation)
func (m *MockVideoCaptureSource) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.running = false
	return nil
}

// Config returns the video configuration
func (m *MockVideoCaptureSource) Config() VideoConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config
}

// Close closes the capture source
func (m *MockVideoCaptureSource) Close() error {
	return m.Stop()
}
