package compose

import (
	"context"
	"fmt"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// MergeGraphStates merges a parallel branch snapshot into dst under key.
func MergeGraphStates(dst *GraphState, key string, part *GraphState) {
	if dst == nil || part == nil {
		return
	}
	if dst.Vars == nil {
		dst.Vars = map[string]any{}
	}
	dst.Vars[key] = cloneGraphState(part)
	if len(part.Messages) > len(dst.Messages) {
		dst.Messages = append([]*schema.Message(nil), part.Messages...)
	}
	if part.LastOutput != nil {
		dst.LastOutput = part.LastOutput
	}
	for _, r := range GraphRunsFromState(part) {
		appendGraphRun(dst, r)
	}
}

// MergeAllGraphStates merges all parallel branch results into dst.Vars["parallel"].
func MergeAllGraphStates(dst *GraphState, parts map[string]*GraphState) {
	if dst == nil || len(parts) == 0 {
		return
	}
	if dst.Vars == nil {
		dst.Vars = map[string]any{}
	}
	merged := map[string]*GraphState{}
	for k, p := range parts {
		if p != nil {
			merged[k] = cloneGraphState(p)
		}
	}
	dst.Vars["parallel"] = merged
	for k, p := range parts {
		MergeGraphStates(dst, k, p)
	}
}

// AddParallelFanInNode registers a node that runs branches concurrently and fan-in merges state.
func (g *Graph) AddParallelFanInNode(name string, branches map[string]NodeFunc) error {
	if g == nil {
		return fmt.Errorf("compose: nil graph")
	}
	if len(branches) == 0 {
		return fmt.Errorf("compose: empty parallel branches for node %q", name)
	}
	return g.AddLambdaNode(name, func(ctx context.Context, st *GraphState) error {
		parts := make(map[string]*GraphState, len(branches))
		var wg sync.WaitGroup
		var mu sync.Mutex
		var firstErr error
		for key, fn := range branches {
			if fn == nil {
				continue
			}
			wg.Add(1)
			go func(key string, fn NodeFunc) {
				defer wg.Done()
				child := cloneGraphState(st)
				if err := fn(ctx, child); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("parallel branch %q: %w", key, err)
					}
					mu.Unlock()
					return
				}
				mu.Lock()
				parts[key] = child
				mu.Unlock()
			}(key, fn)
		}
		wg.Wait()
		if firstErr != nil {
			return firstErr
		}
		MergeAllGraphStates(st, parts)
		return nil
	})
}
