package media

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"fmt"
	"sync/atomic"
)

// RoutingStrategy defines how packets are routed
type RoutingStrategy int

const (
	// StrategyBroadcast sends to all outputs
	StrategyBroadcast RoutingStrategy = iota
	// StrategyRoundRobin distributes across outputs
	StrategyRoundRobin
	// StrategyFirstAvailable uses first available output
	StrategyFirstAvailable
)

// RouteRule defines routing rules
type RouteRule struct {
	Condition func(packet MediaPacket) bool
	Targets   []string // Transport IDs
	Strategy  RoutingStrategy
}

// Router manages packet routing. The round-robin index uses atomic operations
// to avoid lock contention on the hot path (Route is called per-packet).
type Router struct {
	rules           []RouteRule
	defaultStrategy RoutingStrategy
	mu              atomic.Pointer[[]RouteRule]
	roundRobinIndex atomic.Int64
}

// NewRouter creates a new router
func NewRouter(defaultStrategy RoutingStrategy) *Router {
	rules := make([]RouteRule, 0)
	r := &Router{
		defaultStrategy: defaultStrategy,
	}
	r.mu.Store(&rules)
	return r
}

// AddRule adds a routing rule
func (r *Router) AddRule(rule RouteRule) {
	// Copy-on-write: snapshot current rules, append, atomically swap.
	// This avoids blocking Route() readers during rule addition.
	old := r.mu.Load()
	newRules := make([]RouteRule, len(*old), len(*old)+1)
	copy(newRules, *old)
	newRules = append(newRules, rule)
	r.mu.Store(&newRules)
}

// Route determines where to send a packet. Lock-free read path.
func (r *Router) Route(packet MediaPacket, availableTransports []*TransportConnector) []*TransportConnector {
	rules := r.mu.Load()

	// Check rules in order
	for _, rule := range *rules {
		if rule.Condition != nil && rule.Condition(packet) {
			return r.applyStrategy(rule.Strategy, rule.Targets, availableTransports)
		}
	}

	// Use default strategy
	return r.applyStrategy(r.defaultStrategy, nil, availableTransports)
}

// applyStrategy applies routing strategy. Round-robin uses atomic increment.
func (r *Router) applyStrategy(strategy RoutingStrategy, targets []string, available []*TransportConnector) []*TransportConnector {
	if len(available) == 0 {
		return nil
	}

	switch strategy {
	case StrategyBroadcast:
		return available

	case StrategyRoundRobin:
		idx := r.roundRobinIndex.Add(1)
		return []*TransportConnector{available[int(idx)%len(available)]}

	case StrategyFirstAvailable:
		return []*TransportConnector{available[0]}

	default:
		return available
	}
}

// TransportConnector represents a connection to a transport.
// Active uses atomic.Bool for lock-free reads on the hot path.
type TransportConnector struct {
	ID        string
	Transport MediaTransport
	Direction string // "input" or "output"
	active    atomic.Bool
}

// NewTransportConnector creates a new transport connector
func NewTransportConnector(id string, transport MediaTransport, direction string) *TransportConnector {
	tc := &TransportConnector{
		ID:        id,
		Transport: transport,
		Direction: direction,
	}
	tc.active.Store(true)
	return tc
}

// String returns string representation
func (tc *TransportConnector) String() string {
	return fmt.Sprintf("TransportConnector{ID: %s, Direction: %s, Active: %v}", tc.ID, tc.Direction, tc.active.Load())
}

// SetActive sets the active state
func (tc *TransportConnector) SetActive(active bool) {
	tc.active.Store(active)
}

// IsActive checks if connector is active (lock-free)
func (tc *TransportConnector) IsActive() bool {
	return tc.active.Load()
}
