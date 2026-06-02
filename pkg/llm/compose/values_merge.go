package compose

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type mergeFunc func([]any) (any, error)

var (
	mergeMu   sync.RWMutex
	mergeByTy = map[reflect.Type]mergeFunc{}
)

func init() {
	registerBuiltInMerge(reflect.TypeOf(map[string]any(nil)), mergeStringAnyMaps)
	registerBuiltInMerge(reflect.TypeOf(map[string]string(nil)), mergeStringStringMaps)
	registerBuiltInMerge(reflect.TypeOf([]*schema.Message(nil)), mergeMessageSlices)
}

func registerBuiltInMerge(t reflect.Type, fn mergeFunc) {
	mergeByTy[t] = fn
}

// RegisterValuesMergeFunc registers a custom fan-in merge for type T (Eino compose.RegisterValuesMergeFunc subset).
func RegisterValuesMergeFunc[T any](fn func([]T) (T, error)) {
	if fn == nil {
		return
	}
	var zero T
	t := reflect.TypeOf(zero)
	mergeMu.Lock()
	mergeByTy[t] = func(vs []any) (any, error) {
		items := make([]T, len(vs))
		for i, v := range vs {
			item, ok := v.(T)
			if !ok {
				var z T
				return z, fmt.Errorf("compose: merge type mismatch at %d: want %v got %T", i, t, v)
			}
			items[i] = item
		}
		return fn(items)
	}
	mergeMu.Unlock()
}

// MergeValues merges fan-in outputs. Built-ins: map[string]any, map[string]string, []*schema.Message.
func MergeValues(vs []any) (any, error) {
	if len(vs) == 0 {
		return nil, nil
	}
	if len(vs) == 1 {
		return vs[0], nil
	}
	t := reflect.TypeOf(vs[0])
	mergeMu.RLock()
	fn := mergeByTy[t]
	mergeMu.RUnlock()
	if fn == nil {
		return nil, fmt.Errorf("compose: unsupported merge type %v", t)
	}
	return fn(vs)
}

func mergeStringAnyMaps(vs []any) (any, error) {
	out := map[string]any{}
	for i, v := range vs {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("compose: merge map[string]any type mismatch at %d: %T", i, v)
		}
		for k, val := range m {
			if _, dup := out[k]; dup {
				return nil, fmt.Errorf("compose: duplicated key %q in merge", k)
			}
			out[k] = val
		}
	}
	return out, nil
}

func mergeStringStringMaps(vs []any) (any, error) {
	out := map[string]string{}
	for i, v := range vs {
		m, ok := v.(map[string]string)
		if !ok {
			return nil, fmt.Errorf("compose: merge map[string]string type mismatch at %d: %T", i, v)
		}
		for k, val := range m {
			if _, dup := out[k]; dup {
				return nil, fmt.Errorf("compose: duplicated key %q in merge", k)
			}
			out[k] = val
		}
	}
	return out, nil
}

func mergeMessageSlices(vs []any) (any, error) {
	var longest []*schema.Message
	for i, v := range vs {
		msgs, ok := v.([]*schema.Message)
		if !ok {
			return nil, fmt.Errorf("compose: merge []*schema.Message type mismatch at %d: %T", i, v)
		}
		if len(msgs) > len(longest) {
			longest = append([]*schema.Message(nil), msgs...)
		}
	}
	return longest, nil
}
