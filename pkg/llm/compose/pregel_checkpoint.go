package compose

const pregelCheckpointMarker = "__pregel_active"

// PregelCheckpoint stores BSP superstep progress for resume.
type PregelCheckpoint struct {
	Completed  []string `json:"completed,omitempty"`
	Scheduled  []string `json:"scheduled,omitempty"`
	ReachedEnd bool     `json:"reached_end,omitempty"`
	Superstep  int      `json:"superstep,omitempty"`
}

func pregelCheckpointFromVars(st *GraphState) *PregelCheckpoint {
	if st == nil || st.Vars == nil {
		return nil
	}
	pc, _ := st.Vars[pregelCheckpointMarker].(*PregelCheckpoint)
	return pc
}

func setPregelCheckpointVars(st *GraphState, pc *PregelCheckpoint) {
	if st == nil {
		return
	}
	if st.Vars == nil {
		st.Vars = map[string]any{}
	}
	if pc == nil {
		delete(st.Vars, pregelCheckpointMarker)
		return
	}
	st.Vars[pregelCheckpointMarker] = pc
}

func stringSetToSlice(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	return out
}

func sliceToStringSet(ss []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range ss {
		m[s] = true
	}
	return m
}

// WithPregelCheckpoint enables periodic checkpoint writes during Pregel Invoke.
func WithPregelCheckpoint() GraphInvokeOption {
	return func(c *graphInvokeConfig) {
		c.pregelCheckpoint = true
	}
}
