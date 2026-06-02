package compose

// NodeKind classifies graph nodes for introspection (Eino GraphInfo subset).
type NodeKind string

const (
	NodeKindLambda     NodeKind = "lambda"
	NodeKindChatModel  NodeKind = "chat_model"
	NodeKindTools      NodeKind = "tools"
	NodeKindTemplate   NodeKind = "template"
	NodeKindInput      NodeKind = "input"
	NodeKindReactEmbed NodeKind = "react_embed"
	NodeKindPassthrough NodeKind = "passthrough"
)

func (g *Graph) markNodeKind(name string, kind NodeKind) {
	if g == nil || name == "" || kind == "" {
		return
	}
	if g.nodeKinds == nil {
		g.nodeKinds = map[string]NodeKind{}
	}
	g.nodeKinds[name] = kind
}

func cloneNodeKinds(m map[string]NodeKind) map[string]NodeKind {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]NodeKind, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func reactNodeNames(r *CompiledGraph) []string {
	if r == nil || !r.HasReActRuntime() {
		return nil
	}
	return []string{NodeChatModel, NodeTools}
}
