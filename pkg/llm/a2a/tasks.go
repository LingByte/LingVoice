package a2a

import (
	"fmt"
	"sync"
)

type taskManager struct {
	mu       sync.RWMutex
	tasks    map[string]*Task
	push     map[string]PushNotificationConfig
	onUpdate func(task *Task, prev *Task, hadPrev bool)
}

func newTaskManager() *taskManager {
	return &taskManager{
		tasks: map[string]*Task{},
		push:  map[string]PushNotificationConfig{},
	}
}

func (m *taskManager) put(task *Task) {
	if m == nil || task == nil || task.ID == "" {
		return
	}
	m.mu.Lock()
	prev, hadPrev := m.snapshotLocked(task.ID)
	copy := *task
	m.tasks[task.ID] = &copy
	onUpdate := m.onUpdate
	m.mu.Unlock()
	if onUpdate != nil {
		onUpdate(&copy, prev, hadPrev)
	}
}

func (m *taskManager) snapshotLocked(id string) (*Task, bool) {
	t, ok := m.tasks[id]
	if !ok {
		return nil, false
	}
	copy := *t
	return &copy, true
}

func (m *taskManager) get(id string) (*Task, bool) {
	if m == nil || id == "" {
		return nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tasks[id]
	if !ok {
		return nil, false
	}
	copy := *t
	return &copy, true
}

func (m *taskManager) cancel(id string) (*Task, error) {
	if m == nil || id == "" {
		return nil, fmt.Errorf("task not found")
	}
	m.mu.Lock()
	t, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("task not found")
	}
	if t.Status.State.Terminal() {
		m.mu.Unlock()
		return nil, fmt.Errorf("task not cancelable")
	}
	prev := *t
	t.Status = TaskStatus{State: TaskCanceled, Timestamp: nowRFC3339()}
	copy := *t
	m.tasks[id] = t
	onUpdate := m.onUpdate
	m.mu.Unlock()
	if onUpdate != nil {
		onUpdate(&copy, &prev, true)
	}
	return &copy, nil
}

func (m *taskManager) list(contextID string, limit int) []Task {
	if m == nil {
		return nil
	}
	if limit <= 0 {
		limit = 50
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		if t == nil {
			continue
		}
		if contextID != "" && t.ContextID != contextID {
			continue
		}
		copy := *t
		out = append(out, copy)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (m *taskManager) setPush(cfg PushNotificationConfig) error {
	if m == nil || cfg.TaskID == "" || cfg.URL == "" {
		return fmt.Errorf("invalid push config")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[cfg.TaskID]; !ok {
		return fmt.Errorf("task not found")
	}
	cfg.TaskID = cfg.TaskID
	m.push[cfg.TaskID] = cfg
	return nil
}

func (m *taskManager) getPush(taskID string) (PushNotificationConfig, bool) {
	if m == nil || taskID == "" {
		return PushNotificationConfig{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg, ok := m.push[taskID]
	return cfg, ok
}

func (m *taskManager) listPush(taskID string) []PushNotificationConfig {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if taskID != "" {
		if cfg, ok := m.push[taskID]; ok {
			return []PushNotificationConfig{cfg}
		}
		return nil
	}
	out := make([]PushNotificationConfig, 0, len(m.push))
	for _, cfg := range m.push {
		out = append(out, cfg)
	}
	return out
}

func (m *taskManager) deletePush(taskID string) error {
	if m == nil || taskID == "" {
		return fmt.Errorf("task not found")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.push[taskID]; !ok {
		return fmt.Errorf("push config not found")
	}
	delete(m.push, taskID)
	return nil
}

func (m *taskManager) legacyStatus(id string) (LegacyTaskStatus, bool) {
	t, ok := m.get(id)
	if !ok {
		return LegacyTaskStatus{}, false
	}
	st := LegacyTaskStatus{TaskID: id, Status: string(t.Status.State)}
	if t.Status.Message != nil {
		st.Error = textFromParts(t.Status.Message.Parts)
	}
	return st, true
}

func blockingFromConfig(cfg *MessageSendConfiguration) bool {
	if cfg == nil || cfg.Blocking == nil {
		return true
	}
	return *cfg.Blocking
}

func prepareUserMessage(msg A2AMessage) A2AMessage {
	if msg.Kind == "" {
		msg.Kind = "message"
	}
	if msg.MessageID == "" {
		msg.MessageID = newID()
	}
	if msg.Role == "" {
		msg.Role = "user"
	}
	return msg
}

func contextIDFrom(params MessageSendParams) string {
	if id := params.Message.ContextID; id != "" {
		return id
	}
	return newID()
}

func taskIDFrom(params MessageSendParams) string {
	if id := params.Message.TaskID; id != "" {
		return id
	}
	return newID()
}
