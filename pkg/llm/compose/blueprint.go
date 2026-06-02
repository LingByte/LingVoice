package compose

import (
	"fmt"

	"github.com/LingByte/LingVoice/pkg/knowledge"
	pmedi "github.com/LingByte/LingVoice/pkg/protocol/media"
)

// GraphBlueprint assembles a compose Graph from capability flags.
// Scenarios differ only by which capabilities are enabled, not separate graph forks.
type GraphBlueprint struct {
	Name       string
	Caps       pmedi.CapabilitySet
	RAG        *knowledge.Service
	RAGPreamble string
	EchoPrefix  string
}

// Build compiles a linear graph: input → [rag] → [script] → reply → END.
func (b *GraphBlueprint) Build() (*Graph, error) {
	if b == nil {
		return nil, fmt.Errorf("compose: nil graph blueprint")
	}
	name := b.Name
	if name == "" {
		name = "av-blueprint"
	}
	g := NewGraph(name)
	if err := g.AddUtteranceInputNode("input", ""); err != nil {
		return nil, err
	}
	prev := "input"

	if b.Caps.Has(pmedi.CapRAG) && b.RAG != nil {
		if err := g.AddKnowledgeRAGNode("rag", b.RAG, nil, nil, b.RAGPreamble, pmedi.ChannelText); err != nil {
			return nil, err
		}
		if err := g.AddEdge(prev, "rag"); err != nil {
			return nil, err
		}
		prev = "rag"
	}

	if b.Caps.Has(pmedi.CapScript) {
		if err := g.AddScriptVarsNode("script"); err != nil {
			return nil, err
		}
		if err := g.AddEdge(prev, "script"); err != nil {
			return nil, err
		}
		prev = "script"
	}

	prefix := b.EchoPrefix
	if prefix == "" {
		prefix = "助手："
	}
	if err := g.AddEchoReplyNode("reply", prefix); err != nil {
		return nil, err
	}
	if err := g.AddEdge(prev, "reply"); err != nil {
		return nil, err
	}

	if b.Caps.Has(pmedi.CapHandoff) {
		if err := g.AddHandoffGateNode("handoff"); err != nil {
			return nil, err
		}
		if err := g.AddEdge("reply", "handoff"); err != nil {
			return nil, err
		}
		prev = "handoff"
	} else {
		prev = "reply"
	}

	if err := g.AddEdge(START, "input"); err != nil {
		return nil, err
	}
	if err := g.AddEdge(prev, END); err != nil {
		return nil, err
	}
	return g, nil
}

// BuildCompiled returns a compiled graph ready for GraphRunner.
func (b *GraphBlueprint) BuildCompiled() (*CompiledGraph, error) {
	g, err := b.Build()
	if err != nil {
		return nil, err
	}
	return g.Compile()
}
