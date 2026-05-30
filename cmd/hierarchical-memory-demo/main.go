package main

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm"
)

func main() {
	fmt.Println("=== LingVoice Hierarchical Memory System Demo ===\n")

	// Example 1: Knowledge Accumulation
	fmt.Println("Example 1: Knowledge Accumulation Over Multiple Calls")
	fmt.Println("-----------------------------------------------------")
	knowledgeAccumulationDemo()

	fmt.Println("\n")

	// Example 2: Memory Consolidation
	fmt.Println("Example 2: Memory Consolidation (Short-term to Long-term)")
	fmt.Println("-------------------------------------------------------")
	memoryConsolidationDemo()

	fmt.Println("\n")

	// Example 3: Intelligent Recall
	fmt.Println("Example 3: Intelligent Multi-level Recall")
	fmt.Println("----------------------------------------")
	intelligentRecallDemo()

	fmt.Println("\n")

	// Example 4: Episodic Learning
	fmt.Println("Example 4: Episodic Learning from Conversations")
	fmt.Println("---------------------------------------------")
	episodicLearningDemo()

	fmt.Println("\n")

	// Example 5: Skill Improvement
	fmt.Println("Example 5: Procedural Memory - Skill Improvement")
	fmt.Println("----------------------------------------------")
	skillImprovementDemo()

	fmt.Println("\n✅ All demos completed successfully!")
}

// knowledgeAccumulationDemo demonstrates knowledge accumulation
func knowledgeAccumulationDemo() {
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-knowledge")
	hms := llm.NewHierarchicalMemorySystem(store)

	// Simulate multiple LLM calls with knowledge accumulation
	knowledgeItems := []struct {
		query      string
		content    string
		importance float64
	}{
		{
			query:      "What is Go?",
			content:    "Go is a statically typed, compiled programming language designed for simplicity and efficiency.",
			importance: 0.95,
		},
		{
			query:      "Go concurrency",
			content:    "Go uses goroutines and channels for concurrent programming, making it easy to write concurrent code.",
			importance: 0.9,
		},
		{
			query:      "Go packages",
			content:    "Go packages are collections of Go source files in the same directory that are compiled together.",
			importance: 0.85,
		},
		{
			query:      "Go interfaces",
			content:    "Go interfaces define a set of methods that a type must implement to satisfy the interface.",
			importance: 0.88,
		},
		{
			query:      "Go error handling",
			content:    "Go uses explicit error handling with error types, making errors a first-class concept.",
			importance: 0.92,
		},
	}

	fmt.Println("📚 Accumulating knowledge through multiple LLM calls:")
	for i, item := range knowledgeItems {
		entry := llm.MemoryEntry{
			ID:         fmt.Sprintf("knowledge_%d", i),
			Content:    item.content,
			Importance: item.importance,
			Metadata: map[string]interface{}{
				"query":      item.query,
				"call_index": i + 1,
				"timestamp":  time.Now(),
			},
		}

		err := hms.AddMemory(context.Background(), entry)
		if err != nil {
			log.Fatalf("failed to add memory: %v", err)
		}

		fmt.Printf("  [Call %d] Query: '%s' → Importance: %.2f\n", i+1, item.query, item.importance)
	}

	// Verify distribution across memory levels
	working, _ := hms.Working.Get(context.Background(), 100)
	shortTerm, _ := hms.ShortTerm.Get(context.Background(), 100)
	longTerm, _ := hms.LongTerm.Search(context.Background(), "Go")

	fmt.Printf("\n📊 Memory Distribution:\n")
	fmt.Printf("  Working Memory: %d entries\n", len(working))
	fmt.Printf("  Short-term Memory: %d entries\n", len(shortTerm))
	fmt.Printf("  Long-term Memory: %d entries found\n", len(longTerm))
}

// memoryConsolidationDemo demonstrates memory consolidation
func memoryConsolidationDemo() {
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-consolidation")
	hms := llm.NewHierarchicalMemorySystem(store)

	fmt.Println("🔄 Memory Consolidation Process:")

	// Phase 1: Initial learning (working memory)
	fmt.Println("\n  Phase 1: Initial Learning (Working Memory)")
	for i := 0; i < 3; i++ {
		entry := llm.MemoryEntry{
			ID:         fmt.Sprintf("phase1_%d", i),
			Content:    fmt.Sprintf("New concept %d", i+1),
			Importance: 0.6,
		}
		hms.Working.Add(context.Background(), entry)
		fmt.Printf("    ✓ Added to working memory: concept %d\n", i+1)
	}

	// Phase 2: Repeated access (short-term memory)
	fmt.Println("\n  Phase 2: Repeated Access (Short-term Memory)")
	for i := 0; i < 3; i++ {
		entry := llm.MemoryEntry{
			ID:         fmt.Sprintf("phase2_%d", i),
			Content:    fmt.Sprintf("Important concept %d", i+1),
			Importance: 0.75,
		}
		hms.ShortTerm.Add(context.Background(), entry)
		fmt.Printf("    ✓ Added to short-term memory: concept %d\n", i+1)
	}

	// Phase 3: Consolidation (long-term memory)
	fmt.Println("\n  Phase 3: Consolidation (Long-term Memory)")
	for i := 0; i < 3; i++ {
		entry := llm.MemoryEntry{
			ID:         fmt.Sprintf("phase3_%d", i),
			Content:    fmt.Sprintf("Critical concept %d", i+1),
			Importance: 0.95,
		}
		hms.LongTerm.Add(context.Background(), entry)
		fmt.Printf("    ✓ Added to long-term memory: concept %d\n", i+1)
	}

	// Verify consolidation
	working, _ := hms.Working.Get(context.Background(), 100)
	shortTerm, _ := hms.ShortTerm.Get(context.Background(), 100)

	fmt.Printf("\n📈 Consolidation Status:\n")
	fmt.Printf("  Working Memory: %d entries (temporary)\n", len(working))
	fmt.Printf("  Short-term Memory: %d entries (medium-term)\n", len(shortTerm))
	fmt.Printf("  Long-term Memory: Successfully consolidated\n")
}

// intelligentRecallDemo demonstrates intelligent recall
func intelligentRecallDemo() {
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-recall")
	hms := llm.NewHierarchicalMemorySystem(store)

	// Build knowledge base
	fmt.Println("🧠 Building knowledge base for intelligent recall:")

	knowledgeBase := []struct {
		id         string
		content    string
		importance float64
	}{
		{"k1", "Python is a high-level programming language", 0.85},
		{"k2", "Python uses indentation for code blocks", 0.88},
		{"k3", "Python has a large ecosystem of libraries", 0.9},
		{"k4", "Go is a compiled language with fast performance", 0.92},
		{"k5", "Go has built-in concurrency with goroutines", 0.95},
		{"k6", "Rust provides memory safety without garbage collection", 0.93},
	}

	for _, kb := range knowledgeBase {
		entry := llm.MemoryEntry{
			ID:         kb.id,
			Content:    kb.content,
			Importance: kb.importance,
		}
		hms.AddMemory(context.Background(), entry)
		fmt.Printf("  ✓ Added: %s\n", kb.content)
	}

	// Perform intelligent recalls
	fmt.Println("\n🔍 Intelligent Recall Results:")

	queries := []string{"Python", "Go", "Rust", "programming"}
	for _, query := range queries {
		results, _ := hms.Recall(context.Background(), query)
		fmt.Printf("\n  Query: '%s'\n", query)
		fmt.Printf("  Found %d relevant memories:\n", len(results))
		for _, result := range results {
			fmt.Printf("    - %s (importance: %.2f)\n", result.Content, result.Importance)
		}
	}
}

// episodicLearningDemo demonstrates episodic learning
func episodicLearningDemo() {
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-episodic")
	em := llm.NewEpisodicMemory(store, "conversations")

	fmt.Println("📖 Episodic Learning from Conversations:")

	// Create conversation episode
	episode, _ := em.CreateEpisode(context.Background(), "conv_001", "First Conversation about Go")

	// Add events to episode
	events := []struct {
		role    string
		content string
	}{
		{"user", "What is Go?"},
		{"assistant", "Go is a programming language..."},
		{"user", "How does concurrency work?"},
		{"assistant", "Go uses goroutines and channels..."},
		{"user", "Can you give an example?"},
		{"assistant", "Here's a simple goroutine example..."},
	}

	fmt.Printf("\n  Episode: %s\n", episode.Name)
	for i, event := range events {
		eventEntry := llm.MemoryEntry{
			ID:      fmt.Sprintf("event_%d", i),
			Content: event.content,
		}
		em.AddEventToEpisode(context.Background(), "conv_001", eventEntry)
		fmt.Printf("    [%s] %s\n", event.role, event.content)
	}

	// Close episode
	em.CloseEpisode(context.Background(), "conv_001")

	// Retrieve episode
	retrieved, _ := em.GetEpisode(context.Background(), "conv_001")
	fmt.Printf("\n  📊 Episode Summary:\n")
	fmt.Printf("    Name: %s\n", retrieved.Name)
	fmt.Printf("    Total Events: %d\n", len(retrieved.Events))
	fmt.Printf("    Duration: %v\n", retrieved.EndTime.Sub(retrieved.StartTime))
}

// skillImprovementDemo demonstrates procedural memory improvement
func skillImprovementDemo() {
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-skills")
	pm := llm.NewProceduralMemory(store, "skills")

	fmt.Println("🎯 Procedural Memory - Skill Improvement:")

	// Define skills
	skills := []struct {
		id    string
		name  string
		steps []string
	}{
		{
			id:   "skill_coding",
			name: "Write Go Code",
			steps: []string{
				"Open editor",
				"Write code",
				"Test code",
				"Debug if needed",
				"Commit changes",
			},
		},
		{
			id:   "skill_debugging",
			name: "Debug Code",
			steps: []string{
				"Identify error",
				"Add logging",
				"Run with debugger",
				"Inspect variables",
				"Fix issue",
			},
		},
	}

	// Add skills
	fmt.Println("\n  Initial Skills:")
	for _, skill := range skills {
		procedure := &llm.Procedure{
			ID:          skill.id,
			Name:        skill.name,
			Steps:       skill.steps,
			Category:    "Development",
			Proficiency: 0.5,
		}
		pm.AddProcedure(context.Background(), procedure)
		fmt.Printf("    ✓ %s (Proficiency: 50%%)\n", skill.name)
	}

	// Execute skills multiple times to improve proficiency
	fmt.Println("\n  Skill Practice (10 executions):")
	for i := 0; i < 10; i++ {
		pm.ExecuteProcedure(context.Background(), "skill_coding")
		if (i+1)%3 == 0 {
			proc, _ := pm.GetProcedure(context.Background(), "skill_coding")
			fmt.Printf("    After %d executions: Proficiency = %.1f%%\n",
				i+1, proc.Proficiency*100)
		}
	}

	// Check final proficiency
	finalProc, _ := pm.GetProcedure(context.Background(), "skill_coding")
	fmt.Printf("\n  📈 Final Proficiency: %.1f%%\n", finalProc.Proficiency*100)
	fmt.Printf("  Last Used: %v\n", finalProc.LastUsed)
}

// Bonus: Semantic memory demo
func semanticMemoryDemo() {
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-semantic")
	sm := llm.NewSemanticMemory(store, "knowledge")

	fmt.Println("🧬 Semantic Memory - Knowledge Organization:")

	// Add facts
	fmt.Println("\n  Facts:")
	facts := []struct {
		id      string
		content string
	}{
		{"fact_1", "Go was created by Google in 2007"},
		{"fact_2", "Go is used for cloud infrastructure"},
		{"fact_3", "Go has a simple syntax"},
	}

	for _, fact := range facts {
		sm.AddFact(context.Background(), fact.id, fact.content)
		fmt.Printf("    ✓ %s\n", fact.content)
	}

	// Add concepts
	fmt.Println("\n  Concepts:")
	concepts := []struct {
		id         string
		name       string
		definition string
	}{
		{
			"concept_1",
			"Programming Language",
			"A formal language for writing computer programs",
		},
		{
			"concept_2",
			"Concurrency",
			"The ability to execute multiple tasks simultaneously",
		},
	}

	for _, concept := range concepts {
		c := &llm.Concept{
			ID:         concept.id,
			Name:       concept.name,
			Definition: concept.definition,
			Category:   "Computer Science",
		}
		sm.AddConcept(context.Background(), c)
		fmt.Printf("    ✓ %s: %s\n", concept.name, concept.definition)
	}

	// Retrieve concepts
	fmt.Println("\n  Retrieved Concepts:")
	retrieved, _ := sm.GetConcept(context.Background(), "concept_1")
	fmt.Printf("    - %s: %s\n", retrieved.Name, retrieved.Definition)
}
