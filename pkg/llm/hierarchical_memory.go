package llm

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MemoryType defines the type of memory
type MemoryType int

const (
	// MemoryTypeSensory - Sensory memory (milliseconds to seconds)
	MemoryTypeSensory MemoryType = iota
	// MemoryTypeWorking - Working memory (seconds to minutes)
	MemoryTypeWorking
	// MemoryTypeShortTerm - Short-term memory (minutes to hours)
	MemoryTypeShortTerm
	// MemoryTypeLongTerm - Long-term memory (hours to years)
	MemoryTypeLongTerm
	// MemoryTypeEpisodic - Episodic memory (events and experiences)
	MemoryTypeEpisodic
	// MemoryTypeSemantic - Semantic memory (facts and concepts)
	MemoryTypeSemantic
	// MemoryTypeProc - Procedural memory (skills and habits)
	MemoryTypeProc
)

// MemoryEntry represents a single memory entry
type MemoryEntry struct {
	ID           string                 `json:"id"`
	Type         MemoryType             `json:"type"`
	Content      string                 `json:"content"`
	Timestamp    time.Time              `json:"timestamp"`
	AccessCount  int                    `json:"access_count"`
	LastAccessed time.Time              `json:"last_accessed"`
	Importance   float64                `json:"importance"` // 0.0 to 1.0
	Metadata     map[string]interface{} `json:"metadata"`
	Associations []string               `json:"associations"` // IDs of related memories
	Decay        float64                `json:"decay"`        // Forgetting curve
}

// SensoryMemory - Very short-term sensory buffer
type SensoryMemory struct {
	entries  []MemoryEntry
	maxSize  int
	ttl      time.Duration // Time to live
	mu       sync.RWMutex
	store    PersistentMemoryStore
	storeKey string
}

// NewSensoryMemory creates a new sensory memory
func NewSensoryMemory(maxSize int, ttl time.Duration, store PersistentMemoryStore, storeKey string) *SensoryMemory {
	sm := &SensoryMemory{
		entries:  make([]MemoryEntry, 0, maxSize),
		maxSize:  maxSize,
		ttl:      ttl,
		store:    store,
		storeKey: storeKey,
	}

	// Load existing entries
	var entries []MemoryEntry
	if err := store.Load(context.Background(), storeKey, &entries); err == nil {
		sm.entries = entries
	}

	return sm
}

// Add adds an entry to sensory memory
func (sm *SensoryMemory) Add(ctx context.Context, entry MemoryEntry) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	entry.Timestamp = time.Now()
	entry.Type = MemoryTypeSensory

	sm.entries = append(sm.entries, entry)

	// Keep only recent entries
	if len(sm.entries) > sm.maxSize {
		sm.entries = sm.entries[len(sm.entries)-sm.maxSize:]
	}

	// Clean expired entries
	sm.cleanExpired()

	return sm.store.Save(ctx, sm.storeKey, sm.entries)
}

// cleanExpired removes expired entries
func (sm *SensoryMemory) cleanExpired() {
	now := time.Now()
	filtered := make([]MemoryEntry, 0)

	for _, entry := range sm.entries {
		if now.Sub(entry.Timestamp) < sm.ttl {
			filtered = append(filtered, entry)
		}
	}

	sm.entries = filtered
}

// Get retrieves entries
func (sm *SensoryMemory) Get(ctx context.Context, limit int) ([]MemoryEntry, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	sm.cleanExpired()

	if limit <= 0 || limit > len(sm.entries) {
		limit = len(sm.entries)
	}

	result := make([]MemoryEntry, limit)
	copy(result, sm.entries[len(sm.entries)-limit:])
	return result, nil
}

// WorkingMemory - Active memory for current task
type WorkingMemory struct {
	entries    []MemoryEntry
	maxSize    int
	mu         sync.RWMutex
	store      PersistentMemoryStore
	storeKey   string
	lastUpdate time.Time
}

// NewWorkingMemory creates a new working memory
func NewWorkingMemory(maxSize int, store PersistentMemoryStore, storeKey string) *WorkingMemory {
	wm := &WorkingMemory{
		entries:    make([]MemoryEntry, 0, maxSize),
		maxSize:    maxSize,
		store:      store,
		storeKey:   storeKey,
		lastUpdate: time.Now(),
	}

	var entries []MemoryEntry
	if err := store.Load(context.Background(), storeKey, &entries); err == nil {
		wm.entries = entries
	}

	return wm
}

// Add adds an entry to working memory
func (wm *WorkingMemory) Add(ctx context.Context, entry MemoryEntry) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	entry.Timestamp = time.Now()
	entry.Type = MemoryTypeWorking
	entry.AccessCount = 1

	wm.entries = append(wm.entries, entry)

	if len(wm.entries) > wm.maxSize {
		wm.entries = wm.entries[len(wm.entries)-wm.maxSize:]
	}

	wm.lastUpdate = time.Now()
	return wm.store.Save(ctx, wm.storeKey, wm.entries)
}

// Access increments access count
func (wm *WorkingMemory) Access(ctx context.Context, id string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	for i, entry := range wm.entries {
		if entry.ID == id {
			wm.entries[i].AccessCount++
			wm.entries[i].LastAccessed = time.Now()
			break
		}
	}

	return wm.store.Save(ctx, wm.storeKey, wm.entries)
}

// Get retrieves entries
func (wm *WorkingMemory) Get(ctx context.Context, limit int) ([]MemoryEntry, error) {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	if limit <= 0 || limit > len(wm.entries) {
		limit = len(wm.entries)
	}

	result := make([]MemoryEntry, limit)
	copy(result, wm.entries[len(wm.entries)-limit:])
	return result, nil
}

// ShortTermMemory - Medium-term memory (minutes to hours)
type ShortTermMemory struct {
	entries  []MemoryEntry
	maxSize  int
	ttl      time.Duration
	mu       sync.RWMutex
	store    PersistentMemoryStore
	storeKey string
}

// NewShortTermMemory creates a new short-term memory
func NewShortTermMemory(maxSize int, ttl time.Duration, store PersistentMemoryStore, storeKey string) *ShortTermMemory {
	stm := &ShortTermMemory{
		entries:  make([]MemoryEntry, 0, maxSize),
		maxSize:  maxSize,
		ttl:      ttl,
		store:    store,
		storeKey: storeKey,
	}

	var entries []MemoryEntry
	if err := store.Load(context.Background(), storeKey, &entries); err == nil {
		stm.entries = entries
	}

	return stm
}

// Add adds an entry to short-term memory
func (stm *ShortTermMemory) Add(ctx context.Context, entry MemoryEntry) error {
	stm.mu.Lock()
	defer stm.mu.Unlock()

	entry.Timestamp = time.Now()
	entry.Type = MemoryTypeShortTerm

	stm.entries = append(stm.entries, entry)

	if len(stm.entries) > stm.maxSize {
		stm.entries = stm.entries[len(stm.entries)-stm.maxSize:]
	}

	stm.cleanExpired()
	return stm.store.Save(ctx, stm.storeKey, stm.entries)
}

// cleanExpired removes expired entries
func (stm *ShortTermMemory) cleanExpired() {
	now := time.Now()
	filtered := make([]MemoryEntry, 0)

	for _, entry := range stm.entries {
		if now.Sub(entry.Timestamp) < stm.ttl {
			filtered = append(filtered, entry)
		}
	}

	stm.entries = filtered
}

// Get retrieves entries
func (stm *ShortTermMemory) Get(ctx context.Context, limit int) ([]MemoryEntry, error) {
	stm.mu.RLock()
	defer stm.mu.RUnlock()

	stm.cleanExpired()

	if limit <= 0 || limit > len(stm.entries) {
		limit = len(stm.entries)
	}

	result := make([]MemoryEntry, limit)
	copy(result, stm.entries[len(stm.entries)-limit:])
	return result, nil
}

// LongTermMemory - Long-term storage with consolidation
type LongTermMemory struct {
	entries  map[string]MemoryEntry
	maxSize  int
	mu       sync.RWMutex
	store    PersistentMemoryStore
	storeKey string
	index    map[string][]string // Content-based index
}

// NewLongTermMemory creates a new long-term memory
func NewLongTermMemory(maxSize int, store PersistentMemoryStore, storeKey string) *LongTermMemory {
	ltm := &LongTermMemory{
		entries:  make(map[string]MemoryEntry),
		maxSize:  maxSize,
		store:    store,
		storeKey: storeKey,
		index:    make(map[string][]string),
	}

	var entries map[string]MemoryEntry
	if err := store.Load(context.Background(), storeKey, &entries); err == nil {
		ltm.entries = entries
		ltm.buildIndex()
	}

	return ltm
}

// Add adds an entry to long-term memory
func (ltm *LongTermMemory) Add(ctx context.Context, entry MemoryEntry) error {
	ltm.mu.Lock()
	defer ltm.mu.Unlock()

	entry.Timestamp = time.Now()
	entry.Type = MemoryTypeLongTerm

	ltm.entries[entry.ID] = entry
	ltm.indexEntry(entry)

	// Consolidate if needed
	if len(ltm.entries) > ltm.maxSize {
		ltm.consolidate()
	}

	return ltm.store.Save(ctx, ltm.storeKey, ltm.entries)
}

// indexEntry adds entry to index
func (ltm *LongTermMemory) indexEntry(entry MemoryEntry) {
	// Simple keyword indexing
	keywords := extractKeywords(entry.Content)
	for _, kw := range keywords {
		ltm.index[kw] = append(ltm.index[kw], entry.ID)
	}
}

// buildIndex rebuilds the index
func (ltm *LongTermMemory) buildIndex() {
	ltm.index = make(map[string][]string)
	for _, entry := range ltm.entries {
		ltm.indexEntry(entry)
	}
}

// consolidate removes least important entries
func (ltm *LongTermMemory) consolidate() {
	// Find entry with lowest importance
	minID := ""
	minImportance := 1.0

	for id, entry := range ltm.entries {
		if entry.Importance < minImportance {
			minImportance = entry.Importance
			minID = id
		}
	}

	if minID != "" {
		delete(ltm.entries, minID)
	}
}

// Get retrieves an entry
func (ltm *LongTermMemory) Get(ctx context.Context, id string) (*MemoryEntry, error) {
	ltm.mu.RLock()
	defer ltm.mu.RUnlock()

	entry, exists := ltm.entries[id]
	if !exists {
		return nil, fmt.Errorf("entry not found: %s", id)
	}

	return &entry, nil
}

// Search searches by keyword
func (ltm *LongTermMemory) Search(ctx context.Context, keyword string) ([]MemoryEntry, error) {
	ltm.mu.RLock()
	defer ltm.mu.RUnlock()

	ids, exists := ltm.index[keyword]
	if !exists {
		return []MemoryEntry{}, nil
	}

	result := make([]MemoryEntry, 0)
	for _, id := range ids {
		if entry, ok := ltm.entries[id]; ok {
			result = append(result, entry)
		}
	}

	return result, nil
}

// EpisodicMemory - Event-based memory
type EpisodicMemory struct {
	episodes map[string]*Episode
	mu       sync.RWMutex
	store    PersistentMemoryStore
	storeKey string
}

// Episode represents a sequence of events
type Episode struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	StartTime time.Time     `json:"start_time"`
	EndTime   time.Time     `json:"end_time"`
	Events    []MemoryEntry `json:"events"`
	Context   string        `json:"context"`
}

// NewEpisodicMemory creates a new episodic memory
func NewEpisodicMemory(store PersistentMemoryStore, storeKey string) *EpisodicMemory {
	em := &EpisodicMemory{
		episodes: make(map[string]*Episode),
		store:    store,
		storeKey: storeKey,
	}

	var episodes map[string]*Episode
	if err := store.Load(context.Background(), storeKey, &episodes); err == nil {
		em.episodes = episodes
	}

	return em
}

// CreateEpisode creates a new episode
func (em *EpisodicMemory) CreateEpisode(ctx context.Context, id, name string) (*Episode, error) {
	em.mu.Lock()
	defer em.mu.Unlock()

	episode := &Episode{
		ID:        id,
		Name:      name,
		StartTime: time.Now(),
		Events:    make([]MemoryEntry, 0),
	}

	em.episodes[id] = episode
	return episode, em.store.Save(ctx, em.storeKey, em.episodes)
}

// AddEventToEpisode adds an event to an episode
func (em *EpisodicMemory) AddEventToEpisode(ctx context.Context, episodeID string, event MemoryEntry) error {
	em.mu.Lock()
	defer em.mu.Unlock()

	episode, exists := em.episodes[episodeID]
	if !exists {
		return fmt.Errorf("episode not found: %s", episodeID)
	}

	event.Type = MemoryTypeEpisodic
	event.Timestamp = time.Now()
	episode.Events = append(episode.Events, event)

	return em.store.Save(ctx, em.storeKey, em.episodes)
}

// CloseEpisode closes an episode
func (em *EpisodicMemory) CloseEpisode(ctx context.Context, episodeID string) error {
	em.mu.Lock()
	defer em.mu.Unlock()

	episode, exists := em.episodes[episodeID]
	if !exists {
		return fmt.Errorf("episode not found: %s", episodeID)
	}

	episode.EndTime = time.Now()
	return em.store.Save(ctx, em.storeKey, em.episodes)
}

// GetEpisode retrieves an episode
func (em *EpisodicMemory) GetEpisode(ctx context.Context, episodeID string) (*Episode, error) {
	em.mu.RLock()
	defer em.mu.RUnlock()

	episode, exists := em.episodes[episodeID]
	if !exists {
		return nil, fmt.Errorf("episode not found: %s", episodeID)
	}

	return episode, nil
}

// SemanticMemory - Fact and concept-based memory
type SemanticMemory struct {
	facts    map[string]string // Fact ID -> Content
	concepts map[string]*Concept
	mu       sync.RWMutex
	store    PersistentMemoryStore
	storeKey string
}

// Concept represents a semantic concept
type Concept struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Definition   string   `json:"definition"`
	RelatedTerms []string `json:"related_terms"`
	Category     string   `json:"category"`
}

// NewSemanticMemory creates a new semantic memory
func NewSemanticMemory(store PersistentMemoryStore, storeKey string) *SemanticMemory {
	sm := &SemanticMemory{
		facts:    make(map[string]string),
		concepts: make(map[string]*Concept),
		store:    store,
		storeKey: storeKey,
	}

	var data map[string]interface{}
	if err := store.Load(context.Background(), storeKey, &data); err == nil {
		// Load facts and concepts
	}

	return sm
}

// AddFact adds a fact
func (sm *SemanticMemory) AddFact(ctx context.Context, id, content string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.facts[id] = content
	return sm.store.Save(ctx, sm.storeKey, map[string]interface{}{
		"facts":    sm.facts,
		"concepts": sm.concepts,
	})
}

// AddConcept adds a concept
func (sm *SemanticMemory) AddConcept(ctx context.Context, concept *Concept) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.concepts[concept.ID] = concept
	return sm.store.Save(ctx, sm.storeKey, map[string]interface{}{
		"facts":    sm.facts,
		"concepts": sm.concepts,
	})
}

// GetConcept retrieves a concept
func (sm *SemanticMemory) GetConcept(ctx context.Context, id string) (*Concept, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	concept, exists := sm.concepts[id]
	if !exists {
		return nil, fmt.Errorf("concept not found: %s", id)
	}

	return concept, nil
}

// ProceduralMemory - Skill and habit-based memory
type ProceduralMemory struct {
	procedures map[string]*Procedure
	mu         sync.RWMutex
	store      PersistentMemoryStore
	storeKey   string
}

// Procedure represents a skill or habit
type Procedure struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Steps       []string  `json:"steps"`
	Category    string    `json:"category"`
	Proficiency float64   `json:"proficiency"` // 0.0 to 1.0
	LastUsed    time.Time `json:"last_used"`
}

// NewProceduralMemory creates a new procedural memory
func NewProceduralMemory(store PersistentMemoryStore, storeKey string) *ProceduralMemory {
	pm := &ProceduralMemory{
		procedures: make(map[string]*Procedure),
		store:      store,
		storeKey:   storeKey,
	}

	var procedures map[string]*Procedure
	if err := store.Load(context.Background(), storeKey, &procedures); err == nil {
		pm.procedures = procedures
	}

	return pm
}

// AddProcedure adds a procedure
func (pm *ProceduralMemory) AddProcedure(ctx context.Context, procedure *Procedure) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.procedures[procedure.ID] = procedure
	return pm.store.Save(ctx, pm.storeKey, pm.procedures)
}

// ExecuteProcedure marks a procedure as executed
func (pm *ProceduralMemory) ExecuteProcedure(ctx context.Context, id string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	procedure, exists := pm.procedures[id]
	if !exists {
		return fmt.Errorf("procedure not found: %s", id)
	}

	procedure.LastUsed = time.Now()
	// Increase proficiency
	if procedure.Proficiency < 1.0 {
		procedure.Proficiency += 0.01
	}

	return pm.store.Save(ctx, pm.storeKey, pm.procedures)
}

// GetProcedure retrieves a procedure
func (pm *ProceduralMemory) GetProcedure(ctx context.Context, id string) (*Procedure, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	procedure, exists := pm.procedures[id]
	if !exists {
		return nil, fmt.Errorf("procedure not found: %s", id)
	}

	return procedure, nil
}

// HierarchicalMemorySystem - Complete memory hierarchy
type HierarchicalMemorySystem struct {
	Sensory    *SensoryMemory
	Working    *WorkingMemory
	ShortTerm  *ShortTermMemory
	LongTerm   *LongTermMemory
	Episodic   *EpisodicMemory
	Semantic   *SemanticMemory
	Procedural *ProceduralMemory
	mu         sync.RWMutex
}

// NewHierarchicalMemorySystem creates a complete memory system
func NewHierarchicalMemorySystem(store PersistentMemoryStore) *HierarchicalMemorySystem {
	return &HierarchicalMemorySystem{
		Sensory:    NewSensoryMemory(100, 5*time.Second, store, "sensory"),
		Working:    NewWorkingMemory(50, store, "working"),
		ShortTerm:  NewShortTermMemory(200, 1*time.Hour, store, "short_term"),
		LongTerm:   NewLongTermMemory(10000, store, "long_term"),
		Episodic:   NewEpisodicMemory(store, "episodic"),
		Semantic:   NewSemanticMemory(store, "semantic"),
		Procedural: NewProceduralMemory(store, "procedural"),
	}
}

// AddMemory adds memory to appropriate level
func (hms *HierarchicalMemorySystem) AddMemory(ctx context.Context, entry MemoryEntry) error {
	hms.mu.Lock()
	defer hms.mu.Unlock()

	// Add to sensory first
	if err := hms.Sensory.Add(ctx, entry); err != nil {
		return err
	}

	// If important, add to working memory
	if entry.Importance > 0.5 {
		if err := hms.Working.Add(ctx, entry); err != nil {
			return err
		}
	}

	// If very important, add to short-term
	if entry.Importance > 0.7 {
		if err := hms.ShortTerm.Add(ctx, entry); err != nil {
			return err
		}
	}

	// If critical, add to long-term
	if entry.Importance > 0.9 {
		if err := hms.LongTerm.Add(ctx, entry); err != nil {
			return err
		}
	}

	return nil
}

// Recall retrieves memory from all levels
func (hms *HierarchicalMemorySystem) Recall(ctx context.Context, query string) ([]MemoryEntry, error) {
	hms.mu.RLock()
	defer hms.mu.RUnlock()

	var results []MemoryEntry

	// Search in order of recency
	if working, err := hms.Working.Get(ctx, 10); err == nil {
		results = append(results, working...)
	}

	if shortTerm, err := hms.ShortTerm.Get(ctx, 20); err == nil {
		results = append(results, shortTerm...)
	}

	if longTerm, err := hms.LongTerm.Search(ctx, query); err == nil {
		results = append(results, longTerm...)
	}

	return results, nil
}

// Helper function to extract keywords
func extractKeywords(content string) []string {
	// Simple keyword extraction (in production, use NLP)
	keywords := make([]string, 0)
	words := make(map[string]bool)

	// Split and deduplicate
	for _, word := range splitWords(content) {
		if len(word) > 3 && !words[word] {
			words[word] = true
			keywords = append(keywords, word)
		}
	}

	return keywords
}

// Helper function to split words
func splitWords(content string) []string {
	// Simple split (in production, use proper tokenizer)
	words := make([]string, 0)
	current := ""

	for _, ch := range content {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			current += string(ch)
		} else {
			if current != "" {
				words = append(words, current)
				current = ""
			}
		}
	}

	if current != "" {
		words = append(words, current)
	}

	return words
}
