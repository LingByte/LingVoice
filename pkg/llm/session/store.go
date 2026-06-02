// Package session provides multi-turn LLM conversation state (orchestration layer, not voice).
package session

import (
	"context"
	"fmt"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// PendingInterrupt records an in-flight HITL pause for session resume.
type PendingInterrupt struct {
	CheckPointID string `json:"checkpoint_id"`
	Info         any    `json:"info,omitempty"`
}

// Conversation holds accumulated messages and template variables.
type Conversation struct {
	ID       string            `json:"id"`
	Messages []*schema.Message `json:"messages"`
	Vars     map[string]any    `json:"vars,omitempty"`
	Pending  *PendingInterrupt `json:"pending,omitempty"`
}

// ClearPending removes a stored interrupt after successful resume.
func (c *Conversation) ClearPending() {
	if c != nil {
		c.Pending = nil
	}
}

// SetPending stores interrupt metadata for a later Resume call.
func (c *Conversation) SetPending(cpID string, info any) {
	if c == nil {
		return
	}
	c.Pending = &PendingInterrupt{CheckPointID: cpID, Info: info}
}

// HasPending reports whether the conversation awaits human input.
func (c *Conversation) HasPending() bool {
	return c != nil && c.Pending != nil && c.Pending.CheckPointID != ""
}

// Clone returns a shallow copy.
func (c *Conversation) Clone() *Conversation {
	if c == nil {
		return nil
	}
	out := &Conversation{
		ID:       c.ID,
		Messages: append([]*schema.Message(nil), c.Messages...),
		Vars:     map[string]any{},
		Pending:  c.Pending,
	}
	for k, v := range c.Vars {
		out.Vars[k] = v
	}
	return out
}

// AppendUser adds a user turn.
func (c *Conversation) AppendUser(text string) {
	if c == nil {
		return
	}
	c.Messages = append(c.Messages, schema.UserMessage(text))
}

// AppendAssistant adds an assistant message.
func (c *Conversation) AppendAssistant(msg *schema.Message) {
	if c == nil || msg == nil {
		return
	}
	c.Messages = append(c.Messages, msg)
}

// Store persists conversations.
type Store interface {
	Get(ctx context.Context, id string) (*Conversation, bool, error)
	Save(ctx context.Context, conv *Conversation) error
}

// MemoryStore is an in-memory conversation store.
type MemoryStore struct {
	mu sync.RWMutex
	m  map[string]*Conversation
}

// NewMemoryStore creates an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{m: make(map[string]*Conversation)}
}

func (s *MemoryStore) Get(_ context.Context, id string) (*Conversation, bool, error) {
	if s == nil || id == "" {
		return nil, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.m[id]
	if !ok {
		return nil, false, nil
	}
	return c.Clone(), true, nil
}

func (s *MemoryStore) Save(_ context.Context, conv *Conversation) error {
	if s == nil {
		return fmt.Errorf("session: nil store")
	}
	if conv == nil || conv.ID == "" {
		return fmt.Errorf("session: conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[conv.ID] = conv.Clone()
	return nil
}

// GetOrCreate loads or initializes a conversation.
func (s *MemoryStore) GetOrCreate(_ context.Context, id string) *Conversation {
	if s == nil {
		return &Conversation{ID: id, Vars: map[string]any{}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.m[id]; ok {
		return c.Clone()
	}
	c := &Conversation{ID: id, Vars: map[string]any{}}
	s.m[id] = c
	return c.Clone()
}
