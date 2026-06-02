package compose

import (
	"context"
)

const streamRoundKey = "__stream_round"

// WithStreamCheckpoint enables periodic checkpoint writes during graph StreamFrames.
func WithStreamCheckpoint() GraphInvokeOption {
	return func(c *graphInvokeConfig) {
		c.streamCheckpoint = true
	}
}

func (r *CompiledGraph) maybeSaveStreamCheckpoint(ctx context.Context, icfg graphInvokeConfig, st *GraphState, nextNode string, round int) {
	if r == nil || r.checkpointStore == nil || icfg.checkpointID == "" || !icfg.streamCheckpoint {
		return
	}
	cpSt := cloneGraphState(st)
	if cpSt.Vars == nil {
		cpSt.Vars = map[string]any{}
	}
	cpSt.Vars[streamRoundKey] = round
	_ = r.saveCheckpoint(ctx, icfg.checkpointID, &Checkpoint{
		NextNode: nextNode,
		State:    cpSt,
	})
}

// StreamRoundFromCheckpoint reads the streaming round marker from checkpoint state.
func StreamRoundFromCheckpoint(st *GraphState) int {
	if st == nil || st.Vars == nil {
		return 0
	}
	n, _ := st.Vars[streamRoundKey].(int)
	return n
}
