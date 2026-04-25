package utils

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"context"
	"sync"

	"github.com/LingByte/LingVoice/pkg/task"
)

// SignalHandler is invoked synchronously for each matching subscription (see Emit),
// or as part of an async dispatch (see EmitAsync).
type SignalHandler func(sender any, params ...any)

type SigHandler struct {
	ID      uint
	Handler SignalHandler
}

const (
	evTypeAdd = iota
	evTypeDel
)

type SigHandlerEvent struct {
	EvType     int
	SignalName string
	SigHandler SigHandler
}

// Signals is a pub/sub registry (observer). Emit runs handlers on the caller goroutine;
// EmitAsync runs a snapshot of handlers on a shared worker pool.
type Signals struct {
	mu          sync.Mutex
	lastID      uint
	sigHandlers map[string][]SigHandler
	inLoop      bool
	events      []SigHandlerEvent

	asyncMu     sync.Mutex
	asyncPool   *task.TaskPool[asyncEmitPayload, struct{}]
	asyncPoolFn func() *task.TaskPool[asyncEmitPayload, struct{}]
}

type asyncEmitPayload struct {
	event    string
	sender   any
	params   []any
	handlers []SigHandler
}

var sig *Signals

func init() {
	Sig()
}

func Sig() *Signals {
	if sig == nil {
		sig = NewSignals()
	}
	return sig
}

func NewSignals() *Signals {
	return &Signals{
		lastID:      0,
		sigHandlers: map[string][]SigHandler{},
		inLoop:      false,
		events:      []SigHandlerEvent{},
	}
}

// SetAsyncPool replaces the default lazy pool factory used by EmitAsync.
// Pass nil to clear and fall back to the built-in default on next EmitAsync.
func (s *Signals) SetAsyncPoolFactory(fn func() *task.TaskPool[asyncEmitPayload, struct{}]) {
	s.asyncMu.Lock()
	defer s.asyncMu.Unlock()
	s.asyncPoolFn = fn
	s.asyncPool = nil
}

func (s *Signals) defaultAsyncPool() *task.TaskPool[asyncEmitPayload, struct{}] {
	return task.NewTaskPool[asyncEmitPayload, struct{}](&task.PoolOption{
		WorkerCount: 4,
		QueueSize:   1024,
	})
}

func (s *Signals) poolForAsync() *task.TaskPool[asyncEmitPayload, struct{}] {
	s.asyncMu.Lock()
	defer s.asyncMu.Unlock()
	if s.asyncPool != nil {
		return s.asyncPool
	}
	if s.asyncPoolFn != nil {
		s.asyncPool = s.asyncPoolFn()
	} else {
		s.asyncPool = s.defaultAsyncPool()
	}
	return s.asyncPool
}

func (s *Signals) processEventsLocked() {
	if len(s.events) <= 0 || s.inLoop {
		return
	}
	for _, v := range s.events {
		sigs, ok := s.sigHandlers[v.SignalName]
		if !ok {
			sigs = make([]SigHandler, 0)
		}
		switch v.EvType {
		case evTypeAdd:
			sigs = append(sigs, v.SigHandler)
		case evTypeDel:
			for i := 0; i < len(sigs); i++ {
				if sigs[i].ID == v.SigHandler.ID {
					sigs = append(sigs[0:i], sigs[i+1:]...)
					break
				}
			}
		}
		s.sigHandlers[v.SignalName] = sigs
	}
	s.events = nil
}

func (s *Signals) snapshotHandlers(event string) []SigHandler {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.sigHandlers[event]
	if !ok || len(h) == 0 {
		return nil
	}
	out := make([]SigHandler, len(h))
	copy(out, h)
	return out
}

func (s *Signals) Connect(event string, handler SignalHandler) uint {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastID++
	ev := SigHandlerEvent{
		EvType:     evTypeAdd,
		SignalName: event,
		SigHandler: SigHandler{
			ID:      s.lastID,
			Handler: handler,
		},
	}
	s.events = append(s.events, ev)
	s.processEventsLocked()
	return s.lastID
}

func (s *Signals) Disconnect(event string, id uint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev := SigHandlerEvent{
		EvType:     evTypeDel,
		SignalName: event,
		SigHandler: SigHandler{
			ID: id,
		},
	}
	s.events = append(s.events, ev)
	s.processEventsLocked()
}

func (s *Signals) Clear(events ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range events {
		delete(s.sigHandlers, event)
	}
}

func (s *Signals) Emit(event string, sender any, params ...any) {
	s.mu.Lock()
	s.inLoop = true
	sigs := append([]SigHandler(nil), s.sigHandlers[event]...)
	s.mu.Unlock()

	for _, sh := range sigs {
		sh.Handler(sender, params...)
	}

	s.mu.Lock()
	s.inLoop = false
	s.processEventsLocked()
	s.mu.Unlock()
}

// EmitAsync schedules a snapshot of current handlers for event on the async worker pool.
// Handler order matches Emit; subscriptions added after this call are not included.
func (s *Signals) EmitAsync(ctx context.Context, event string, sender any, params ...any) (*task.Task[asyncEmitPayload, struct{}], error) {
	if ctx == nil {
		ctx = context.Background()
	}
	handlers := s.snapshotHandlers(event)
	if len(handlers) == 0 {
		return nil, nil
	}
	p := asyncEmitPayload{
		event:    event,
		sender:   sender,
		params:   append([]any(nil), params...),
		handlers: handlers,
	}
	return s.poolForAsync().AddTask(ctx, p, func(ctx context.Context, payload asyncEmitPayload) (struct{}, error) {
		var out struct{}
		for _, sh := range payload.handlers {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			sh.Handler(payload.sender, payload.params...)
		}
		return out, nil
	})
}
