package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// VideoRenderTarget defines the interface for video rendering targets
type VideoRenderTarget interface {
	// Render renders a video frame
	Render(frame *VideoFrame) error

	// Config returns the video configuration
	Config() VideoConfig

	// Close closes the render target
	Close() error
}

// VideoRenderComponent consumes video frames and renders them to a target
type VideoRenderComponent struct {
	BaseComponent
	target VideoRenderTarget
	mu     sync.Mutex
}

// NewVideoRenderComponent creates a new VideoRenderComponent
func NewVideoRenderComponent(id string, target VideoRenderTarget) *VideoRenderComponent {
	return &VideoRenderComponent{
		BaseComponent: NewBaseComponent(id, "VideoRender", []string{"video_in"}, nil),
		target:        target,
	}
}

// Process implements the Component interface
func (v *VideoRenderComponent) Process(
	ctx context.Context,
	inputs map[string]<-chan *Packet,
	outputs map[string]chan<- *Packet,
) error {
	in := inputs["video_in"]

	frameCount := int64(0)

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

				// Render the frame
				if err := v.target.Render(frame); err != nil {
					// Log error but continue rendering
					continue
				}

				frameCount++
			}
		}
	}
}

// GetTarget returns the underlying video render target
func (v *VideoRenderComponent) GetTarget() VideoRenderTarget {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.target
}

// MockVideoRenderTarget is a mock implementation for testing
type MockVideoRenderTarget struct {
	config      VideoConfig
	frameCount  int64
	mu          sync.Mutex
}

// NewMockVideoRenderTarget creates a new mock video render target
func NewMockVideoRenderTarget(config VideoConfig) *MockVideoRenderTarget {
	return &MockVideoRenderTarget{
		config: config,
	}
}

// Render renders a frame (mock implementation)
func (m *MockVideoRenderTarget) Render(frame *VideoFrame) error {
	if frame == nil {
		return errors.New("frame is nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate frame
	if frame.Width != m.config.Width || frame.Height != m.config.Height {
		return fmt.Errorf("frame size mismatch: expected %dx%d, got %dx%d",
			m.config.Width, m.config.Height, frame.Width, frame.Height)
	}

	m.frameCount++

	// Mock rendering - just count frames
	return nil
}

// Config returns the video configuration
func (m *MockVideoRenderTarget) Config() VideoConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config
}

// Close closes the render target
func (m *MockVideoRenderTarget) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.frameCount = 0
	return nil
}

// GetFrameCount returns the number of frames rendered
func (m *MockVideoRenderTarget) GetFrameCount() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.frameCount
}
