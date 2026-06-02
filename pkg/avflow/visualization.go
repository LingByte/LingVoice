package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"fmt"
	"strings"
)

// GraphVisualizer visualizes graph structure
type GraphVisualizer struct {
	graph *Graph
}

// NewGraphVisualizer creates a new graph visualizer
func NewGraphVisualizer(graph *Graph) *GraphVisualizer {
	return &GraphVisualizer{
		graph: graph,
	}
}

// ToASCII returns ASCII representation of the graph
func (gv *GraphVisualizer) ToASCII() string {
	var sb strings.Builder

	sb.WriteString("=== Graph: " + gv.graph.name + " ===\n\n")

	// Components section
	sb.WriteString("Components:\n")
	for id, comp := range gv.graph.components {
		sb.WriteString(fmt.Sprintf("  [%s] %s\n", id, comp.Type()))
	}

	sb.WriteString("\nConnections:\n")
	for _, edge := range gv.graph.edges {
		sb.WriteString(fmt.Sprintf("  %s.%s -> %s.%s\n",
			edge.FromNode, edge.FromPort,
			edge.ToNode, edge.ToPort))
	}

	return sb.String()
}

// ToDOT returns DOT format representation (for Graphviz)
func (gv *GraphVisualizer) ToDOT() string {
	var sb strings.Builder

	sb.WriteString("digraph " + gv.graph.name + " {\n")
	sb.WriteString("  rankdir=LR;\n")
	sb.WriteString("  node [shape=box, style=rounded];\n\n")

	// Components
	for id, comp := range gv.graph.components {
		label := fmt.Sprintf("%s\\n(%s)", id, comp.Type())
		sb.WriteString(fmt.Sprintf("  \"%s\" [label=\"%s\"];\n", id, label))
	}

	sb.WriteString("\n")

	// Connections
	for _, edge := range gv.graph.edges {
		sb.WriteString(fmt.Sprintf("  \"%s\" -> \"%s\";\n",
			edge.FromNode, edge.ToNode))
	}

	sb.WriteString("}\n")

	return sb.String()
}

// ToJSON returns JSON representation of the graph
func (gv *GraphVisualizer) ToJSON() string {
	var sb strings.Builder

	sb.WriteString("{\n")
	sb.WriteString(fmt.Sprintf("  \"name\": \"%s\",\n", gv.graph.name))
	sb.WriteString("  \"components\": [\n")

	components := make([]string, 0)
	for id, comp := range gv.graph.components {
		components = append(components, fmt.Sprintf("    {\"id\": \"%s\", \"type\": \"%s\"}", id, comp.Type()))
	}
	sb.WriteString(strings.Join(components, ",\n"))

	sb.WriteString("\n  ],\n")
	sb.WriteString("  \"connections\": [\n")

	connections := make([]string, 0)
	for _, edge := range gv.graph.edges {
		conn := fmt.Sprintf("    {\"from\": \"%s.%s\", \"to\": \"%s.%s\"}",
			edge.FromNode, edge.FromPort,
			edge.ToNode, edge.ToPort)
		connections = append(connections, conn)
	}
	sb.WriteString(strings.Join(connections, ",\n"))

	sb.WriteString("\n  ]\n")
	sb.WriteString("}\n")

	return sb.String()
}

// GraphDebugger provides debugging capabilities for graphs
type GraphDebugger struct {
	graph *Graph
}

// NewGraphDebugger creates a new graph debugger
func NewGraphDebugger(graph *Graph) *GraphDebugger {
	return &GraphDebugger{
		graph: graph,
	}
}

// ValidateGraph validates graph structure
func (gd *GraphDebugger) ValidateGraph() []string {
	var errors []string

	// Check for isolated components
	for id := range gd.graph.components {
		hasConnection := false
		for _, edge := range gd.graph.edges {
			if edge.FromNode == id || edge.ToNode == id {
				hasConnection = true
				break
			}
		}
		if !hasConnection {
			errors = append(errors, fmt.Sprintf("Component %s is isolated (no connections)", id))
		}
	}

	// Check for invalid connections
	for _, edge := range gd.graph.edges {
		if _, exists := gd.graph.components[edge.FromNode]; !exists {
			errors = append(errors, fmt.Sprintf("Source component %s not found", edge.FromNode))
		}
		if _, exists := gd.graph.components[edge.ToNode]; !exists {
			errors = append(errors, fmt.Sprintf("Target component %s not found", edge.ToNode))
		}
	}

	return errors
}

// GetComponentInfo returns detailed information about a component
func (gd *GraphDebugger) GetComponentInfo(componentID string) map[string]interface{} {
	comp, exists := gd.graph.components[componentID]
	if !exists {
		return nil
	}

	info := map[string]interface{}{
		"id":      componentID,
		"type":    comp.Type(),
		"inputs":  comp.Inputs(),
		"outputs": comp.Outputs(),
	}

	// Find connections
	inbound := make([]string, 0)
	outbound := make([]string, 0)

	for _, edge := range gd.graph.edges {
		if edge.FromNode == componentID {
			outbound = append(outbound, edge.ToNode)
		}
		if edge.ToNode == componentID {
			inbound = append(inbound, edge.FromNode)
		}
	}

	info["inbound_connections"] = inbound
	info["outbound_connections"] = outbound

	return info
}

// GetGraphStats returns statistics about the graph
func (gd *GraphDebugger) GetGraphStats() map[string]interface{} {
	stats := map[string]interface{}{
		"name":             gd.graph.name,
		"component_count":  len(gd.graph.components),
		"connection_count": len(gd.graph.edges),
		"buffer_size":      gd.graph.bufferSize,
	}

	// Component type distribution
	typeDistribution := make(map[string]int)
	for _, comp := range gd.graph.components {
		typeDistribution[comp.Type()]++
	}
	stats["component_types"] = typeDistribution

	return stats
}

// TracePacket traces a packet through the graph
type PacketTracer struct {
	graph *Graph
	trace []TraceEvent
}

// TraceEvent represents an event in packet tracing
type TraceEvent struct {
	Timestamp   int64
	ComponentID string
	EventType   string
	Message     string
}

// NewPacketTracer creates a new packet tracer
func NewPacketTracer(graph *Graph) *PacketTracer {
	return &PacketTracer{
		graph: graph,
		trace: make([]TraceEvent, 0),
	}
}

// RecordEvent records a trace event
func (pt *PacketTracer) RecordEvent(componentID, eventType, message string) {
	event := TraceEvent{
		Timestamp:   int64(len(pt.trace)),
		ComponentID: componentID,
		EventType:   eventType,
		Message:     message,
	}
	pt.trace = append(pt.trace, event)
}

// GetTrace returns the trace
func (pt *PacketTracer) GetTrace() []TraceEvent {
	return pt.trace
}

// PrintTrace prints the trace in readable format
func (pt *PacketTracer) PrintTrace() string {
	var sb strings.Builder

	sb.WriteString("=== Packet Trace ===\n\n")
	for _, event := range pt.trace {
		sb.WriteString(fmt.Sprintf("[%d] %s: %s - %s\n",
			event.Timestamp, event.ComponentID, event.EventType, event.Message))
	}

	return sb.String()
}
