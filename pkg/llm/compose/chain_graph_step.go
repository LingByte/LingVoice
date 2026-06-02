package compose

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// GraphInfo describes a compiled graph for introspection (Eino GraphInfo subset).
type GraphInfo struct {
	Name            string              `json:"name"`
	RunMode         GraphRunMode        `json:"run_mode"`
	Nodes           []string            `json:"nodes"`
	Entries         []string            `json:"entries"`
	Edges           map[string]string   `json:"edges"`
	FanOut          map[string][]string `json:"fan_out,omitempty"`
	Branches        map[string][]string `json:"branches,omitempty"`
	BranchSources   []string            `json:"branch_sources,omitempty"`
	JoinAny         []string            `json:"join_any,omitempty"`
	NodeKinds       map[string]NodeKind `json:"node_kinds,omitempty"`
	HasReActRuntime bool                `json:"has_react_runtime,omitempty"`
	ReActNodes      []string            `json:"react_nodes,omitempty"`
}

// Describe returns a snapshot of graph topology.
func (r *CompiledGraph) Describe() GraphInfo {
	if r == nil {
		return GraphInfo{}
	}
	info := GraphInfo{
		Name:    r.name,
		RunMode: r.runMode,
		Entries: append([]string(nil), r.entries...),
		Edges:   map[string]string{},
		FanOut:  map[string][]string{},
	}
	for name := range r.nodes {
		info.Nodes = append(info.Nodes, name)
	}
	for from, to := range r.edges {
		info.Edges[from] = to
	}
	for from, tos := range r.fanOut {
		info.FanOut[from] = append([]string(nil), tos...)
	}
	if len(r.branches) > 0 {
		info.Branches = map[string][]string{}
		for from, allowed := range r.branchOK {
			for to := range allowed {
				info.Branches[from] = append(info.Branches[from], to)
			}
		}
	}
	for name := range r.joinAny {
		if r.joinAny[name] {
			info.JoinAny = append(info.JoinAny, name)
		}
	}
	if len(r.nodeKinds) > 0 {
		info.NodeKinds = make(map[string]NodeKind, len(r.nodeKinds))
		for k, v := range r.nodeKinds {
			info.NodeKinds[k] = v
		}
	}
	if len(r.branches) > 0 {
		info.BranchSources = make([]string, 0, len(r.branches))
		for from := range r.branches {
			info.BranchSources = append(info.BranchSources, from)
		}
	}
	info.HasReActRuntime = r.HasReActRuntime()
	info.ReActNodes = reactNodeNames(r)
	return info
}

// SubGraphStep runs a nested graph inside a chain.
type SubGraphStep struct {
	Name  string
	Graph *Graph
	Opts  []GraphCompileOption
}

func (s SubGraphStep) Run(ctx context.Context, st *State) error {
	if s.Graph == nil {
		return fmt.Errorf("compose: nil subgraph")
	}
	cg, err := s.Graph.Compile(s.Opts...)
	if err != nil {
		return err
	}
	gs := &GraphState{
		Messages:   append([]*schema.Message(nil), st.Messages...),
		LastOutput: st.LastOutput,
		Vars:       map[string]any{},
	}
	for k, v := range st.Vars {
		if k != RunsKey {
			gs.Vars[k] = v
		}
	}
	subSt, _, err := cg.Invoke(ctx, gs.Messages)
	if err != nil {
		return err
	}
	if subSt != nil {
		st.Messages = subSt.Messages
		st.LastOutput = subSt.LastOutput
		for k, v := range subSt.Vars {
			if k != RunsKey {
				if st.Vars == nil {
					st.Vars = map[string]any{}
				}
				st.Vars[k] = v
			}
		}
	}
	return nil
}

// PassthroughStep is a no-op chain/graph step (Eino AppendPassthrough subset).
type PassthroughStep struct{}

func (PassthroughStep) Run(_ context.Context, _ *State) error { return nil }
