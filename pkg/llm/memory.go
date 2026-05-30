package llm

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Message represents a message in conversation history
type Message struct {
	Role      string    // "user", "assistant", "system"
	Content   string
	Timestamp time.Time
}

// Memory defines the interface for conversation memory
type Memory interface {
	// AddMessage adds a message to memory
	AddMessage(ctx context.Context, message Message) error

	// GetMessages retrieves messages from memory
	GetMessages(ctx context.Context, limit int) ([]Message, error)

	// Clear clears all messages
	Clear(ctx context.Context) error

	// GetContext returns formatted context for LLM
	GetContext() string
}

// BufferMemory stores messages in memory
type BufferMemory struct {
	messages  []Message
	maxSize   int
	mu        sync.RWMutex
}

// NewBufferMemory creates a new buffer memory
func NewBufferMemory(maxSize int) *BufferMemory {
	return &BufferMemory{
		messages: make([]Message, 0, maxSize),
		maxSize:  maxSize,
	}
}

// AddMessage adds a message to memory
func (bm *BufferMemory) AddMessage(ctx context.Context, message Message) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if message.Timestamp.IsZero() {
		message.Timestamp = time.Now()
	}

	bm.messages = append(bm.messages, message)

	// Keep only recent messages
	if len(bm.messages) > bm.maxSize {
		bm.messages = bm.messages[len(bm.messages)-bm.maxSize:]
	}

	return nil
}

// GetMessages retrieves messages from memory
func (bm *BufferMemory) GetMessages(ctx context.Context, limit int) ([]Message, error) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	if limit <= 0 || limit > len(bm.messages) {
		limit = len(bm.messages)
	}

	result := make([]Message, limit)
	copy(result, bm.messages[len(bm.messages)-limit:])
	return result, nil
}

// Clear clears all messages
func (bm *BufferMemory) Clear(ctx context.Context) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	bm.messages = make([]Message, 0, bm.maxSize)
	return nil
}

// GetContext returns formatted context for LLM
func (bm *BufferMemory) GetContext() string {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	context := ""
	for _, msg := range bm.messages {
		context += fmt.Sprintf("%s: %s\n", msg.Role, msg.Content)
	}

	return context
}

// SummaryMemory summarizes old messages to save tokens
type SummaryMemory struct {
	messages  []Message
	maxSize   int
	llm       LLMModel
	mu        sync.RWMutex
}

// NewSummaryMemory creates a new summary memory
func NewSummaryMemory(maxSize int, llm LLMModel) *SummaryMemory {
	return &SummaryMemory{
		messages: make([]Message, 0, maxSize),
		maxSize:  maxSize,
		llm:      llm,
	}
}

// AddMessage adds a message to memory
func (sm *SummaryMemory) AddMessage(ctx context.Context, message Message) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if message.Timestamp.IsZero() {
		message.Timestamp = time.Now()
	}

	sm.messages = append(sm.messages, message)

	// Summarize old messages if needed
	if len(sm.messages) > sm.maxSize {
		// Keep recent messages
		sm.messages = sm.messages[len(sm.messages)-sm.maxSize:]
	}

	return nil
}

// GetMessages retrieves messages from memory
func (sm *SummaryMemory) GetMessages(ctx context.Context, limit int) ([]Message, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if limit <= 0 || limit > len(sm.messages) {
		limit = len(sm.messages)
	}

	result := make([]Message, limit)
	copy(result, sm.messages[len(sm.messages)-limit:])
	return result, nil
}

// Clear clears all messages
func (sm *SummaryMemory) Clear(ctx context.Context) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.messages = make([]Message, 0, sm.maxSize)
	return nil
}

// GetContext returns formatted context for LLM
func (sm *SummaryMemory) GetContext() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	context := ""
	for _, msg := range sm.messages {
		context += fmt.Sprintf("%s: %s\n", msg.Role, msg.Content)
	}

	return context
}

// MemoryStep is a chain step for memory management
type MemoryStep struct {
	name       string
	memory     Memory
	role       string
	inputKey   string
	outputKey  string
}

// NewMemoryStep creates a new memory step
func NewMemoryStep(name string, memory Memory, role, inputKey, outputKey string) *MemoryStep {
	return &MemoryStep{
		name:      name,
		memory:    memory,
		role:      role,
		inputKey:  inputKey,
		outputKey: outputKey,
	}
}

// Execute executes the memory step
func (ms *MemoryStep) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	content, ok := input[ms.inputKey].(string)
	if !ok {
		return nil, fmt.Errorf("input key %s not found or not a string", ms.inputKey)
	}

	// Add message to memory
	msg := Message{
		Role:      ms.role,
		Content:   content,
		Timestamp: time.Now(),
	}

	if err := ms.memory.AddMessage(ctx, msg); err != nil {
		return nil, err
	}

	// Return memory context
	return map[string]interface{}{
		ms.outputKey: ms.memory.GetContext(),
	}, nil
}

// GetInputKeys returns required input keys
func (ms *MemoryStep) GetInputKeys() []string {
	return []string{ms.inputKey}
}

// GetOutputKeys returns produced output keys
func (ms *MemoryStep) GetOutputKeys() []string {
	return []string{ms.outputKey}
}

// GetName returns the step name
func (ms *MemoryStep) GetName() string {
	return ms.name
}
