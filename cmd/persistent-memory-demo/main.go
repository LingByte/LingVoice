package main

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"fmt"
	"log"

	"github.com/LingByte/LingVoice/pkg/llm"
)

func main() {
	fmt.Println("=== LingVoice Persistent Memory System Demo ===\n")

	// Example 1: File System Storage
	fmt.Println("Example 1: File System Storage")
	fmt.Println("------------------------------")
	fileSystemStorageDemo()

	fmt.Println("\n")

	// Example 2: Persistent Memory
	fmt.Println("Example 2: Persistent Memory")
	fmt.Println("----------------------------")
	persistentMemoryDemo()

	fmt.Println("\n")

	// Example 3: Memory Profiles
	fmt.Println("Example 3: Memory Profiles")
	fmt.Println("-------------------------")
	memoryProfilesDemo()

	fmt.Println("\n")

	// Example 4: Knowledge Base
	fmt.Println("Example 4: Knowledge Base")
	fmt.Println("------------------------")
	knowledgeBaseDemo()

	fmt.Println("\n")

	// Example 5: Conversation History
	fmt.Println("Example 5: Conversation History")
	fmt.Println("-------------------------------")
	conversationHistoryDemo()

	fmt.Println("\n✅ All demos completed successfully!")
}

// fileSystemStorageDemo demonstrates file system storage
func fileSystemStorageDemo() {
	store, err := llm.NewFileSystemStore("/tmp/lingvoice-storage")
	if err != nil {
		log.Fatalf("failed to create store: %v", err)
	}

	// Save data
	data := map[string]interface{}{
		"user_id": "user123",
		"name":    "Alice",
		"email":   "alice@example.com",
	}

	err = store.Save(context.Background(), "users/user123/profile", data)
	if err != nil {
		log.Fatalf("failed to save: %v", err)
	}

	fmt.Println("✓ Saved user profile to persistent storage")

	// Load data
	var loaded map[string]interface{}
	err = store.Load(context.Background(), "users/user123/profile", &loaded)
	if err != nil {
		log.Fatalf("failed to load: %v", err)
	}

	fmt.Printf("✓ Loaded user profile: %v\n", loaded)

	// List keys
	keys, err := store.List(context.Background())
	if err != nil {
		log.Fatalf("failed to list: %v", err)
	}

	fmt.Printf("✓ Total keys in storage: %d\n", len(keys))
}

// persistentMemoryDemo demonstrates persistent memory
func persistentMemoryDemo() {
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-memory")

	// Create persistent memory
	memory := llm.NewPersistentMemory(10, store, "conversation_1")

	// Add messages
	messages := []llm.Message{
		{Role: "user", Content: "Hello, what is your name?"},
		{Role: "assistant", Content: "I'm an AI assistant. How can I help?"},
		{Role: "user", Content: "Tell me about Go programming"},
		{Role: "assistant", Content: "Go is a statically typed, compiled language..."},
	}

	for _, msg := range messages {
		err := memory.AddMessage(context.Background(), msg)
		if err != nil {
			log.Fatalf("failed to add message: %v", err)
		}
	}

	fmt.Printf("✓ Added %d messages to persistent memory\n", len(messages))

	// Get conversation context
	context := memory.GetContext()
	fmt.Printf("✓ Conversation context:\n%s\n", context)

	// Verify persistence by creating new memory instance
	memory2 := llm.NewPersistentMemory(10, store, "conversation_1")
	msgs, _ := memory2.GetMessages(context.Background(), 10)
	fmt.Printf("✓ Verified persistence: loaded %d messages from storage\n", len(msgs))
}

// memoryProfilesDemo demonstrates user memory profiles
func memoryProfilesDemo() {
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-profiles")
	manager := llm.NewMemoryProfileManager(store)

	// Create profiles
	users := []string{"alice", "bob", "charlie"}

	for _, userID := range users {
		profile, err := manager.CreateProfile(context.Background(), userID)
		if err != nil {
			log.Fatalf("failed to create profile: %v", err)
		}

		// Set preferences
		profile.Preferences["language"] = "en"
		profile.Preferences["theme"] = "dark"
		profile.Metadata["signup_date"] = "2026-05-30"

		// Update profile
		err = manager.UpdateProfile(context.Background(), profile)
		if err != nil {
			log.Fatalf("failed to update profile: %v", err)
		}

		fmt.Printf("✓ Created and updated profile for user: %s\n", userID)
	}

	// Retrieve a profile
	profile, err := manager.GetProfile(context.Background(), "alice")
	if err != nil {
		log.Fatalf("failed to get profile: %v", err)
	}

	fmt.Printf("✓ Retrieved profile for alice: language=%v, theme=%v\n",
		profile.Preferences["language"],
		profile.Preferences["theme"])
}

// knowledgeBaseDemo demonstrates knowledge base management
func knowledgeBaseDemo() {
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-kb")
	manager := llm.NewKnowledgeBaseManager(store)

	// Create knowledge base
	kb, err := manager.CreateKnowledgeBase(context.Background(), "programming", "Programming Knowledge Base")
	if err != nil {
		log.Fatalf("failed to create KB: %v", err)
	}

	fmt.Printf("✓ Created knowledge base: %s\n", kb.Name)

	// Add documents
	documents := []llm.Document{
		{
			ID:      "go-intro",
			Content: "Go is a statically typed, compiled programming language designed for simplicity and efficiency.",
		},
		{
			ID:      "python-intro",
			Content: "Python is a high-level, interpreted programming language known for its readability.",
		},
		{
			ID:      "rust-intro",
			Content: "Rust is a systems programming language that runs blazingly fast and prevents segfaults.",
		},
	}

	for _, doc := range documents {
		err := manager.AddDocument(context.Background(), "programming", doc)
		if err != nil {
			log.Fatalf("failed to add document: %v", err)
		}
	}

	fmt.Printf("✓ Added %d documents to knowledge base\n", len(documents))

	// Retrieve knowledge base
	retrieved, err := manager.GetKnowledgeBase(context.Background(), "programming")
	if err != nil {
		log.Fatalf("failed to get KB: %v", err)
	}

	fmt.Printf("✓ Retrieved knowledge base with %d documents\n", len(retrieved.Documents))

	// List documents
	for id, doc := range retrieved.Documents {
		fmt.Printf("  - %s: %s\n", id, doc.Content[:50]+"...")
	}
}

// conversationHistoryDemo demonstrates conversation history management
func conversationHistoryDemo() {
	store, _ := llm.NewFileSystemStore("/tmp/lingvoice-conversations")
	manager := llm.NewConversationManager(store)

	// Create conversations for different users
	conversations := map[string][]llm.Message{
		"conv_alice_1": {
			{Role: "user", Content: "What is machine learning?"},
			{Role: "assistant", Content: "Machine learning is a subset of AI..."},
		},
		"conv_alice_2": {
			{Role: "user", Content: "How does neural networks work?"},
			{Role: "assistant", Content: "Neural networks are inspired by biological neurons..."},
		},
		"conv_bob_1": {
			{Role: "user", Content: "Tell me about cloud computing"},
			{Role: "assistant", Content: "Cloud computing is the delivery of computing services..."},
		},
	}

	// Create conversations and add messages
	for convID, messages := range conversations {
		userID := "alice"
		if convID == "conv_bob_1" {
			userID = "bob"
		}

		_, err := manager.CreateConversation(context.Background(), convID, userID)
		if err != nil {
			log.Fatalf("failed to create conversation: %v", err)
		}

		for _, msg := range messages {
			err := manager.AddMessageToConversation(context.Background(), convID, msg)
			if err != nil {
				log.Fatalf("failed to add message: %v", err)
			}
		}

		fmt.Printf("✓ Created conversation %s with %d messages\n", convID, len(messages))
	}

	// List conversations for alice
	aliceConvs, err := manager.ListConversations(context.Background(), "alice")
	if err != nil {
		log.Fatalf("failed to list conversations: %v", err)
	}

	fmt.Printf("✓ Alice has %d conversations\n", len(aliceConvs))

	// Retrieve a specific conversation
	conv, err := manager.GetConversation(context.Background(), "conv_alice_1")
	if err != nil {
		log.Fatalf("failed to get conversation: %v", err)
	}

	fmt.Printf("✓ Retrieved conversation with %d messages:\n", len(conv.Messages))
	for _, msg := range conv.Messages {
		fmt.Printf("  - %s: %s\n", msg.Role, msg.Content)
	}
}
