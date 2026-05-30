package compose

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

var (
	chunkConcatMu sync.RWMutex
	chunkConcat   = map[reflect.Type]func([]any) (any, error){}
)

func initStreamConcat() {
	var msg *schema.Message
	registerStreamChunkConcat(reflect.TypeOf(msg), func(items []any) (any, error) {
		msgs := make([]*schema.Message, len(items))
		for i, it := range items {
			m, ok := it.(*schema.Message)
			if !ok {
				return nil, fmt.Errorf("compose: message concat type mismatch at %d: %T", i, it)
			}
			msgs[i] = m
		}
		return schema.ConcatMessages(msgs)
	})
}

func registerStreamChunkConcat(t reflect.Type, fn func([]any) (any, error)) {
	chunkConcatMu.Lock()
	chunkConcat[t] = fn
	chunkConcatMu.Unlock()
}

// RegisterStreamChunkConcatFunc registers a stream chunk concat function for type T (Eino subset).
func RegisterStreamChunkConcatFunc[T any](fn func([]T) (T, error)) {
	if fn == nil {
		return
	}
	var zero T
	t := reflect.TypeOf(zero)
	registerStreamChunkConcat(t, func(items []any) (any, error) {
		typed := make([]T, len(items))
		for i, it := range items {
			v, ok := it.(T)
			if !ok {
				var z T
				return z, fmt.Errorf("compose: stream concat type mismatch at %d: want %v got %T", i, t, it)
			}
			typed[i] = v
		}
		return fn(typed)
	})
}

// ConcatStreamReader drains a stream and concatenates chunks using a registered concat func.
func ConcatStreamReader[T any](sr *schema.StreamReader[T]) (T, error) {
	var zero T
	if sr == nil {
		return zero, errors.New("compose: nil stream reader")
	}
	defer sr.Close()
	var items []any
	for {
		chunk, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return zero, err
		}
		items = append(items, chunk)
	}
	if len(items) == 0 {
		return zero, errors.New("compose: empty stream")
	}
	if len(items) == 1 {
		if v, ok := items[0].(T); ok {
			return v, nil
		}
		return zero, fmt.Errorf("compose: stream chunk type mismatch: %T", items[0])
	}
	t := reflect.TypeOf(zero)
	chunkConcatMu.RLock()
	fn := chunkConcat[t]
	chunkConcatMu.RUnlock()
	if fn == nil {
		return zero, fmt.Errorf("compose: no stream concat func for type %v", t)
	}
	out, err := fn(items)
	if err != nil {
		return zero, err
	}
	v, ok := out.(T)
	if !ok {
		return zero, fmt.Errorf("compose: stream concat result type mismatch: %T", out)
	}
	return v, nil
}

// ConcatMessageStream drains and concatenates message stream chunks.
func ConcatMessageStream(sr *schema.StreamReader[*schema.Message]) (*schema.Message, error) {
	initOnce.Do(initStreamConcat)
	return ConcatStreamReader(sr)
}

var initOnce sync.Once
