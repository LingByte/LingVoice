package compose

import (
	"context"
	"fmt"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// FuncStep runs a custom function as a chain/graph step.
type FuncStep struct {
	Name string
	Fn   func(ctx context.Context, st *State) error
}

func (s FuncStep) Run(ctx context.Context, st *State) error {
	if s.Fn == nil {
		return nil
	}
	return s.Fn(ctx, st)
}

// Parallel runs multiple steps concurrently (Eino compose.NewParallel subset).
// Each step receives a shallow copy of Messages; results merge into st.Vars[key].
type Parallel struct {
	Steps map[string]Step
}

// NewParallel creates a parallel step group.
func NewParallel(steps map[string]Step) *Parallel {
	return &Parallel{Steps: steps}
}

// Run executes all steps in parallel and stores errors keyed by name.
func (p *Parallel) Run(ctx context.Context, st *State) error {
	if p == nil || len(p.Steps) == 0 {
		return nil
	}
	if st.Vars == nil {
		st.Vars = map[string]any{}
	}
	type result struct {
		key string
		err error
	}
	ch := make(chan result, len(p.Steps))
	var wg sync.WaitGroup
	for key, step := range p.Steps {
		if step == nil {
			continue
		}
		wg.Add(1)
		go func(key string, step Step) {
			defer wg.Done()
			child := &State{
				Messages: append([]*schema.Message(nil), st.Messages...),
				Vars:     map[string]any{},
			}
			err := step.Run(ctx, child)
			if err == nil {
				st.Vars[key] = child
			}
			ch <- result{key: key, err: err}
		}(key, step)
	}
	wg.Wait()
	close(ch)
	var first error
	for r := range ch {
		if r.err != nil && first == nil {
			first = fmt.Errorf("parallel step %q: %w", r.key, r.err)
		}
	}
	return first
}

// ParallelStep adapts Parallel to Chain Step.
type ParallelStep struct {
	Parallel *Parallel
}

func (s ParallelStep) Run(ctx context.Context, st *State) error {
	if s.Parallel == nil {
		return nil
	}
	return s.Parallel.Run(ctx, st)
}

// MergeParallelStates merges child State snapshots from a Parallel run into parent vars.
func MergeParallelStates(dst *State, key string) {
	if dst == nil || dst.Vars == nil {
		return
	}
	child, ok := dst.Vars[key].(*State)
	if !ok || child == nil {
		return
	}
	if len(child.Messages) > len(dst.Messages) {
		dst.Messages = append([]*schema.Message(nil), child.Messages...)
	}
	if child.LastOutput != nil {
		dst.LastOutput = child.LastOutput
	}
	for k, v := range child.Vars {
		dst.Vars[k] = v
	}
	MergeRunsInto(dst, child)
}
