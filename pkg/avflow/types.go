package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"time"
)

// PacketType restricts the category of payloads running on the dataflow graph edges.
type PacketType string

const (
	PacketTypeAudio   PacketType = "audio"   // Carries media.AudioPacket
	PacketTypeVideo   PacketType = "video"   // Carries raw or encoded video frames
	PacketTypeText    PacketType = "text"    // Carries media.TextPacket
	PacketTypeMessage PacketType = "message" // Carries *schema.Message (LLM chat role/content)
	PacketTypeControl PacketType = "control" // Carries session control flags (e.g., barge-in interrupt)
	PacketTypeGeneric PacketType = "generic" // Carries any KV arguments or telemetry data
)

// Packet is the unified data unit flowing between components.
type Packet struct {
	Type      PacketType
	Data      any
	Timestamp time.Time
	Metadata  map[string]any
}

// NewPacket wraps raw data with a type and metadata.
func NewPacket(t PacketType, data any) *Packet {
	return &Packet{
		Type:      t,
		Data:      data,
		Timestamp: time.Now(),
		Metadata:  make(map[string]any),
	}
}

// WithMetadata sets metadata on the packet and returns it.
func (p *Packet) WithMetadata(k string, v any) *Packet {
	if p.Metadata == nil {
		p.Metadata = make(map[string]any)
	}
	p.Metadata[k] = v
	return p
}

// Component defines the lifecycle and execution contract of a node in the AVFlow Graph.
// Any custom processing block, audio device, AI provider, or callback hook must implement this.
type Component interface {
	ID() string                             // Unique node identifier in a graph, e.g. "my-asr-node"
	Type() string                           // Component category, e.g. "ASR", "TTS", "VAD"
	Inputs() []string                       // Names of required or optional input ports
	Outputs() []string                      // Names of produced output ports
	
	// Process executes the component. When started by the graph, it runs inside its own goroutine.
	// It reads from named input channels (defined in Inputs()) and writes to output channels (defined in Outputs()).
	// It must block until the inputs are closed, the context is cancelled, or it encounters a fatal error.
	Process(ctx context.Context, inputs map[string]<-chan *Packet, outputs map[string]chan<- *Packet) error
}
