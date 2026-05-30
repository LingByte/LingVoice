package compose

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/llm/rag"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// AddRAGNode retrieves context and injects into GraphState.Vars["rag_context"].
func (g *Graph) AddRAGNode(name string, chain *rag.Chain, queryFromVars string) error {
	if chain == nil {
		return fmt.Errorf("compose: nil RAG chain for node %q", name)
	}
	key := queryFromVars
	if key == "" {
		key = "query"
	}
	return g.AddLambdaNode(name, func(ctx context.Context, st *GraphState) error {
		query, _ := st.Vars[key].(string)
		if query == "" {
			query = lastUserPlainText(st.Messages)
		}
		if query == "" {
			return fmt.Errorf("compose: RAG node %q: empty query", name)
		}
		docs, err := chain.Retrieve(ctx, query)
		if err != nil {
			return err
		}
		msgs, err := chain.BuildMessages(ctx, query)
		if err != nil {
			return err
		}
		if st.Vars == nil {
			st.Vars = map[string]any{}
		}
		st.Vars["rag_context"] = docs
		st.Vars["rag_query"] = query
		st.Messages = append([]*schema.Message(nil), msgs...)
		return nil
	})
}

func lastUserPlainText(msgs []*schema.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m != nil && m.Role == schema.User {
			return m.PlainText()
		}
	}
	return ""
}
