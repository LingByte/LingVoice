package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// NodeRole represents the role of a node in distributed processing
type NodeRole string

const (
	NodeRoleCoordinator NodeRole = "coordinator"
	NodeRoleWorker      NodeRole = "worker"
	NodeRoleGateway     NodeRole = "gateway"
)

// DistributedNode represents a node in distributed graph execution
type DistributedNode struct {
	// Node ID
	ID string

	// Node role
	Role NodeRole

	// Node address (host:port)
	Address string

	// Node status
	Status string

	// Last heartbeat time
	LastHeartbeat time.Time

	// Node capacity (0-100)
	Capacity int

	// Current load (0-100)
	CurrentLoad int
}

// DistributedGraphConfig represents configuration for distributed graph execution
type DistributedGraphConfig struct {
	// Enable distributed execution
	Enabled bool

	// Coordinator address
	CoordinatorAddress string

	// Worker nodes
	WorkerNodes []DistributedNode

	// Replication factor
	ReplicationFactor int

	// Timeout for node communication
	NodeTimeout time.Duration

	// Heartbeat interval
	HeartbeatInterval time.Duration

	// Load balancing strategy (round-robin, least-loaded, etc.)
	LoadBalancingStrategy string
}

// DistributedExecutor manages distributed graph execution
type DistributedExecutor struct {
	config        DistributedGraphConfig
	nodes         map[string]*DistributedNode
	nodesMu       sync.RWMutex
	taskQueue     chan *DistributedTask
	resultChannel chan *TaskResult
	stopChan      chan struct{}
}

// DistributedTask represents a task to be executed on a remote node
type DistributedTask struct {
	// Task ID
	ID string

	// Component ID
	ComponentID string

	// Packet data
	Packet *Packet

	// Target node ID
	TargetNodeID string

	// Retry count
	RetryCount int

	// Max retries
	MaxRetries int

	// Created time
	CreatedTime time.Time

	// Deadline
	Deadline time.Time
}

// TaskResult represents the result of a distributed task
type TaskResult struct {
	// Task ID
	TaskID string

	// Success flag
	Success bool

	// Result packet
	Result *Packet

	// Error message
	Error string

	// Execution time in milliseconds
	ExecutionTime int64

	// Node ID that executed the task
	ExecutedOnNode string
}

// NewDistributedExecutor creates a new distributed executor
func NewDistributedExecutor(config DistributedGraphConfig) *DistributedExecutor {
	return &DistributedExecutor{
		config:        config,
		nodes:         make(map[string]*DistributedNode),
		taskQueue:     make(chan *DistributedTask, 1000),
		resultChannel: make(chan *TaskResult, 1000),
		stopChan:      make(chan struct{}),
	}
}

// RegisterNode registers a worker node
func (de *DistributedExecutor) RegisterNode(node DistributedNode) error {
	de.nodesMu.Lock()
	defer de.nodesMu.Unlock()

	if _, exists := de.nodes[node.ID]; exists {
		return fmt.Errorf("node %s already registered", node.ID)
	}

	node.LastHeartbeat = time.Now()
	node.Status = "healthy"
	de.nodes[node.ID] = &node

	return nil
}

// UnregisterNode unregisters a worker node
func (de *DistributedExecutor) UnregisterNode(nodeID string) error {
	de.nodesMu.Lock()
	defer de.nodesMu.Unlock()

	if _, exists := de.nodes[nodeID]; !exists {
		return fmt.Errorf("node %s not found", nodeID)
	}

	delete(de.nodes, nodeID)
	return nil
}

// GetHealthyNodes returns all healthy nodes
func (de *DistributedExecutor) GetHealthyNodes() []*DistributedNode {
	de.nodesMu.RLock()
	defer de.nodesMu.RUnlock()

	var healthy []*DistributedNode
	now := time.Now()

	for _, node := range de.nodes {
		// Check if node is still alive (heartbeat within timeout)
		if now.Sub(node.LastHeartbeat) < de.config.NodeTimeout {
			healthy = append(healthy, node)
		}
	}

	return healthy
}

// SubmitTask submits a task for distributed execution
func (de *DistributedExecutor) SubmitTask(task *DistributedTask) error {
	select {
	case de.taskQueue <- task:
		return nil
	case <-de.stopChan:
		return fmt.Errorf("executor is stopped")
	default:
		return fmt.Errorf("task queue is full")
	}
}

// GetResult retrieves a task result
func (de *DistributedExecutor) GetResult(ctx context.Context) (*TaskResult, error) {
	select {
	case result := <-de.resultChannel:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-de.stopChan:
		return nil, fmt.Errorf("executor is stopped")
	}
}

// SelectNode selects a target node for task execution
func (de *DistributedExecutor) SelectNode(componentID string) (*DistributedNode, error) {
	healthyNodes := de.GetHealthyNodes()
	if len(healthyNodes) == 0 {
		return nil, fmt.Errorf("no healthy nodes available")
	}

	// Load balancing strategy
	switch de.config.LoadBalancingStrategy {
	case "least-loaded":
		return de.selectLeastLoaded(healthyNodes), nil
	case "round-robin":
		return de.selectRoundRobin(healthyNodes), nil
	default:
		return healthyNodes[0], nil
	}
}

// selectLeastLoaded selects the node with least load
func (de *DistributedExecutor) selectLeastLoaded(nodes []*DistributedNode) *DistributedNode {
	if len(nodes) == 0 {
		return nil
	}

	selected := nodes[0]
	for _, node := range nodes[1:] {
		if node.CurrentLoad < selected.CurrentLoad {
			selected = node
		}
	}
	return selected
}

// selectRoundRobin selects node using round-robin
func (de *DistributedExecutor) selectRoundRobin(nodes []*DistributedNode) *DistributedNode {
	if len(nodes) == 0 {
		return nil
	}
	return nodes[0]
}

// Start starts the distributed executor
func (de *DistributedExecutor) Start(ctx context.Context) error {
	go de.processHeartbeats(ctx)
	go de.processTasks(ctx)
	return nil
}

// Stop stops the distributed executor
func (de *DistributedExecutor) Stop() error {
	close(de.stopChan)
	return nil
}

// processHeartbeats processes node heartbeats
func (de *DistributedExecutor) processHeartbeats(ctx context.Context) {
	ticker := time.NewTicker(de.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-de.stopChan:
			return
		case <-ticker.C:
			de.checkNodeHealth()
		}
	}
}

// checkNodeHealth checks health of all nodes
func (de *DistributedExecutor) checkNodeHealth() {
	de.nodesMu.Lock()
	defer de.nodesMu.Unlock()

	now := time.Now()
	for _, node := range de.nodes {
		if now.Sub(node.LastHeartbeat) > de.config.NodeTimeout {
			node.Status = "unhealthy"
		} else {
			node.Status = "healthy"
		}
	}
}

// processTasks processes submitted tasks
func (de *DistributedExecutor) processTasks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-de.stopChan:
			return
		case task := <-de.taskQueue:
			de.executeTask(ctx, task)
		}
	}
}

// executeTask executes a task on a remote node
func (de *DistributedExecutor) executeTask(ctx context.Context, task *DistributedTask) {
	startTime := time.Now()

	// Select target node if not specified
	if task.TargetNodeID == "" {
		node, err := de.SelectNode(task.ComponentID)
		if err != nil {
			de.resultChannel <- &TaskResult{
				TaskID:        task.ID,
				Success:       false,
				Error:         err.Error(),
				ExecutionTime: time.Since(startTime).Milliseconds(),
			}
			return
		}
		task.TargetNodeID = node.ID
	}

	// Simulate task execution
	result := &TaskResult{
		TaskID:         task.ID,
		Success:        true,
		Result:         task.Packet,
		ExecutionTime:  time.Since(startTime).Milliseconds(),
		ExecutedOnNode: task.TargetNodeID,
	}

	select {
	case de.resultChannel <- result:
	case <-ctx.Done():
	case <-de.stopChan:
	}
}

// LoadBalancer manages load balancing across nodes
type LoadBalancer struct {
	executor *DistributedExecutor
	mu       sync.RWMutex
}

// NewLoadBalancer creates a new load balancer
func NewLoadBalancer(executor *DistributedExecutor) *LoadBalancer {
	return &LoadBalancer{
		executor: executor,
	}
}

// GetOptimalNode returns the optimal node for a task
func (lb *LoadBalancer) GetOptimalNode(componentID string) (*DistributedNode, error) {
	return lb.executor.SelectNode(componentID)
}

// RebalanceLoad rebalances load across nodes
func (lb *LoadBalancer) RebalanceLoad() error {
	nodes := lb.executor.GetHealthyNodes()
	if len(nodes) == 0 {
		return fmt.Errorf("no healthy nodes available")
	}

	// Calculate average load
	totalLoad := 0
	for _, node := range nodes {
		totalLoad += node.CurrentLoad
	}
	avgLoad := totalLoad / len(nodes)

	// Rebalance by redistributing tasks
	for _, node := range nodes {
		if node.CurrentLoad > avgLoad+10 {
			// This node is overloaded, redistribute tasks
			node.CurrentLoad = avgLoad
		}
	}

	return nil
}
