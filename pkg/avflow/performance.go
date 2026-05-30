package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"sync"
	"time"
)

// PerformanceMetrics tracks component performance metrics
type PerformanceMetrics struct {
	// Component ID
	ComponentID string

	// Total packets processed
	PacketsProcessed int64

	// Total packets dropped
	PacketsDropped int64

	// Average processing time in milliseconds
	AvgProcessingTime float64

	// Min processing time in milliseconds
	MinProcessingTime int64

	// Max processing time in milliseconds
	MaxProcessingTime int64

	// Throughput in packets per second
	Throughput float64

	// Start time
	StartTime time.Time

	// Last update time
	LastUpdateTime time.Time
}

// PerformanceMonitor monitors component performance
type PerformanceMonitor struct {
	metrics map[string]*PerformanceMetrics
	mu      sync.RWMutex
}

// NewPerformanceMonitor creates a new performance monitor
func NewPerformanceMonitor() *PerformanceMonitor {
	return &PerformanceMonitor{
		metrics: make(map[string]*PerformanceMetrics),
	}
}

// RecordPacket records a processed packet
func (pm *PerformanceMonitor) RecordPacket(componentID string, processingTime int64) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, exists := pm.metrics[componentID]; !exists {
		pm.metrics[componentID] = &PerformanceMetrics{
			ComponentID:    componentID,
			StartTime:      time.Now(),
			MinProcessingTime: processingTime,
			MaxProcessingTime: processingTime,
		}
	}

	m := pm.metrics[componentID]
	m.PacketsProcessed++
	m.LastUpdateTime = time.Now()

	// Update min/max
	if processingTime < m.MinProcessingTime {
		m.MinProcessingTime = processingTime
	}
	if processingTime > m.MaxProcessingTime {
		m.MaxProcessingTime = processingTime
	}

	// Update average
	if m.PacketsProcessed > 0 {
		m.AvgProcessingTime = float64(m.AvgProcessingTime*(float64(m.PacketsProcessed)-1)+float64(processingTime)) / float64(m.PacketsProcessed)
	}

	// Update throughput
	elapsed := time.Since(m.StartTime).Seconds()
	if elapsed > 0 {
		m.Throughput = float64(m.PacketsProcessed) / elapsed
	}
}

// RecordDroppedPacket records a dropped packet
func (pm *PerformanceMonitor) RecordDroppedPacket(componentID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, exists := pm.metrics[componentID]; !exists {
		pm.metrics[componentID] = &PerformanceMetrics{
			ComponentID: componentID,
			StartTime:   time.Now(),
		}
	}

	pm.metrics[componentID].PacketsDropped++
}

// GetMetrics returns metrics for a component
func (pm *PerformanceMonitor) GetMetrics(componentID string) *PerformanceMetrics {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if m, exists := pm.metrics[componentID]; exists {
		// Return a copy
		copy := *m
		return &copy
	}
	return nil
}

// GetAllMetrics returns all metrics
func (pm *PerformanceMonitor) GetAllMetrics() map[string]*PerformanceMetrics {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make(map[string]*PerformanceMetrics)
	for k, v := range pm.metrics {
		copy := *v
		result[k] = &copy
	}
	return result
}

// Reset resets metrics for a component
func (pm *PerformanceMonitor) Reset(componentID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	delete(pm.metrics, componentID)
}

// ResetAll resets all metrics
func (pm *PerformanceMonitor) ResetAll() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.metrics = make(map[string]*PerformanceMetrics)
}

// PerformanceOptimizer provides optimization recommendations
type PerformanceOptimizer struct {
	monitor *PerformanceMonitor
}

// NewPerformanceOptimizer creates a new performance optimizer
func NewPerformanceOptimizer(monitor *PerformanceMonitor) *PerformanceOptimizer {
	return &PerformanceOptimizer{
		monitor: monitor,
	}
}

// GetOptimizationRecommendations returns optimization recommendations
func (po *PerformanceOptimizer) GetOptimizationRecommendations() map[string][]string {
	recommendations := make(map[string][]string)

	metrics := po.monitor.GetAllMetrics()
	for componentID, m := range metrics {
		var recs []string

		// Check for high processing time
		if m.AvgProcessingTime > 100 {
			recs = append(recs, "High processing time detected. Consider GPU acceleration or model optimization.")
		}

		// Check for dropped packets
		if m.PacketsDropped > 0 {
			dropRate := float64(m.PacketsDropped) / float64(m.PacketsProcessed+m.PacketsDropped) * 100
			if dropRate > 5 {
				recs = append(recs, "High packet drop rate. Increase buffer size or reduce processing load.")
			}
		}

		// Check for low throughput
		if m.Throughput < 10 {
			recs = append(recs, "Low throughput detected. Consider parallel processing or batch optimization.")
		}

		if len(recs) > 0 {
			recommendations[componentID] = recs
		}
	}

	return recommendations
}

// LatencyAnalyzer analyzes end-to-end latency
type LatencyAnalyzer struct {
	startTimes map[string]time.Time
	mu         sync.Mutex
}

// NewLatencyAnalyzer creates a new latency analyzer
func NewLatencyAnalyzer() *LatencyAnalyzer {
	return &LatencyAnalyzer{
		startTimes: make(map[string]time.Time),
	}
}

// StartMeasurement starts latency measurement
func (la *LatencyAnalyzer) StartMeasurement(packetID string) {
	la.mu.Lock()
	defer la.mu.Unlock()
	la.startTimes[packetID] = time.Now()
}

// EndMeasurement ends latency measurement and returns latency in milliseconds
func (la *LatencyAnalyzer) EndMeasurement(packetID string) int64 {
	la.mu.Lock()
	defer la.mu.Unlock()

	if startTime, exists := la.startTimes[packetID]; exists {
		latency := time.Since(startTime).Milliseconds()
		delete(la.startTimes, packetID)
		return latency
	}
	return 0
}

// ClearMeasurement clears a measurement
func (la *LatencyAnalyzer) ClearMeasurement(packetID string) {
	la.mu.Lock()
	defer la.mu.Unlock()
	delete(la.startTimes, packetID)
}
