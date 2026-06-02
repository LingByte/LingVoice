package adk

import (
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// AsyncIterator streams items until closed (Eino adk.AsyncIterator subset).
type AsyncIterator[T any] struct {
	ch     chan T
	closed bool
	mu     sync.Mutex
}

// NewAsyncIterator creates a buffered iterator.
func NewAsyncIterator[T any](buf int) *AsyncIterator[T] {
	if buf < 0 {
		buf = 0
	}
	return &AsyncIterator[T]{ch: make(chan T, buf)}
}

// Send publishes one item; no-op after Close.
func (it *AsyncIterator[T]) Send(v T) {
	if it == nil {
		return
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	if it.closed {
		return
	}
	it.ch <- v
}

// Close finishes the iterator.
func (it *AsyncIterator[T]) Close() {
	if it == nil {
		return
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	if !it.closed {
		it.closed = true
		close(it.ch)
	}
}

// Next returns the next item or false when done.
func (it *AsyncIterator[T]) Next() (T, bool) {
	var zero T
	if it == nil {
		return zero, false
	}
	v, ok := <-it.ch
	return v, ok
}

// Collect drains the iterator into a slice.
func (it *AsyncIterator[T]) Collect() []T {
	if it == nil {
		return nil
	}
	var out []T
	for {
		v, ok := it.Next()
		if !ok {
			return out
		}
		out = append(out, v)
	}
}

// FinalMessage returns the last message event from a run iterator.
func FinalMessage(it *AsyncIterator[*AgentEvent]) *schema.Message {
	if it == nil {
		return nil
	}
	var last *schema.Message
	for {
		ev, ok := it.Next()
		if !ok {
			return last
		}
		if ev != nil && ev.Message != nil {
			last = ev.Message
		}
	}
}
