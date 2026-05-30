package compose

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func computePredecessors(g *Graph) map[string][]string {
	if g == nil {
		return map[string][]string{}
	}
	preds := map[string][]string{}
	addPred := func(node, pred string) {
		if node == "" || node == END {
			return
		}
		for _, existing := range preds[node] {
			if existing == pred {
				return
			}
		}
		preds[node] = append(preds[node], pred)
	}

	if tos, ok := g.fanOut[START]; ok {
		for _, to := range tos {
			addPred(to, START)
		}
	} else if to, ok := g.edges[START]; ok && to != END {
		addPred(to, START)
	}

	for from, to := range g.edges {
		if from == START || to == END {
			continue
		}
		addPred(to, from)
	}
	for from, tos := range g.fanOut {
		if from == START {
			continue
		}
		for _, to := range tos {
			if to == END {
				continue
			}
			addPred(to, from)
		}
	}
	for from, allowed := range g.branchOK {
		for to := range allowed {
			if to == END {
				continue
			}
			addPred(to, from)
		}
	}
	return preds
}

func mergeNodeOutput(dst, src *GraphState, nodeName string) {
	if dst == nil || src == nil {
		return
	}
	if dst.Vars == nil {
		dst.Vars = map[string]any{}
	}
	dst.Vars[nodeOutputKey(nodeName)] = nodeOutputMap(src)
	if len(src.Messages) > len(dst.Messages) {
		dst.Messages = append([]*schema.Message(nil), src.Messages...)
	}
	if src.LastOutput != nil {
		dst.LastOutput = src.LastOutput
	}
	for k, v := range src.Vars {
		if k == RunsKey {
			continue
		}
		dst.Vars[k] = v
	}
	for _, r := range GraphRunsFromState(src) {
		appendGraphRun(dst, r)
	}
}

func nodeOutputKey(node string) string {
	return "node:" + node
}

func nodeOutputMap(st *GraphState) map[string]any {
	out := map[string]any{}
	if st == nil {
		return out
	}
	if st.LastOutput != nil {
		out["last_output"] = st.LastOutput
	}
	if len(st.Messages) > 0 {
		out["messages"] = append([]*schema.Message(nil), st.Messages...)
	}
	for k, v := range st.Vars {
		if k != RunsKey {
			out[k] = v
		}
	}
	return out
}

func applyNodeInputMappings(st *GraphState, node string, mappings []FieldMapping) error {
	if st == nil || len(mappings) == 0 {
		return nil
	}
	local := map[string]any{}
	for _, m := range mappings {
		srcNode := m.FromNode
		if srcNode == "" {
			continue
		}
		src, _ := st.Vars[nodeOutputKey(srcNode)].(map[string]any)
		if src == nil {
			src = map[string]any{}
		}
		if err := MapFields(src, local, m); err != nil {
			return err
		}
	}
	if st.Vars == nil {
		st.Vars = map[string]any{}
	}
	st.Vars[nodeInputKey(node)] = local
	return nil
}

func nodeInputKey(node string) string {
	return "input:" + node
}

func (r *CompiledGraph) successors(node string, ctx context.Context, st *GraphState) ([]string, error) {
	if cond, ok := r.branches[node]; ok {
		to, err := cond(ctx, st)
		if err != nil {
			return nil, err
		}
		if to == END {
			return []string{END}, nil
		}
		allowed := r.branchOK[node]
		if allowed != nil && !allowed[to] {
			return nil, fmt.Errorf("compose: branch from %q to disallowed node %q", node, to)
		}
		return []string{to}, nil
	}
	if tos, ok := r.fanOut[node]; ok {
		return append([]string(nil), tos...), nil
	}
	if to, ok := r.edges[node]; ok {
		return []string{to}, nil
	}
	return nil, fmt.Errorf("compose: node %q has no successors", node)
}
