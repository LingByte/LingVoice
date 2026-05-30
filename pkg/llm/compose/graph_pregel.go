package compose

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

func (r *CompiledGraph) invokePregel(
	ctx context.Context,
	st *GraphState,
	trace []GraphStep,
	icfg graphInvokeConfig,
	start *PregelCheckpoint,
	skipInterruptBefore string,
) (*GraphState, []GraphStep, error) {
	if r == nil {
		return st, trace, fmt.Errorf("compose: nil compiled graph")
	}
	ensureMailbox(st)
	applyCompiledPregelMerge(st, r.pregelMerge)
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
		if graphInterruptRequested(ctx) {
			info := &InterruptInfo{
				State: cloneGraphState(st),
				InterruptContexts: []*InterruptCtx{{
					ID:   newInterruptID(),
					Info: map[string]string{"reason": "external_interrupt", "mode": "pregel"},
				}},
			}
			_ = r.savePregelCheckpoint(ctx, icfg, st, completed, scheduled, reachedEnd, step, info)
			return st, trace, wrapInterruptInfo(info)
		}

		ready := r.pregelReady(completed, scheduled)
		if len(ready) == 0 {
			if reachedEnd {
				if icfg.checkpointID != "" {
					if icfg.clearCheckpoint || r.clearOnComplete {
						r.deleteCheckpoint(ctx, icfg.checkpointID)
					} else {
						_ = r.saveCheckpoint(ctx, icfg.checkpointID, &Checkpoint{
							NextNode: END,
							State:    cloneGraphState(st),
						})
					}
				}
				setPregelCheckpointVars(st, nil)
				return st, trace, nil
			}
			return st, trace, fmt.Errorf("compose: pregel graph %q stalled at superstep %d", r.name, step)
		}

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
					return st, trace, err
				}
				return st, trace, wrapInterruptInfo(info)
			}

			fn, ok := r.nodes[nodeName]
			if !ok {
				return st, trace, fmt.Errorf("compose: graph missing node %q", nodeName)
			}
			wg.Add(1)
			go func(name string, fn NodeFunc) {
				defer wg.Done()
				local := cloneGraphState(st)
				applyMailboxToNode(local, name, r.preds[name])
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
				return st, trace, wrapInterruptInfo(info)
			}
			return st, trace, fmt.Errorf("compose: pregel superstep %d: %w", step, firstErr)
		}

		for nodeName, partial := range partials {
			mergeNodeOutput(st, partial, nodeName)
			completed[nodeName] = true

			if r.interruptAfter != nil && r.interruptAfter[nodeName] {
				succs, _ := r.successors(nodeName, ctx, st)
				next := END
				if len(succs) > 0 {
					next = succs[0]
				}
				info := &InterruptInfo{
					State:      cloneGraphState(st),
					AfterNodes: []string{nodeName},
					InterruptContexts: []*InterruptCtx{{
						ID:   newInterruptID(),
						Node: nodeName,
						Info: map[string]string{"phase": "after", "node": nodeName, "next": next},
					}},
				}
				if err := r.savePregelCheckpoint(ctx, icfg, st, completed, scheduled, reachedEnd, step+1, info); err != nil {
					return st, trace, err
				}
				return st, trace, wrapInterruptInfo(info)
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
	return st, trace, fmt.Errorf("compose: pregel graph %q exceeded max steps (%d)", r.name, r.maxSteps)
}

func (r *CompiledGraph) savePregelCheckpoint(
	ctx context.Context,
	icfg graphInvokeConfig,
	st *GraphState,
	completed, scheduled map[string]bool,
	reachedEnd bool,
	superstep int,
	interrupt *InterruptInfo,
) error {
	if r == nil || icfg.checkpointID == "" || r.checkpointStore == nil {
		return nil
	}
	pc := &PregelCheckpoint{
		Completed:  stringSetToSlice(completed),
		Scheduled:  stringSetToSlice(scheduled),
		ReachedEnd: reachedEnd,
		Superstep:  superstep,
	}
	setPregelCheckpointVars(st, pc)
	return r.saveCheckpoint(ctx, icfg.checkpointID, &Checkpoint{
		NextNode:  pregelCheckpointMarker,
		State:     cloneGraphState(st),
		Pregel:    pc,
		Interrupt: interrupt,
	})
}

func (r *CompiledGraph) pregelReady(completed, scheduled map[string]bool) []string {
	var ready []string
	for name := range r.nodes {
		if completed[name] || !scheduled[name] {
			continue
		}
		preds := r.preds[name]
		if len(preds) == 0 {
			ready = append(ready, name)
			continue
		}
		if r.joinAny != nil && r.joinAny[name] {
			anyDone := false
			for _, pred := range preds {
				if pred == START {
					continue
				}
				if completed[pred] {
					anyDone = true
					break
				}
			}
			if anyDone {
				ready = append(ready, name)
			}
			continue
		}
		allDone := true
		for _, pred := range preds {
			if pred == START {
				continue
			}
			if !completed[pred] {
				allDone = false
				break
			}
		}
		if allDone {
			ready = append(ready, name)
		}
	}
	return ready
}
