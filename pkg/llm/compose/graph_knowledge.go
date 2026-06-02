package compose

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/knowledge"
	"github.com/LingByte/LingVoice/pkg/llm/rag"
)

// AddKnowledgeRAGNode indexes documents (when non-empty) and adds a RAG node backed by knowledge.Service.
func (g *Graph) AddKnowledgeRAGNode(name string, svc *knowledge.Service, docs []knowledge.DocumentInput, opts *knowledge.IndexOptions, preamble, queryFromVars string) error {
	if svc == nil {
		return fmt.Errorf("compose: nil knowledge service for node %q", name)
	}
	if len(docs) > 0 {
		if _, err := svc.IndexDocuments(context.Background(), docs, opts); err != nil {
			return fmt.Errorf("compose: index for RAG node %q: %w", name, err)
		}
	}
	chain, err := rag.ChainFromService(svc, preamble)
	if err != nil {
		return err
	}
	return g.AddRAGNode(name, chain, queryFromVars)
}
