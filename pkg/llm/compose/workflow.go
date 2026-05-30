package compose

import (
	"context"
	"fmt"
)

// Workflow is a field-mapped graph builder (Eino compose.Workflow subset).
type Workflow struct {
	name      string
	graph     *Graph
	inputKey  string
	steps     []workflowStep
	nodeMaps     map[string][]FieldMapping
	branchFns    map[string]BranchStep
	staticValues map[string]map[string]any
}

type workflowStep struct {
	node string
	fn   NodeFunc
	from []FieldMapping
}

// WorkflowNode references one node in a workflow for dependency/mapping APIs.
type WorkflowNode struct {
	wf  *Workflow
	key string
}

// NewWorkflow creates a workflow with a default input var key.
func NewWorkflow(name string) *Workflow {
	return &Workflow{
		name:      name,
		graph:     NewGraph(name),
		inputKey:  "input",
		nodeMaps:     map[string][]FieldMapping{},
		branchFns:    map[string]BranchStep{},
		staticValues: map[string]map[string]any{},
	}
}

// Node returns a workflow node handle for AddInput / static values.
func (w *Workflow) Node(key string) *WorkflowNode {
	if w == nil {
		return nil
	}
	return &WorkflowNode{wf: w, key: key}
}

// AddInputNode registers the workflow entry that stores input into state vars.
func (w *Workflow) AddInputNode(name string) error {
	if w == nil {
		return fmt.Errorf("compose: nil workflow")
	}
	if err := w.graph.AddLambdaNode(name, func(_ context.Context, st *GraphState) error {
		if st.Vars == nil {
			st.Vars = map[string]any{}
		}
		if len(st.Messages) > 0 {
			st.Vars[w.inputKey] = st.Messages[len(st.Messages)-1].PlainText()
		}
		st.Vars[nodeOutputKey(name)] = nodeOutputMap(st)
		return nil
	}); err != nil {
		return err
	}
	w.graph.markNodeKind(name, NodeKindInput)
	return nil
}

// AddLambdaStep adds a node with optional input field mappings from prior node outputs.
func (w *Workflow) AddLambdaStep(name string, fn NodeFunc, mappings ...FieldMapping) error {
	if w == nil || fn == nil {
		return fmt.Errorf("compose: nil workflow step")
	}
	if len(mappings) > 0 {
		w.nodeMaps[name] = append(w.nodeMaps[name], mappings...)
	}
	wrapped := w.wrapWorkflowNode(name, fn, mappings)
	w.steps = append(w.steps, workflowStep{node: name, fn: wrapped, from: mappings})
	return w.graph.AddLambdaNode(name, wrapped)
}

// AddInput declares field mappings from a predecessor node (Eino WorkflowNode.AddInput subset).
func (n *WorkflowNode) AddInput(fromNodeKey string, mappings ...FieldMapping) error {
	if n == nil || n.wf == nil || n.key == "" {
		return fmt.Errorf("compose: nil workflow node")
	}
	for i := range mappings {
		mappings[i].FromNode = fromNodeKey
	}
	n.wf.nodeMaps[n.key] = append(n.wf.nodeMaps[n.key], mappings...)
	return nil
}

// SetStaticValue sets a static field merged into the node input map before execution.
func (n *WorkflowNode) SetStaticValue(field string, value any) {
	if n == nil || n.wf == nil || field == "" {
		return
	}
	if n.wf.staticValues[n.key] == nil {
		n.wf.staticValues[n.key] = map[string]any{}
	}
	n.wf.staticValues[n.key][field] = value
}

// AddBranch adds conditional routing from a workflow node (Eino Workflow.AddBranch subset).
func (w *Workflow) AddBranch(fromNodeKey string, branch BranchStep) error {
	if w == nil {
		return fmt.Errorf("compose: nil workflow")
	}
	if len(branch.Steps) < 2 {
		return fmt.Errorf("compose: workflow branch needs at least 2 targets")
	}
	w.branchFns[fromNodeKey] = branch
	allowed := map[string]bool{}
	for key, step := range branch.Steps {
		if step == nil {
			continue
		}
		nodeKey := fromNodeKey + "_branch_" + key
		allowed[nodeKey] = true
		if err := w.AddLambdaStep(nodeKey, func(ctx context.Context, st *GraphState) error {
			return stepToNodeFn(step)(ctx, st)
		}); err != nil {
			return err
		}
	}
	selectFn := func(ctx context.Context, st *GraphState) (string, error) {
		bs := w.branchFns[fromNodeKey]
		k, err := bs.Select(ctx, chainStateFromGraph(st))
		if err != nil {
			return "", err
		}
		if _, ok := bs.Steps[k]; !ok {
			if bs.Default != "" {
				k = bs.Default
			} else {
				return "", fmt.Errorf("compose: workflow branch key %q not found", k)
			}
		}
		return fromNodeKey + "_branch_" + k, nil
	}
	return w.graph.AddBranch(fromNodeKey, selectFn, allowed)
}

func (w *Workflow) wrapWorkflowNode(name string, fn NodeFunc, mappings []FieldMapping) NodeFunc {
	return func(ctx context.Context, st *GraphState) error {
		allMaps := append([]FieldMapping(nil), mappings...)
		if extra, ok := w.nodeMaps[name]; ok {
			allMaps = append(allMaps, extra...)
		}
		if len(allMaps) > 0 {
			if err := applyNodeInputMappings(st, name, allMaps); err != nil {
				return err
			}
		}
		if statics := w.staticValues[name]; len(statics) > 0 {
			if st.Vars == nil {
				st.Vars = map[string]any{}
			}
			in, _ := st.Vars[nodeInputKey(name)].(map[string]any)
			if in == nil {
				in = map[string]any{}
			}
			for k, v := range statics {
				in[k] = v
			}
			st.Vars[nodeInputKey(name)] = in
		}
		if err := fn(ctx, st); err != nil {
			return err
		}
		if st.Vars == nil {
			st.Vars = map[string]any{}
		}
		st.Vars[nodeOutputKey(name)] = nodeOutputMap(st)
		return nil
	}
}

// AddEdge forwards to underlying graph.
func (w *Workflow) AddEdge(from, to string) error {
	if w == nil {
		return fmt.Errorf("compose: nil workflow")
	}
	return w.graph.AddEdge(from, to)
}

// AddFanOutEdges forwards fan-out to underlying graph.
func (w *Workflow) AddFanOutEdges(from string, to ...string) error {
	if w == nil {
		return fmt.Errorf("compose: nil workflow")
	}
	return w.graph.AddFanOutEdges(from, to...)
}

// Compile builds the workflow graph.
func (w *Workflow) Compile(opts ...GraphCompileOption) (*CompiledGraph, error) {
	if w == nil {
		return nil, fmt.Errorf("compose: nil workflow")
	}
	return w.graph.Compile(opts...)
}
