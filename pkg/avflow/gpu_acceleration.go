package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"fmt"
	"sync"
)

// GPUProvider represents GPU acceleration provider
type GPUProvider string

const (
	GPUProviderNone      GPUProvider = "none"
	GPUProviderNVIDIA    GPUProvider = "nvidia"
	GPUProviderAMD       GPUProvider = "amd"
	GPUProviderIntel     GPUProvider = "intel"
	GPUProviderApple     GPUProvider = "apple"
	GPUProviderQualcomm GPUProvider = "qualcomm"
)

// GPUInfo contains information about available GPU
type GPUInfo struct {
	// GPU provider
	Provider GPUProvider

	// Device name
	DeviceName string

	// Compute capability/version
	ComputeCapability string

	// Total memory in MB
	TotalMemory int64

	// Available memory in MB
	AvailableMemory int64

	// Driver version
	DriverVersion string

	// Whether GPU is available
	IsAvailable bool
}

// GPUAccelerator manages GPU acceleration
type GPUAccelerator struct {
	provider GPUProvider
	devices  []GPUInfo
	mu       sync.RWMutex
}

// NewGPUAccelerator creates a new GPU accelerator
func NewGPUAccelerator() *GPUAccelerator {
	return &GPUAccelerator{
		provider: GPUProviderNone,
		devices:  []GPUInfo{},
	}
}

// DetectGPU detects available GPU devices
func (g *GPUAccelerator) DetectGPU() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Try to detect NVIDIA GPU
	if nvidiaGPU := g.detectNVIDIA(); nvidiaGPU != nil {
		g.provider = GPUProviderNVIDIA
		g.devices = append(g.devices, *nvidiaGPU)
		return nil
	}

	// Try to detect AMD GPU
	if amdGPU := g.detectAMD(); amdGPU != nil {
		g.provider = GPUProviderAMD
		g.devices = append(g.devices, *amdGPU)
		return nil
	}

	// Try to detect Intel GPU
	if intelGPU := g.detectIntel(); intelGPU != nil {
		g.provider = GPUProviderIntel
		g.devices = append(g.devices, *intelGPU)
		return nil
	}

	// Try to detect Apple GPU
	if appleGPU := g.detectApple(); appleGPU != nil {
		g.provider = GPUProviderApple
		g.devices = append(g.devices, *appleGPU)
		return nil
	}

	g.provider = GPUProviderNone
	return fmt.Errorf("no GPU detected")
}

// GetProvider returns the detected GPU provider
func (g *GPUAccelerator) GetProvider() GPUProvider {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.provider
}

// GetDevices returns available GPU devices
func (g *GPUAccelerator) GetDevices() []GPUInfo {
	g.mu.RLock()
	defer g.mu.RUnlock()

	devices := make([]GPUInfo, len(g.devices))
	copy(devices, g.devices)
	return devices
}

// IsGPUAvailable checks if GPU is available
func (g *GPUAccelerator) IsGPUAvailable() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.provider != GPUProviderNone && len(g.devices) > 0
}

// GetPrimaryDevice returns the primary GPU device
func (g *GPUAccelerator) GetPrimaryDevice() *GPUInfo {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(g.devices) > 0 {
		return &g.devices[0]
	}
	return nil
}

// detectNVIDIA detects NVIDIA GPU
func (g *GPUAccelerator) detectNVIDIA() *GPUInfo {
	// Mock implementation - in real code, would use nvidia-ml-py or similar
	return &GPUInfo{
		Provider:          GPUProviderNVIDIA,
		DeviceName:        "NVIDIA GeForce RTX 3090",
		ComputeCapability: "8.6",
		TotalMemory:       24576,
		AvailableMemory:   24576,
		DriverVersion:     "535.0",
		IsAvailable:       true,
	}
}

// detectAMD detects AMD GPU
func (g *GPUAccelerator) detectAMD() *GPUInfo {
	// Mock implementation
	return nil
}

// detectIntel detects Intel GPU
func (g *GPUAccelerator) detectIntel() *GPUInfo {
	// Mock implementation
	return nil
}

// detectApple detects Apple GPU
func (g *GPUAccelerator) detectApple() *GPUInfo {
	// Mock implementation
	return nil
}

// GPUMemoryManager manages GPU memory allocation
type GPUMemoryManager struct {
	totalMemory     int64
	allocatedMemory int64
	mu              sync.Mutex
}

// NewGPUMemoryManager creates a new GPU memory manager
func NewGPUMemoryManager(totalMemory int64) *GPUMemoryManager {
	return &GPUMemoryManager{
		totalMemory:     totalMemory,
		allocatedMemory: 0,
	}
}

// Allocate allocates GPU memory
func (m *GPUMemoryManager) Allocate(size int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.allocatedMemory+size > m.totalMemory {
		return false, fmt.Errorf("insufficient GPU memory: need %d MB, available %d MB",
			size, m.totalMemory-m.allocatedMemory)
	}

	m.allocatedMemory += size
	return true, nil
}

// Release releases GPU memory
func (m *GPUMemoryManager) Release(size int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.allocatedMemory < size {
		return fmt.Errorf("cannot release more memory than allocated")
	}

	m.allocatedMemory -= size
	return nil
}

// GetAvailableMemory returns available GPU memory
func (m *GPUMemoryManager) GetAvailableMemory() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalMemory - m.allocatedMemory
}

// GetAllocatedMemory returns allocated GPU memory
func (m *GPUMemoryManager) GetAllocatedMemory() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.allocatedMemory
}

// GetMemoryUsagePercent returns memory usage percentage
func (m *GPUMemoryManager) GetMemoryUsagePercent() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.totalMemory == 0 {
		return 0
	}
	return float64(m.allocatedMemory) / float64(m.totalMemory) * 100
}
