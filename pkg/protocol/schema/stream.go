package schema

import (
	"errors"
	"io"
	"sync"
)

// ErrRecvAfterClosed is returned when Recv is called after Close on a stream reader.
var ErrRecvAfterClosed = errors.New("schema: recv after stream closed")

// StreamReader receives typed chunks from a stream. Close the reader when done.
type StreamReader[T any] struct {
	ch     <-chan streamItem[T]
	closed bool
	mu     sync.Mutex
}

type streamItem[T any] struct {
	val T
	err error
}

// StreamWriter sends chunks to the paired StreamReader created by Pipe.
type StreamWriter[T any] struct {
	ch chan streamItem[T]
}

// Pipe creates a buffered stream pair.
func Pipe[T any](capacity int) (*StreamReader[T], *StreamWriter[T]) {
	if capacity <= 0 {
		capacity = 1
	}
	ch := make(chan streamItem[T], capacity)
	return &StreamReader[T]{ch: ch}, &StreamWriter[T]{ch: ch}
}

// Send enqueues one chunk. Returns false if the reader has closed.
func (w *StreamWriter[T]) Send(v T, err error) bool {
	if w == nil || w.ch == nil {
		return false
	}
	item := streamItem[T]{val: v, err: err}
	select {
	case w.ch <- item:
		return true
	default:
		// block when buffer full
		w.ch <- item
		return true
	}
}

// Close closes the writer side; Recv will eventually return io.EOF.
func (w *StreamWriter[T]) Close() {
	if w == nil || w.ch == nil {
		return
	}
	close(w.ch)
}

// Recv returns the next chunk. io.EOF means the stream ended normally.
func (r *StreamReader[T]) Recv() (T, error) {
	var zero T
	if r == nil {
		return zero, io.EOF
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return zero, ErrRecvAfterClosed
	}
	r.mu.Unlock()
	item, ok := <-r.ch
	if !ok {
		return zero, io.EOF
	}
	if item.err != nil {
		return item.val, item.err
	}
	return item.val, nil
}

// Close stops reading and releases the stream.
func (r *StreamReader[T]) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	for range r.ch {
	}
}

// Collect drains the stream and concatenates message chunks when T is *Message.
func CollectMessages(r *StreamReader[*Message]) (*Message, error) {
	if r == nil {
		return &Message{}, nil
	}
	defer r.Close()
	var chunks []*Message
	for {
		msg, err := r.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		chunks = append(chunks, msg)
	}
	return ConcatMessages(chunks)
}

// StreamReaderFromSlice exposes a static slice as a one-shot stream (testing).
func StreamReaderFromSlice[T any](items []T) *StreamReader[T] {
	sr, sw := Pipe[T](len(items))
	go func() {
		defer sw.Close()
		for _, it := range items {
			sw.Send(it, nil)
		}
	}()
	return sr
}
