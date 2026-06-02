package compose

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// BSPPhase identifies a superstep phase (Eino graph_run subset).
type BSPPhase string

const (
	BSPPlan    BSPPhase = "plan"
	BSPExecute BSPPhase = "execute"
	BSPUpdate  BSPPhase = "update"
)

// GraphRun orchestrates Pregel BSP execution (Eino graph_run.go subset).
type GraphRun struct {
	graph *CompiledGraph
}

// NewGraphRun creates a BSP runner for a compiled graph.
func NewGraphRun(g *CompiledGraph) *GraphRun {
	return &GraphRun{graph: g}
}

// Run executes BSP supersteps until END, interrupt, or max steps.
func (gr *GraphRun) Run(
	ctx context.Context,
	st *GraphState,
	trace []GraphStep,
	icfg graphInvokeConfig,
	start *PregelCheckpoint,
	skipInterruptBefore string,
) (*GraphState, []GraphStep, error) {
	r := gr.graph
	if r == nil {
		return st, trace, errNilGraphRun
	}
	ensureMailbox(st)
	applyCompiledChannelSpecs(st, r.channelSpecs, r.pregelMerge)

	completed := map[string]bool{}
	scheduled := map[string]bool{}
	reachedEnd := false
	superstep := 0

	if start != nil {
		completed = sliceToStringSet(start.Completed)
		scheduled = sliceToStringSet(start.Scheduled)
		reachedEnd = start.ReachedEnd
		superstep = start.Superstep
	} else {
		for _, e := range r.entries {
			scheduled[e] = true
		}
	}

	for step := superstep; step < r.maxSteps; step++ {
		phase := BSPPlan
		_ = phase

		if graphInterruptRequested(ctx) {
			return r.interruptPregel(ctx, st, trace, icfg, completed, scheduled, reachedEnd, step)
		}

		ready := r.pregelReady(completed, scheduled)
		if len(ready) == 0 {
			if reachedEnd {
				return r.finishPregel(ctx, st, trace, icfg)
			}
			return st, trace, errPregelStalled(r.name, step)
		}

		partials, newTrace, err := r.bspExecute(ctx, st, trace, icfg, ready, skipInterruptBefore, step, completed, scheduled, reachedEnd)
		trace = newTrace
		if err != nil {
			return st, trace, err
		}

		phase = BSPUpdate
		for nodeName, partial := range partials {
			mergeNodeOutput(st, partial, nodeName)
			completed[nodeName] = true

			if r.interruptAfter != nil && r.interruptAfter[nodeName] {
				return r.interruptAfterPregel(ctx, st, trace, icfg, completed, scheduled, reachedEnd, step, nodeName)
			}

			succs, err := r.successors(nodeName, ctx, st)
			if err != nil {
				return st, trace, err
			}
			postMailboxFromNode(st, nodeName, succs)
			for _, succ := range succs {
				if succ == END {
					reachedEnd = true
					continue
				}
				scheduled[succ] = true
			}
		}

		if icfg.pregelCheckpoint && icfg.checkpointID != "" {
			_ = r.savePregelCheckpoint(ctx, icfg, st, completed, scheduled, reachedEnd, step+1, nil)
		}
	}
	return st, trace, errPregelMaxSteps(r.name, r.maxSteps)
}

func (r *CompiledGraph) bspExecute(
	ctx context.Context,
	st *GraphState,
	trace []GraphStep,
	icfg graphInvokeConfig,
	ready []string,
	skipInterruptBefore string,
	step int,
	completed, scheduled map[string]bool,
	reachedEnd bool,
) (map[string]*GraphState, []GraphStep, error) {
	partials := make(map[string]*GraphState, len(ready))
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error

	for _, nodeName := range ready {
		if r.interruptBefore != nil && r.interruptBefore[nodeName] && nodeName != skipInterruptBefore {
			info := &InterruptInfo{
				State:       cloneGraphState(st),
				BeforeNodes: []string{nodeName},
				InterruptContexts: []*InterruptCtx{{
					ID:   newInterruptID(),
					Node: nodeName,
					Info: map[string]string{"phase": "before", "node": nodeName},
				}},
			}
			if err := r.savePregelCheckpoint(ctx, icfg, st, completed, scheduled, reachedEnd, step, info); err != nil {
				return nil, trace, err
			}
			return nil, trace, wrapInterruptInfo(info)
		}

		fn, ok := r.nodes[nodeName]
		if !ok {
			return nil, trace, errMissingNode(nodeName)
		}
		wg.Add(1)
		go func(name string, fn NodeFunc) {
			defer wg.Done()
			local := cloneGraphState(st)
			if err := applyMailboxToNode(local, name, r.preds[name]); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			startAt := time.Now()
			if err := fn(ctx, local); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			partials[name] = local
			trace = append(trace, GraphStep{
				Node:      name,
				StartedAt: startAt,
				Duration:  time.Since(startAt).String(),
			})
			mu.Unlock()
		}(nodeName, fn)
	}
	wg.Wait()
	if firstErr != nil {
		var ie *InterruptError
		if errors.As(firstErr, &ie) {
			if ie.ID == "" {
				ie.ID = newInterruptID()
			}
			info := &InterruptInfo{
				State: cloneGraphState(st),
				InterruptContexts: []*InterruptCtx{{
					ID: ie.ID, Node: ie.Node, Info: ie.Info, State: ie.State,
				}},
			}
			_ = r.savePregelCheckpoint(ctx, icfg, st, completed, scheduled, reachedEnd, step, info)
			return nil, trace, wrapInterruptInfo(info)
		}
		return nil, trace, fmt.Errorf("compose: pregel superstep %d: %w", step, firstErr)
	}
	return partials, trace, nil
}

func (r *CompiledGraph) invokePregelViaGraphRun(
	ctx context.Context,
	st *GraphState,
	trace []GraphStep,
	icfg graphInvokeConfig,
	start *PregelCheckpoint,
	skipInterruptBefore string,
) (*GraphState, []GraphStep, error) {
	return NewGraphRun(r).Run(ctx, st, trace, icfg, start, skipInterruptBefore)
}
