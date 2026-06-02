package llm

import (
	"context"
	"testing"
	"time"
)

func TestSensoryMemory_Add(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-sensory")
	sm := NewSensoryMemory(10, 5*time.Second, store, "test")

	entry := MemoryEntry{
		ID:      "entry1",
		Content: "test content",
	}

	err := sm.Add(context.Background(), entry)
	if err != nil {
		t.Fatalf("failed to add: %v", err)
	}

	entries, _ := sm.Get(context.Background(), 10)
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

func TestSensoryMemory_Expiration(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-sensory-exp")
	sm := NewSensoryMemory(10, 100*time.Millisecond, store, "test")

	entry := MemoryEntry{
		ID:      "entry1",
		Content: "test",
	}

	sm.Add(context.Background(), entry)

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	entries, _ := sm.Get(context.Background(), 10)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after expiration, got %d", len(entries))
	}
}

func TestWorkingMemory_Add(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-working")
	wm := NewWorkingMemory(10, store, "test")

	entry := MemoryEntry{
		ID:      "entry1",
		Content: "test",
	}

	err := wm.Add(context.Background(), entry)
	if err != nil {
		t.Fatalf("failed to add: %v", err)
	}

	entries, _ := wm.Get(context.Background(), 10)
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

func TestWorkingMemory_Access(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-working-access")
	wm := NewWorkingMemory(10, store, "test")

	entry := MemoryEntry{
		ID:      "entry1",
		Content: "test",
	}

	wm.Add(context.Background(), entry)
	wm.Access(context.Background(), "entry1")

	entries, _ := wm.Get(context.Background(), 10)
	if entries[0].AccessCount != 2 {
		t.Errorf("expected access count 2, got %d", entries[0].AccessCount)
	}
}

func TestShortTermMemory_Add(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-short-term")
	stm := NewShortTermMemory(10, 1*time.Hour, store, "test")

	entry := MemoryEntry{
		ID:      "entry1",
		Content: "test",
	}

	err := stm.Add(context.Background(), entry)
	if err != nil {
		t.Fatalf("failed to add: %v", err)
	}

	entries, _ := stm.Get(context.Background(), 10)
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

func TestLongTermMemory_Add(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-long-term")
	ltm := NewLongTermMemory(100, store, "test")

	entry := MemoryEntry{
		ID:         "entry1",
		Content:    "test content",
		Importance: 0.95,
	}

	err := ltm.Add(context.Background(), entry)
	if err != nil {
		t.Fatalf("failed to add: %v", err)
	}

	retrieved, _ := ltm.Get(context.Background(), "entry1")
	if retrieved.ID != "entry1" {
		t.Errorf("expected entry1, got %s", retrieved.ID)
	}
}

func TestLongTermMemory_Search(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-long-term-search")
	ltm := NewLongTermMemory(100, store, "test")

	entry := MemoryEntry{
		ID:         "entry1",
		Content:    "golang programming language",
		Importance: 0.95,
	}

	ltm.Add(context.Background(), entry)

	results, _ := ltm.Search(context.Background(), "golang")
	if len(results) == 0 {
		t.Error("expected search results")
	}
}

func TestEpisodicMemory_CreateEpisode(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-episodic")
	em := NewEpisodicMemory(store, "test")

	episode, err := em.CreateEpisode(context.Background(), "ep1", "First Episode")
	if err != nil {
		t.Fatalf("failed to create episode: %v", err)
	}

	if episode.Name != "First Episode" {
		t.Errorf("expected 'First Episode', got %s", episode.Name)
	}
}

func TestEpisodicMemory_AddEvent(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-episodic-event")
	em := NewEpisodicMemory(store, "test")

	em.CreateEpisode(context.Background(), "ep1", "Episode")

	event := MemoryEntry{
		ID:      "event1",
		Content: "Something happened",
	}

	err := em.AddEventToEpisode(context.Background(), "ep1", event)
	if err != nil {
		t.Fatalf("failed to add event: %v", err)
	}

	episode, _ := em.GetEpisode(context.Background(), "ep1")
	if len(episode.Events) != 1 {
		t.Errorf("expected 1 event, got %d", len(episode.Events))
	}
}

func TestSemanticMemory_AddFact(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-semantic")
	sm := NewSemanticMemory(store, "test")

	err := sm.AddFact(context.Background(), "fact1", "Go is a programming language")
	if err != nil {
		t.Fatalf("failed to add fact: %v", err)
	}
}

func TestSemanticMemory_AddConcept(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-semantic-concept")
	sm := NewSemanticMemory(store, "test")

	concept := &Concept{
		ID:         "concept1",
		Name:       "Programming",
		Definition: "The art of writing code",
		Category:   "Technology",
	}

	err := sm.AddConcept(context.Background(), concept)
	if err != nil {
		t.Fatalf("failed to add concept: %v", err)
	}

	retrieved, _ := sm.GetConcept(context.Background(), "concept1")
	if retrieved.Name != "Programming" {
		t.Errorf("expected 'Programming', got %s", retrieved.Name)
	}
}

func TestProceduralMemory_AddProcedure(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-procedural")
	pm := NewProceduralMemory(store, "test")

	procedure := &Procedure{
		ID:   "proc1",
		Name: "Write Code",
		Steps: []string{
			"Open editor",
			"Write code",
			"Test code",
		},
		Category:    "Development",
		Proficiency: 0.5,
	}

	err := pm.AddProcedure(context.Background(), procedure)
	if err != nil {
		t.Fatalf("failed to add procedure: %v", err)
	}

	retrieved, _ := pm.GetProcedure(context.Background(), "proc1")
	if retrieved.Name != "Write Code" {
		t.Errorf("expected 'Write Code', got %s", retrieved.Name)
	}
}

func TestProceduralMemory_Execute(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-procedural-exec")
	pm := NewProceduralMemory(store, "test")

	procedure := &Procedure{
		ID:          "proc1",
		Name:        "Test",
		Proficiency: 0.5,
	}

	pm.AddProcedure(context.Background(), procedure)
	pm.ExecuteProcedure(context.Background(), "proc1")

	retrieved, _ := pm.GetProcedure(context.Background(), "proc1")
	if retrieved.Proficiency <= 0.5 {
		t.Error("proficiency should increase after execution")
	}
}

func TestHierarchicalMemorySystem_AddMemory(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-hierarchical")
	hms := NewHierarchicalMemorySystem(store)

	entry := MemoryEntry{
		ID:         "entry1",
		Content:    "Important information",
		Importance: 0.95,
	}

	err := hms.AddMemory(context.Background(), entry)
	if err != nil {
		t.Fatalf("failed to add memory: %v", err)
	}
}

func TestHierarchicalMemorySystem_Recall(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-hierarchical-recall")
	hms := NewHierarchicalMemorySystem(store)

	entry := MemoryEntry{
		ID:         "entry1",
		Content:    "golang programming",
		Importance: 0.95,
	}

	hms.AddMemory(context.Background(), entry)

	results, err := hms.Recall(context.Background(), "golang")
	if err != nil {
		t.Fatalf("failed to recall: %v", err)
	}

	if len(results) == 0 {
		t.Error("expected recall results")
	}
}

func TestHierarchicalMemorySystem_MultiLevel(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-hierarchical-multi")
	hms := NewHierarchicalMemorySystem(store)

	// Add memory with different importance levels
	entries := []MemoryEntry{
		{ID: "entry1", Content: "Low importance", Importance: 0.3},
		{ID: "entry2", Content: "Medium importance", Importance: 0.6},
		{ID: "entry3", Content: "High importance", Importance: 0.8},
		{ID: "entry4", Content: "Critical importance", Importance: 0.95},
	}

	for _, entry := range entries {
		hms.AddMemory(context.Background(), entry)
	}

	// Verify distribution across memory levels
	working, _ := hms.working.Get(context.Background(), 100)
	shortTerm, _ := hms.shortTerm.Get(context.Background(), 100)

	if len(working) == 0 {
		t.Error("expected entries in working memory")
	}

	if len(shortTerm) == 0 {
		t.Error("expected entries in short-term memory")
	}
}

func TestMemoryDecay(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-decay")
	sm := NewSensoryMemory(10, 100*time.Millisecond, store, "test")

	entry := MemoryEntry{
		ID:      "entry1",
		Content: "test",
		Decay:   0.9,
	}

	sm.Add(context.Background(), entry)

	// Verify decay is tracked
	entries, _ := sm.Get(context.Background(), 10)
	if entries[0].Decay != 0.9 {
		t.Errorf("expected decay 0.9, got %f", entries[0].Decay)
	}
}

func TestMemoryAssociations(t *testing.T) {
	store, _ := NewFileSystemStore("/tmp/test-associations")
	ltm := NewLongTermMemory(100, store, "test")

	entry := MemoryEntry{
		ID:            "entry1",
		Content:       "test",
		Importance:   0.95,
		Associations: []string{"entry2", "entry3"},
	}

	ltm.Add(context.Background(), entry)

	retrieved, _ := ltm.Get(context.Background(), "entry1")
	if len(retrieved.Associations) != 2 {
		t.Errorf("expected 2 associations, got %d", len(retrieved.Associations))
	}
}
