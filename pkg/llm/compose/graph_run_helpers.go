package compose

import (
	"context"
	"fmt"
)

var (
	errNilGraphRun = fmt.Errorf("compose: nil graph run")
)

func errPregelStalled(name string, step int) error {
	return fmt.Errorf("compose: pregel graph %q stalled at superstep %d", name, step)
}

func errPregelMaxSteps(name string, max int) error {
	return fmt.Errorf("compose: pregel graph %q exceeded max steps (%d)", name, max)
}

func errMissingNode(name string) error {
	return fmt.Errorf("compose: graph missing node %q", name)
}

func (r *CompiledGraph) finishPregel(ctx context.Context, st *GraphState, trace []GraphStep, icfg graphInvokeConfig) (*GraphState, []GraphStep, error) {
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

func (r *CompiledGraph) interruptPregel(
	ctx context.Context,
	st *GraphState,
	trace []GraphStep,
	icfg graphInvokeConfig,
	completed, scheduled map[string]bool,
	reachedEnd bool,
	step int,
) (*GraphState, []GraphStep, error) {
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

func (r *CompiledGraph) interruptAfterPregel(
	ctx context.Context,
	st *GraphState,
	trace []GraphStep,
	icfg graphInvokeConfig,
	completed, scheduled map[string]bool,
	reachedEnd bool,
	step int,
	nodeName string,
) (*GraphState, []GraphStep, error) {
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
