package compose

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func passthroughGraphNode(_ context.Context, _ *GraphState) error {
	return nil
}

func chainStateFromGraph(st *GraphState) *State {
	vars := map[string]any{}
	if st != nil {
		for k, v := range st.Vars {
			if k != RunsKey {
				vars[k] = v
			}
		}
	}
	cs := &State{Vars: vars}
	if st != nil {
		cs.Messages = append([]*schema.Message(nil), st.Messages...)
		cs.LastOutput = st.LastOutput
	}
	return cs
}

func compileChainBuilderGraph(name string, steps []Step, opts ...GraphCompileOption) (*CompiledGraph, error) {
	if len(steps) == 0 {
		return nil, fmt.Errorf("compose: empty chain builder")
	}
	if name == "" {
		name = "chain-graph"
	}
	g := NewGraph(name)
	var prevEnds []string

	for i, step := range steps {
		if step == nil {
			return nil, fmt.Errorf("compose: chain builder step %d is nil", i)
		}
		prefix := fmt.Sprintf("step_%d", i)

		if bs, ok := step.(BranchStep); ok {
			if len(bs.Steps) < 2 {
				return nil, fmt.Errorf("compose: branch step %d needs at least 2 targets", i)
			}
			allowed := map[string]bool{}
			keyToNode := map[string]string{}
			var branchEnds []string
			for key, brStep := range bs.Steps {
				if brStep == nil {
					continue
				}
				nodeKey := fmt.Sprintf("%s_%s", prefix, key)
				keyToNode[key] = nodeKey
				allowed[nodeKey] = true
				if err := g.AddLambdaNode(nodeKey, stepToNodeFn(brStep)); err != nil {
					return nil, err
				}
				branchEnds = append(branchEnds, nodeKey)
			}
			if len(branchEnds) < 2 {
				return nil, fmt.Errorf("compose: branch step %d needs at least 2 non-nil targets", i)
			}

			from := ""
			if len(prevEnds) == 0 {
				entry := prefix + "_entry"
				if err := g.AddLambdaNode(entry, passthroughGraphNode); err != nil {
					return nil, err
				}
				if err := g.AddEdge(START, entry); err != nil {
					return nil, err
				}
				from = entry
			} else if len(prevEnds) == 1 {
				from = prevEnds[0]
			} else {
				join := prefix + "_join_in"
				if err := g.AddLambdaNode(join, passthroughGraphNode); err != nil {
					return nil, err
				}
				for _, p := range prevEnds {
					if err := g.AddEdge(p, join); err != nil {
						return nil, err
					}
				}
				from = join
			}

			selectFn := func(ctx context.Context, st *GraphState) (string, error) {
				key, err := bs.Select(ctx, chainStateFromGraph(st))
				if err != nil {
					return "", err
				}
				if _, ok := bs.Steps[key]; !ok {
					if bs.Default != "" {
						key = bs.Default
					} else {
						return "", fmt.Errorf("compose: branch key %q not found", key)
					}
				}
				nodeKey, ok := keyToNode[key]
				if !ok {
					return "", fmt.Errorf("compose: branch node for key %q missing", key)
				}
				return nodeKey, nil
			}
			if err := g.AddBranch(from, selectFn, allowed); err != nil {
				return nil, err
			}
			prevEnds = branchEnds
			continue
		}

		if ms, ok := step.(MultiBranchStep); ok && len(ms.Steps) >= 2 {
			routerName := prefix + "_router"
			if err := g.AddLambdaNode(routerName, func(ctx context.Context, st *GraphState) error {
				keys, err := resolveMultiBranchKeys(ms, ctx, st)
				if err != nil {
					return err
				}
				if st.Vars == nil {
					st.Vars = map[string]any{}
				}
				st.Vars[selectedBranchesKey] = keys
				return nil
			}); err != nil {
				return nil, err
			}
			var branchNames []string
			for key, sstep := range ms.Steps {
				if sstep == nil {
					continue
				}
				nodeKey := fmt.Sprintf("%s_%s", prefix, key)
				if err := g.AddLambdaNode(nodeKey, wrapGatedBranch(key, ms, stepToNodeFn(sstep))); err != nil {
					return nil, err
				}
				branchNames = append(branchNames, nodeKey)
			}
			if len(branchNames) < 2 {
				return nil, fmt.Errorf("compose: multi-branch step %d needs at least 2 non-nil targets", i)
			}
			joinName := prefix + "_join"
			if err := g.AddLambdaNode(joinName, passthroughGraphNode); err != nil {
				return nil, err
			}
			for _, bn := range branchNames {
				if err := g.AddEdge(bn, joinName); err != nil {
					return nil, err
				}
			}
			if len(prevEnds) == 0 {
				if err := g.AddEdge(START, routerName); err != nil {
					return nil, err
				}
			} else {
				for _, p := range prevEnds {
					if err := g.AddEdge(p, routerName); err != nil {
						return nil, err
					}
				}
			}
			if err := g.AddFanOutEdges(routerName, branchNames...); err != nil {
				return nil, err
			}
			prevEnds = []string{joinName}
			if g.runMode == RunModeDAG {
				g.runMode = RunModePregel
			}
			continue
		}

		if ps, ok := step.(SubGraphStep); ok && ps.Graph != nil {
			nodeName := prefix
			cg, err := ps.Graph.Compile(ps.Opts...)
			if err != nil {
				return nil, err
			}
			if err := g.AddLambdaNode(nodeName, func(ctx context.Context, st *GraphState) error {
				subSt, _, err := cg.Invoke(ctx, st.Messages)
				if err != nil {
					return err
				}
				if subSt != nil {
					st.Messages = subSt.Messages
					st.LastOutput = subSt.LastOutput
					for k, v := range subSt.Vars {
						if st.Vars == nil {
							st.Vars = map[string]any{}
						}
						st.Vars[k] = v
					}
				}
				return nil
			}); err != nil {
				return nil, err
			}
			if len(prevEnds) == 0 {
				if err := g.AddEdge(START, nodeName); err != nil {
					return nil, err
				}
			} else {
				for _, p := range prevEnds {
					if err := g.AddEdge(p, nodeName); err != nil {
						return nil, err
					}
				}
			}
			prevEnds = []string{nodeName}
			continue
		}

		if _, ok := step.(PassthroughStep); ok {
			nodeName := prefix + "_pass"
			if err := g.AddLambdaNode(nodeName, passthroughGraphNode); err != nil {
				return nil, err
			}
			if len(prevEnds) == 0 {
				if err := g.AddEdge(START, nodeName); err != nil {
					return nil, err
				}
			} else {
				for _, p := range prevEnds {
					if err := g.AddEdge(p, nodeName); err != nil {
						return nil, err
					}
				}
			}
			prevEnds = []string{nodeName}
			continue
		}

		if ps, ok := step.(ParallelStep); ok && ps.Parallel != nil && len(ps.Parallel.Steps) >= 2 {
			var branchNames []string
			for key, sstep := range ps.Parallel.Steps {
				if sstep == nil {
					continue
				}
				nodeKey := fmt.Sprintf("%s_%s", prefix, key)
				if err := g.AddLambdaNode(nodeKey, stepToNodeFn(sstep)); err != nil {
					return nil, err
				}
				branchNames = append(branchNames, nodeKey)
			}
			if len(branchNames) < 2 {
				return nil, fmt.Errorf("compose: parallel step %d needs at least 2 non-nil targets", i)
			}
			joinName := prefix + "_join"
			if err := g.AddLambdaNode(joinName, passthroughGraphNode); err != nil {
				return nil, err
			}
			for _, bn := range branchNames {
				if err := g.AddEdge(bn, joinName); err != nil {
					return nil, err
				}
			}
			if len(prevEnds) == 0 {
				if err := g.AddFanOutEdges(START, branchNames...); err != nil {
					return nil, err
				}
			} else if len(prevEnds) == 1 {
				if err := g.AddFanOutEdges(prevEnds[0], branchNames...); err != nil {
					return nil, err
				}
			} else {
				joinIn := prefix + "_join_in"
				if err := g.AddLambdaNode(joinIn, passthroughGraphNode); err != nil {
					return nil, err
				}
				for _, p := range prevEnds {
					if err := g.AddEdge(p, joinIn); err != nil {
						return nil, err
					}
				}
				if err := g.AddFanOutEdges(joinIn, branchNames...); err != nil {
					return nil, err
				}
			}
			prevEnds = []string{joinName}
			if g.runMode == RunModeDAG {
				g.runMode = RunModePregel
			}
			continue
		}

		nodeName := prefix
		if err := g.AddLambdaNode(nodeName, stepToNodeFn(step)); err != nil {
			return nil, err
		}
		if cms, ok := step.(ChatModelStep); ok && cms.Model != nil {
			registerGraphChatModel(g, nodeName, cms.Model, cms.Opts)
		} else if pcms, ok := step.(*ChatModelStep); ok && pcms != nil && pcms.Model != nil {
			registerGraphChatModel(g, nodeName, pcms.Model, pcms.Opts)
		}
		if len(prevEnds) == 0 {
			if err := g.AddEdge(START, nodeName); err != nil {
				return nil, err
			}
		} else {
			for _, p := range prevEnds {
				if err := g.AddEdge(p, nodeName); err != nil {
					return nil, err
				}
			}
		}
		prevEnds = []string{nodeName}
	}

	for _, p := range prevEnds {
		if err := g.AddEdge(p, END); err != nil {
			return nil, err
		}
	}
	return g.Compile(opts...)
}
