package compose

// NodeTriggerMode controls when a node becomes ready (Eino NodeTriggerMode subset).
type NodeTriggerMode int

const (
	// AllPredecessor requires all predecessors to complete (default DAG behavior).
	AllPredecessor NodeTriggerMode = iota
	// AnyPredecessor fires when any predecessor completes (Eino AnyPredecessor).
	AnyPredecessor
)

// WithNodeTriggerMode sets trigger mode for listed nodes at compile time.
func WithNodeTriggerMode(mode NodeTriggerMode, nodes ...string) GraphCompileOption {
	return func(c *graphCompileConfig) {
		if c.nodeTrigger == nil {
			c.nodeTrigger = map[string]NodeTriggerMode{}
		}
		for _, n := range nodes {
			c.nodeTrigger[n] = mode
		}
	}
}

func applyNodeTriggerModes(g *Graph, cfg *graphCompileConfig) {
	if g == nil || cfg == nil || len(cfg.nodeTrigger) == 0 {
		return
	}
	for n, mode := range cfg.nodeTrigger {
		if mode == AnyPredecessor {
			g.MarkAnyPredecessorJoin(n)
		}
	}
}
