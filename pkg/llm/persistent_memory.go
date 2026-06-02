package llm

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PersistentMemoryStore defines the interface for persistent memory storage
type PersistentMemoryStore interface {
	// Save saves data to persistent storage
	Save(ctx context.Context, key string, data interface{}) error

	// Load loads data from persistent storage
	Load(ctx context.Context, key string, data interface{}) error

	// Delete deletes data from persistent storage
	Delete(ctx context.Context, key string) error

	// List lists all keys in persistent storage
	List(ctx context.Context) ([]string, error)

	// Clear clears all data from persistent storage
	Clear(ctx context.Context) error
}

// FileSystemStore implements PersistentMemoryStore using the file system
type FileSystemStore struct {
	basePath string
	mu       sync.RWMutex
}

// NewFileSystemStore creates a new file system store
func NewFileSystemStore(basePath string) (*FileSystemStore, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	return &FileSystemStore{
		basePath: basePath,
	}, nil
}

// Save saves data to file
func (fs *FileSystemStore) Save(ctx context.Context, key string, data interface{}) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	filePath := filepath.Join(fs.basePath, key+".json")

	// Create parent directories if needed
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Marshal data to JSON
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	// Write to file
	if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// Load loads data from file
func (fs *FileSystemStore) Load(ctx context.Context, key string, data interface{}) error {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	filePath := filepath.Join(fs.basePath, key+".json")

	// Read file
	jsonData, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("key not found: %s", key)
		}
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Unmarshal JSON
	if err := json.Unmarshal(jsonData, data); err != nil {
		return fmt.Errorf("failed to unmarshal data: %w", err)
	}

	return nil
}

// Delete deletes a file
func (fs *FileSystemStore) Delete(ctx context.Context, key string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	filePath := filepath.Join(fs.basePath, key+".json")

	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return nil // Already deleted
		}
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return nil
}

// List lists all keys
func (fs *FileSystemStore) List(ctx context.Context) ([]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	var keys []string

	entries, err := os.ReadDir(fs.basePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			key := entry.Name()[:len(entry.Name())-5] // Remove .json extension
			keys = append(keys, key)
		}
	}

	return keys, nil
}

// Clear clears all files
func (fs *FileSystemStore) Clear(ctx context.Context) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	entries, err := os.ReadDir(fs.basePath)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			filePath := filepath.Join(fs.basePath, entry.Name())
			if err := os.Remove(filePath); err != nil {
				return fmt.Errorf("failed to delete file: %w", err)
			}
		}
	}

	return nil
}

// PersistentMemory extends Memory with persistence
type PersistentMemory struct {
	messages []Message
	maxSize  int
	store    PersistentMemoryStore
	storeKey string
	mu       sync.RWMutex
}

// NewPersistentMemory creates a new persistent memory
func NewPersistentMemory(maxSize int, store PersistentMemoryStore, storeKey string) *PersistentMemory {
	pm := &PersistentMemory{
		messages: make([]Message, 0, maxSize),
		maxSize:  maxSize,
		store:    store,
		storeKey: storeKey,
	}

	// Load existing messages from storage
	var messages []Message
	if err := store.Load(context.Background(), storeKey, &messages); err == nil {
		pm.messages = messages
	}

	return pm
}

// AddMessage adds a message and persists it
func (pm *PersistentMemory) AddMessage(ctx context.Context, message Message) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if message.Timestamp.IsZero() {
		message.Timestamp = time.Now()
	}

	pm.messages = append(pm.messages, message)

	// Keep only recent messages in memory
	if len(pm.messages) > pm.maxSize {
		pm.messages = pm.messages[len(pm.messages)-pm.maxSize:]
	}

	// Persist to storage
	return pm.store.Save(ctx, pm.storeKey, pm.messages)
}

// GetMessages retrieves messages from memory
func (pm *PersistentMemory) GetMessages(ctx context.Context, limit int) ([]Message, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if limit <= 0 || limit > len(pm.messages) {
		limit = len(pm.messages)
	}

	result := make([]Message, limit)
	copy(result, pm.messages[len(pm.messages)-limit:])
	return result, nil
}

// Clear clears all messages
func (pm *PersistentMemory) Clear(ctx context.Context) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.messages = make([]Message, 0, pm.maxSize)
	return pm.store.Delete(ctx, pm.storeKey)
}

// GetContext returns formatted context for LLM
func (pm *PersistentMemory) GetContext() string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	context := ""
	for _, msg := range pm.messages {
		context += fmt.Sprintf("%s: %s\n", msg.Role, msg.Content)
	}

	return context
}

// MemoryProfile represents a user's memory profile
type MemoryProfile struct {
	UserID       string                 `json:"user_id"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
	Preferences  map[string]interface{} `json:"preferences"`
	Metadata     map[string]interface{} `json:"metadata"`
	MessageCount int                    `json:"message_count"`
}

// MemoryProfileManager manages user memory profiles
type MemoryProfileManager struct {
	store PersistentMemoryStore
	mu    sync.RWMutex
}

// NewMemoryProfileManager creates a new memory profile manager
func NewMemoryProfileManager(store PersistentMemoryStore) *MemoryProfileManager {
	return &MemoryProfileManager{
		store: store,
	}
}

// CreateProfile creates a new memory profile
func (mpm *MemoryProfileManager) CreateProfile(ctx context.Context, userID string) (*MemoryProfile, error) {
	mpm.mu.Lock()
	defer mpm.mu.Unlock()

	profile := &MemoryProfile{
		UserID:      userID,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Preferences: make(map[string]interface{}),
		Metadata:    make(map[string]interface{}),
	}

	if err := mpm.store.Save(ctx, "profile_"+userID, profile); err != nil {
		return nil, err
	}

	return profile, nil
}

// GetProfile retrieves a memory profile
func (mpm *MemoryProfileManager) GetProfile(ctx context.Context, userID string) (*MemoryProfile, error) {
	mpm.mu.RLock()
	defer mpm.mu.RUnlock()

	var profile MemoryProfile
	if err := mpm.store.Load(ctx, "profile_"+userID, &profile); err != nil {
		return nil, err
	}

	return &profile, nil
}

// UpdateProfile updates a memory profile
func (mpm *MemoryProfileManager) UpdateProfile(ctx context.Context, profile *MemoryProfile) error {
	mpm.mu.Lock()
	defer mpm.mu.Unlock()

	profile.UpdatedAt = time.Now()
	return mpm.store.Save(ctx, "profile_"+profile.UserID, profile)
}

// DeleteProfile deletes a memory profile
func (mpm *MemoryProfileManager) DeleteProfile(ctx context.Context, userID string) error {
	mpm.mu.Lock()
	defer mpm.mu.Unlock()

	return mpm.store.Delete(ctx, "profile_"+userID)
}

// KnowledgeBase represents a persistent knowledge base
type KnowledgeBase struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Documents map[string]Document    `json:"documents"`
	Metadata  map[string]interface{} `json:"metadata"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// KnowledgeBaseManager manages knowledge bases
type KnowledgeBaseManager struct {
	store PersistentMemoryStore
	mu    sync.RWMutex
}

// NewKnowledgeBaseManager creates a new knowledge base manager
func NewKnowledgeBaseManager(store PersistentMemoryStore) *KnowledgeBaseManager {
	return &KnowledgeBaseManager{
		store: store,
	}
}

// CreateKnowledgeBase creates a new knowledge base
func (kbm *KnowledgeBaseManager) CreateKnowledgeBase(ctx context.Context, id, name string) (*KnowledgeBase, error) {
	kbm.mu.Lock()
	defer kbm.mu.Unlock()

	kb := &KnowledgeBase{
		ID:        id,
		Name:      name,
		Documents: make(map[string]Document),
		Metadata:  make(map[string]interface{}),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := kbm.store.Save(ctx, "kb_"+id, kb); err != nil {
		return nil, err
	}

	return kb, nil
}

// AddDocument adds a document to knowledge base
func (kbm *KnowledgeBaseManager) AddDocument(ctx context.Context, kbID string, doc Document) error {
	kbm.mu.Lock()
	defer kbm.mu.Unlock()

	var kb KnowledgeBase
	if err := kbm.store.Load(ctx, "kb_"+kbID, &kb); err != nil {
		return err
	}

	kb.Documents[doc.ID] = doc
	kb.UpdatedAt = time.Now()

	return kbm.store.Save(ctx, "kb_"+kbID, &kb)
}

// GetKnowledgeBase retrieves a knowledge base
func (kbm *KnowledgeBaseManager) GetKnowledgeBase(ctx context.Context, id string) (*KnowledgeBase, error) {
	kbm.mu.RLock()
	defer kbm.mu.RUnlock()

	var kb KnowledgeBase
	if err := kbm.store.Load(ctx, "kb_"+id, &kb); err != nil {
		return nil, err
	}

	return &kb, nil
}

// DeleteKnowledgeBase deletes a knowledge base
func (kbm *KnowledgeBaseManager) DeleteKnowledgeBase(ctx context.Context, id string) error {
	kbm.mu.Lock()
	defer kbm.mu.Unlock()

	return kbm.store.Delete(ctx, "kb_"+id)
}

// ConversationHistory represents a persistent conversation
type ConversationHistory struct {
	ID        string                 `json:"id"`
	UserID    string                 `json:"user_id"`
	Messages  []Message              `json:"messages"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
	Metadata  map[string]interface{} `json:"metadata"`
}

// ConversationManager manages conversation histories
type ConversationManager struct {
	store PersistentMemoryStore
	mu    sync.RWMutex
}

// NewConversationManager creates a new conversation manager
func NewConversationManager(store PersistentMemoryStore) *ConversationManager {
	return &ConversationManager{
		store: store,
	}
}

// CreateConversation creates a new conversation
func (cm *ConversationManager) CreateConversation(ctx context.Context, id, userID string) (*ConversationHistory, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	conv := &ConversationHistory{
		ID:        id,
		UserID:    userID,
		Messages:  make([]Message, 0),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Metadata:  make(map[string]interface{}),
	}

	if err := cm.store.Save(ctx, "conv_"+id, conv); err != nil {
		return nil, err
	}

	return conv, nil
}

// AddMessageToConversation adds a message to conversation
func (cm *ConversationManager) AddMessageToConversation(ctx context.Context, convID string, msg Message) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	var conv ConversationHistory
	if err := cm.store.Load(ctx, "conv_"+convID, &conv); err != nil {
		return err
	}

	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}

	conv.Messages = append(conv.Messages, msg)
	conv.UpdatedAt = time.Now()

	return cm.store.Save(ctx, "conv_"+convID, &conv)
}

// GetConversation retrieves a conversation
func (cm *ConversationManager) GetConversation(ctx context.Context, id string) (*ConversationHistory, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	var conv ConversationHistory
	if err := cm.store.Load(ctx, "conv_"+id, &conv); err != nil {
		return nil, err
	}

	return &conv, nil
}

// ListConversations lists all conversations for a user
func (cm *ConversationManager) ListConversations(ctx context.Context, userID string) ([]ConversationHistory, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	keys, err := cm.store.List(ctx)
	if err != nil {
		return nil, err
	}

	var conversations []ConversationHistory
	for _, key := range keys {
		if len(key) > 5 && key[:5] == "conv_" {
			var conv ConversationHistory
			if err := cm.store.Load(ctx, key, &conv); err == nil && conv.UserID == userID {
				conversations = append(conversations, conv)
			}
		}
	}

	return conversations, nil
}

// DeleteConversation deletes a conversation
func (cm *ConversationManager) DeleteConversation(ctx context.Context, id string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	return cm.store.Delete(ctx, "conv_"+id)
}
