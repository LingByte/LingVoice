package compose

import (
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

const RunsKey = "runs"

const runsMuKey = "_compose_runs_mu"

func runsMu(st *State) *sync.Mutex {
	if st.Vars == nil {
		st.Vars = map[string]any{}
	}
	if mu, ok := st.Vars[runsMuKey].(*sync.Mutex); ok {
		return mu
	}
	mu := &sync.Mutex{}
	st.Vars[runsMuKey] = mu
	return mu
}

func graphRunsMu(st *GraphState) *sync.Mutex {
	if st.Vars == nil {
		st.Vars = map[string]any{}
	}
	if mu, ok := st.Vars[runsMuKey].(*sync.Mutex); ok {
		return mu
	}
	mu := &sync.Mutex{}
	st.Vars[runsMuKey] = mu
	return mu
}

// RunEntry records one ChatModel invocation inside Chain/Pipeline orchestration.
type RunEntry struct {
	Step          string             `json:"step,omitempty"`
	Model         string             `json:"model,omitempty"`
	InputMessages int                `json:"input_messages"`
	Usage         *schema.TokenUsage `json:"usage,omitempty"`
	StartedAt     time.Time          `json:"started_at"`
	DurationMs    float64            `json:"duration_ms"`
	Stream        bool               `json:"stream,omitempty"`
	Error         string             `json:"error,omitempty"`
}

// AppendRun appends a run entry to state.Vars["runs"].
func AppendRun(st *State, entry RunEntry) {
	if st == nil {
		return
	}
	mu := runsMu(st)
	mu.Lock()
	defer mu.Unlock()
	if st.Vars == nil {
		st.Vars = map[string]any{}
	}
	runs, _ := st.Vars[RunsKey].([]RunEntry)
	st.Vars[RunsKey] = append(runs, entry)
}

// RunsFromState returns recorded runs from chain state.
func RunsFromState(st *State) []RunEntry {
	if st == nil || st.Vars == nil {
		return nil
	}
	runs, _ := st.Vars[RunsKey].([]RunEntry)
	if len(runs) == 0 {
		return nil
	}
	out := make([]RunEntry, len(runs))
	copy(out, runs)
	return out
}

// MergeRunsInto copies runs from src into dst state.
func MergeRunsInto(dst, src *State) {
	if dst == nil || src == nil {
		return
	}
	for _, r := range RunsFromState(src) {
		AppendRun(dst, r)
	}
}

// GraphRunsFromState extracts runs recorded during graph execution.
func GraphRunsFromState(st *GraphState) []RunEntry {
	if st == nil || st.Vars == nil {
		return nil
	}
	runs, _ := st.Vars[RunsKey].([]RunEntry)
	if len(runs) == 0 {
		return nil
	}
	out := make([]RunEntry, len(runs))
	copy(out, runs)
	return out
}

// appendGraphRun appends a run entry to graph state.
func appendGraphRun(st *GraphState, entry RunEntry) {
	if st == nil {
		return
	}
	mu := graphRunsMu(st)
	mu.Lock()
	defer mu.Unlock()
	if st.Vars == nil {
		st.Vars = map[string]any{}
	}
	runs, _ := st.Vars[RunsKey].([]RunEntry)
	st.Vars[RunsKey] = append(runs, entry)
}

func modelName(m interface{ Name() string }) string {
	if m == nil {
		return ""
	}
	return m.Name()
}
