package metrics

// EnqueueTool persists a tool run asynchronously.
func (s *MemoryStore) EnqueueTool(rec ToolRunRecord) {
	if s == nil {
		return
	}
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, exists := s.toolByID[rec.ID]; !exists {
			s.toolOrder = append(s.toolOrder, rec.ID)
		}
		s.toolByID[rec.ID] = rec
		for len(s.toolOrder) > s.maxLen {
			old := s.toolOrder[0]
			s.toolOrder = s.toolOrder[1:]
			delete(s.toolByID, old)
		}
		s.toolTotal++
	}()
}

// ListTools returns recent tool runs newest-first.
func (s *MemoryStore) ListTools(limit int) []ToolRunRecord {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := len(s.toolOrder)
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]ToolRunRecord, 0, limit)
	for i := n - 1; i >= 0 && len(out) < limit; i-- {
		if r, ok := s.toolByID[s.toolOrder[i]]; ok {
			out = append(out, r)
		}
	}
	return out
}

// ToolTotal returns count of recorded tool invocations.
func (s *MemoryStore) ToolTotal() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.toolTotal
}
