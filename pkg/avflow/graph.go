package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// Edge represents a directed data connection from an output port to an input port.
type Edge struct {
	FromNode string
	FromPort string
	ToNode   string
	ToPort   string
}

// Graph compiles and runs a set of components connected by directed edges.
type Graph struct {
	name       string
	components map[string]Component
	edges      []Edge
	bufferSize int
	logger     *zap.Logger
}

// NewGraph creates a new orchestration graph.
func NewGraph(name string) *Graph {
	l, _ := zap.NewDevelopment()
	return &Graph{
		name:       name,
		components: make(map[string]Component),
		edges:      make([]Edge, 0),
		bufferSize: 64, // Sufficient buffering for real-time frame packets (e.g., 20ms chunks)
		logger:     l,
	}
}

// WithBufferSize configures the channel buffer size for connections.
func (g *Graph) WithBufferSize(size int) *Graph {
	if size > 0 {
		g.bufferSize = size
	}
	return g
}

// AddComponent registers a component in the graph.
func (g *Graph) AddComponent(c Component) *Graph {
	if g == nil || c == nil {
		return g
	}
	g.components[c.ID()] = c
	return g
}

// Connect links an output port of a component to an input port of another component.
func (g *Graph) Connect(fromNode, fromPort, toNode, toPort string) *Graph {
	if g == nil {
		return g
	}
	g.edges = append(g.edges, Edge{
		FromNode: fromNode,
		FromPort: fromPort,
		ToNode:   toNode,
		ToPort:   toPort,
	})
	return g
}

// Run compiles the graph and executes all components concurrently.
// It blocks until all components finish or the context is cancelled.
func (g *Graph) Run(ctx context.Context) error {
	if g == nil {
		return fmt.Errorf("avflow: nil graph")
	}

	g.logger.Info("compiling graph...", zap.String("graph", g.name), zap.Int("nodes", len(g.components)), zap.Int("edges", len(g.edges)))

	// 1. Verify that all referenced nodes exist.
	for _, edge := range g.edges {
		if _, ok := g.components[edge.FromNode]; !ok {
			return fmt.Errorf("avflow compile error: source node %q not found", edge.FromNode)
		}
		if _, ok := g.components[edge.ToNode]; !ok {
			return fmt.Errorf("avflow compile error: target node %q not found", edge.ToNode)
		}
	}

	// 2. Allocate input/output channel maps for each component.
	// We use buffered channels to allow smooth streaming and backpressure.
	nodeInputs := make(map[string]map[string]chan *Packet)
	nodeOutputs := make(map[string]map[string]chan *Packet)

	for nodeID, c := range g.components {
		nodeInputs[nodeID] = make(map[string]chan *Packet)
		nodeOutputs[nodeID] = make(map[string]chan *Packet)

		for _, port := range c.Inputs() {
			nodeInputs[nodeID][port] = make(chan *Packet, g.bufferSize)
		}
		for _, port := range c.Outputs() {
			nodeOutputs[nodeID][port] = make(chan *Packet, g.bufferSize)
		}
	}

	// 3. Map output channels to their connected input channels (1-to-N broadcasting support).
	connections := make(map[string]map[string][]chan *Packet) // Map [nodeID][portName] -> []input_channels
	for _, edge := range g.edges {
		if connections[edge.FromNode] == nil {
			connections[edge.FromNode] = make(map[string][]chan *Packet)
		}
		targetChan := nodeInputs[edge.ToNode][edge.ToPort]
		if targetChan == nil {
			g.logger.Warn("edge connected to unregistered input port", zap.String("node", edge.ToNode), zap.String("port", edge.ToPort))
			continue
		}
		connections[edge.FromNode][edge.FromPort] = append(connections[edge.FromNode][edge.FromPort], targetChan)
	}

	// Create a sub-context for coordinating shutdown of all goroutines on error
	gCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup

	// 4. Start Routing Goroutines.
	// For each output port of every component, we read packages and dispatch them to connected inputs.
	for nodeID, c := range g.components {
		for _, port := range c.Outputs() {
			outChan := nodeOutputs[nodeID][port]
			destChans := connections[nodeID][port]

			wg.Add(1)
			go func(nID, pName string, src <-chan *Packet, dests []chan *Packet) {
				defer wg.Done()
				defer func() {
					// Close all destination input channels once the source output channel completes
					for _, dest := range dests {
						close(dest)
					}
				}()

				for {
					select {
					case <-gCtx.Done():
						return
					case pkt, ok := <-src:
						if !ok {
							return
						}
						// If there are no connections, the packet is discarded (drains output)
						for _, dest := range dests {
							select {
							case dest <- pkt:
							case <-gCtx.Done():
								return
							}
						}
					}
				}
			}(nodeID, port, outChan, destChans)
		}
	}

	// 5. Build read-only inputs map and write-only outputs map for each component.
	componentInputs := make(map[string]map[string]<-chan *Packet)
	componentOutputs := make(map[string]map[string]chan<- *Packet)

	for nodeID, c := range g.components {
		componentInputs[nodeID] = make(map[string]<-chan *Packet)
		componentOutputs[nodeID] = make(map[string]chan<- *Packet)

		for _, port := range c.Inputs() {
			componentInputs[nodeID][port] = nodeInputs[nodeID][port]
		}
		for _, port := range c.Outputs() {
			componentOutputs[nodeID][port] = nodeOutputs[nodeID][port]
		}
	}

	// 6. Start Component Workers.
	// Each component's Process method runs in its own concurrent goroutine.
	errChan := make(chan error, len(g.components))
	var workerWg sync.WaitGroup

	for nodeID, c := range g.components {
		workerWg.Add(1)
		go func(nID string, comp Component, inputs map[string]<-chan *Packet, outputs map[string]chan<- *Packet) {
			defer workerWg.Done()
			defer func() {
				// Safely close all output channels of this component when Process exits
				for _, outChan := range outputs {
					close(outChan)
				}
			}()

			g.logger.Debug("component worker starting", zap.String("id", nID), zap.String("type", comp.Type()))
			err := comp.Process(gCtx, inputs, outputs)
			if err != nil && err != context.Canceled {
				g.logger.Error("component execution failed", zap.String("id", nID), zap.Error(err))
				errChan <- fmt.Errorf("component %q [%s] failed: %w", nID, comp.Type(), err)
				cancel() // Terminate other workers on error
			} else {
				g.logger.Debug("component worker completed gracefully", zap.String("id", nID))
			}
		}(nodeID, c, componentInputs[nodeID], componentOutputs[nodeID])
	}

	// Wait for all component workers to exit
	workerWg.Wait()

	// Wait for routing goroutines to finish as well
	wg.Wait()

	// 7. Check for execution errors
	select {
	case err := <-errChan:
		return err
	default:
		g.logger.Info("graph completed execution successfully", zap.String("graph", g.name))
		return nil
	}
}
