package compose

import (
	"context"
	"fmt"
)

// SubGraphCompileOption configures a nested graph at compile time.
type SubGraphCompileOption func(*graphCompileConfig)

// GraphNode wraps a compiled subgraph (Eino AddGraphNode subset).
type GraphNode struct {
	name     string
	graph    *CompiledGraph
	compile  []GraphCompileOption
}

// AddGraphNode registers a nested compiled graph as a node.
func (g *Graph) AddGraphNode(name string, sub *Graph, opts ...SubGraphCompileOption) error {
	if g == nil || sub == nil {
		return fmt.Errorf("compose: nil graph")
	}
	var cfg graphCompileConfig
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	compileOpts := make([]GraphCompileOption, 0, 3)
	if cfg.checkpointStore != nil {
		compileOpts = append(compileOpts, WithCheckPointStore(cfg.checkpointStore))
	}
	for n := range cfg.interruptBefore {
		compileOpts = append(compileOpts, WithInterruptBeforeNodes(n))
	}
	for n := range cfg.interruptAfter {
		compileOpts = append(compileOpts, WithInterruptAfterNodes(n))
	}
	compiled, err := sub.Compile(compileOpts...)
	if err != nil {
		return err
	}
	return g.AddLambdaNode(name, func(ctx context.Context, st *GraphState) error {
		ctx = AppendAddressSegment(ctx, name)
		invokeCtx := ctx
		if compiled.genLocalState != nil {
			invokeCtx = withChildLocalState(ctx, compiled.genLocalState)
		}
		subSt, _, err := compiled.Invoke(invokeCtx, st.Messages)
		if err != nil {
			return err
		}
		if subSt != nil {
			st.Messages = subSt.Messages
			st.LastOutput = subSt.LastOutput
			for k, v := range subSt.Vars {
				st.Vars[k] = v
			}
		}
		return nil
	})
}

// WithSubGraphCheckPointStore forwards checkpoint store to subgraph compile.
func WithSubGraphCheckPointStore(store CheckPointStore) SubGraphCompileOption {
	return func(c *graphCompileConfig) { c.checkpointStore = store }
}

// WithSubGraphInterruptBefore forwards interrupt-before to subgraph compile.
func WithSubGraphInterruptBefore(nodes ...string) SubGraphCompileOption {
	return func(c *graphCompileConfig) {
		if c.interruptBefore == nil {
			c.interruptBefore = map[string]bool{}
		}
		for _, n := range nodes {
			c.interruptBefore[n] = true
		}
	}
}
