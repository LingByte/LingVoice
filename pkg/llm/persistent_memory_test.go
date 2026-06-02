package llm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSystemStore_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewFileSystemStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	data := map[string]interface{}{"key": "value"}
	err = store.Save(context.Background(), "test", data)
	if err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	var loaded map[string]interface{}
	err = store.Load(context.Background(), "test", &loaded)
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	if loaded["key"] != "value" {
		t.Errorf("expected 'value', got %v", loaded["key"])
	}
}

func TestFileSystemStore_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewFileSystemStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	data := map[string]interface{}{"key": "value"}
	store.Save(context.Background(), "test", data)

	err = store.Delete(context.Background(), "test")
	if err != nil {
		t.Fatalf("failed to delete: %v", err)
	}

	var loaded map[string]interface{}
	err = store.Load(context.Background(), "test", &loaded)
	if err == nil {
		t.Fatal("expected error for deleted key")
	}
}

func TestFileSystemStore_List(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewFileSystemStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	store.Save(context.Background(), "key1", map[string]interface{}{})
	store.Save(context.Background(), "key2", map[string]interface{}{})
	store.Save(context.Background(), "key3", map[string]interface{}{})

	keys, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}

	if len(keys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(keys))
	}
}

func TestFileSystemStore_Clear(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewFileSystemStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	store.Save(context.Background(), "key1", map[string]interface{}{})
	store.Save(context.Background(), "key2", map[string]interface{}{})

	err = store.Clear(context.Background())
	if err != nil {
		t.Fatalf("failed to clear: %v", err)
	}

	keys, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}

	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}

func TestPersistentMemory_AddMessage(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewFileSystemStore(tmpDir)
	memory := NewPersistentMemory(10, store, "test_memory")

	msg := Message{
		Role:    "user",
		Content: "Hello",
	}

	err := memory.AddMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("failed to add message: %v", err)
	}

	messages, err := memory.GetMessages(context.Background(), 10)
	if err != nil {
		t.Fatalf("failed to get messages: %v", err)
	}

	if len(messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(messages))
	}
}

func TestPersistentMemory_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewFileSystemStore(tmpDir)

	// Create memory and add message
	memory1 := NewPersistentMemory(10, store, "test_memory")
	memory1.AddMessage(context.Background(), Message{
		Role:    "user",
		Content: "Hello",
	})

	// Create new memory instance and verify message persists
	memory2 := NewPersistentMemory(10, store, "test_memory")
	messages, err := memory2.GetMessages(context.Background(), 10)
	if err != nil {
		t.Fatalf("failed to get messages: %v", err)
	}

	if len(messages) != 1 {
		t.Errorf("expected 1 persisted message, got %d", len(messages))
	}
}

func TestMemoryProfileManager_CreateAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewFileSystemStore(tmpDir)
	manager := NewMemoryProfileManager(store)

	profile, err := manager.CreateProfile(context.Background(), "user1")
	if err != nil {
		t.Fatalf("failed to create profile: %v", err)
	}

	retrieved, err := manager.GetProfile(context.Background(), "user1")
	if err != nil {
		t.Fatalf("failed to get profile: %v", err)
	}

	if retrieved.UserID != profile.UserID {
		t.Errorf("expected user ID %s, got %s", profile.UserID, retrieved.UserID)
	}
}

func TestMemoryProfileManager_Update(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewFileSystemStore(tmpDir)
	manager := NewMemoryProfileManager(store)

	profile, _ := manager.CreateProfile(context.Background(), "user1")
	profile.Preferences["language"] = "en"

	err := manager.UpdateProfile(context.Background(), profile)
	if err != nil {
		t.Fatalf("failed to update profile: %v", err)
	}

	retrieved, _ := manager.GetProfile(context.Background(), "user1")
	if retrieved.Preferences["language"] != "en" {
		t.Error("preference not updated")
	}
}

func TestMemoryProfileManager_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewFileSystemStore(tmpDir)
	manager := NewMemoryProfileManager(store)

	manager.CreateProfile(context.Background(), "user1")
	err := manager.DeleteProfile(context.Background(), "user1")
	if err != nil {
		t.Fatalf("failed to delete profile: %v", err)
	}

	_, err = manager.GetProfile(context.Background(), "user1")
	if err == nil {
		t.Fatal("expected error for deleted profile")
	}
}

func TestKnowledgeBaseManager_CreateAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewFileSystemStore(tmpDir)
	manager := NewKnowledgeBaseManager(store)

	kb, err := manager.CreateKnowledgeBase(context.Background(), "kb1", "My Knowledge Base")
	if err != nil {
		t.Fatalf("failed to create knowledge base: %v", err)
	}

	retrieved, err := manager.GetKnowledgeBase(context.Background(), "kb1")
	if err != nil {
		t.Fatalf("failed to get knowledge base: %v", err)
	}

	if retrieved.ID != kb.ID {
		t.Errorf("expected ID %s, got %s", kb.ID, retrieved.ID)
	}
}

func TestKnowledgeBaseManager_AddDocument(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewFileSystemStore(tmpDir)
	manager := NewKnowledgeBaseManager(store)

	manager.CreateKnowledgeBase(context.Background(), "kb1", "My KB")

	doc := Document{
		ID:      "doc1",
		Content: "Test content",
	}

	err := manager.AddDocument(context.Background(), "kb1", doc)
	if err != nil {
		t.Fatalf("failed to add document: %v", err)
	}

	kb, _ := manager.GetKnowledgeBase(context.Background(), "kb1")
	if len(kb.Documents) != 1 {
		t.Errorf("expected 1 document, got %d", len(kb.Documents))
	}
}

func TestConversationManager_CreateAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewFileSystemStore(tmpDir)
	manager := NewConversationManager(store)

	conv, err := manager.CreateConversation(context.Background(), "conv1", "user1")
	if err != nil {
		t.Fatalf("failed to create conversation: %v", err)
	}

	retrieved, err := manager.GetConversation(context.Background(), "conv1")
	if err != nil {
		t.Fatalf("failed to get conversation: %v", err)
	}

	if retrieved.ID != conv.ID {
		t.Errorf("expected ID %s, got %s", conv.ID, retrieved.ID)
	}
}

func TestConversationManager_AddMessage(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewFileSystemStore(tmpDir)
	manager := NewConversationManager(store)

	manager.CreateConversation(context.Background(), "conv1", "user1")

	msg := Message{
		Role:    "user",
		Content: "Hello",
	}

	err := manager.AddMessageToConversation(context.Background(), "conv1", msg)
	if err != nil {
		t.Fatalf("failed to add message: %v", err)
	}

	conv, _ := manager.GetConversation(context.Background(), "conv1")
	if len(conv.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(conv.Messages))
	}
}

func TestConversationManager_ListConversations(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewFileSystemStore(tmpDir)
	manager := NewConversationManager(store)

	manager.CreateConversation(context.Background(), "conv1", "user1")
	manager.CreateConversation(context.Background(), "conv2", "user1")
	manager.CreateConversation(context.Background(), "conv3", "user2")

	convs, err := manager.ListConversations(context.Background(), "user1")
	if err != nil {
		t.Fatalf("failed to list conversations: %v", err)
	}

	if len(convs) != 2 {
		t.Errorf("expected 2 conversations for user1, got %d", len(convs))
	}
}

func TestConversationManager_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewFileSystemStore(tmpDir)
	manager := NewConversationManager(store)

	manager.CreateConversation(context.Background(), "conv1", "user1")
	err := manager.DeleteConversation(context.Background(), "conv1")
	if err != nil {
		t.Fatalf("failed to delete conversation: %v", err)
	}

	_, err = manager.GetConversation(context.Background(), "conv1")
	if err == nil {
		t.Fatal("expected error for deleted conversation")
	}
}

func TestPersistentMemory_MaxSize(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewFileSystemStore(tmpDir)
	memory := NewPersistentMemory(3, store, "test_memory")

	for i := 0; i < 5; i++ {
		memory.AddMessage(context.Background(), Message{
			Role:    "user",
			Content: "Message " + string(rune(i)),
		})
	}

	messages, _ := memory.GetMessages(context.Background(), 10)
	if len(messages) > 3 {
		t.Errorf("expected at most 3 messages, got %d", len(messages))
	}
}

func TestFileSystemStore_NestedPaths(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewFileSystemStore(tmpDir)

	// Save with nested path
	data := map[string]interface{}{"key": "value"}
	err := store.Save(context.Background(), "users/user1/profile", data)
	if err != nil {
		t.Fatalf("failed to save with nested path: %v", err)
	}

	// Verify file was created
	filePath := filepath.Join(tmpDir, "users/user1/profile.json")
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("file not created at expected path: %v", err)
	}
}
