// Command rag-demo: RAG retrieval + compose node (local, no API key).
//
//	go run ./cmd/rag-demo
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/rag"
	"github.com/LingByte/LingVoice/pkg/llm/retriever"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func main() {
	fmt.Fprintln(os.Stderr, "demo: rag-demo | in-memory RAG + compose graph")

	mem := &retriever.InMemoryRetriever{Docs: []*schema.Document{
		{ID: "1", Content: "LingVoice implements eino-style LLM orchestration with Graph and Chain."},
		{ID: "2", Content: "Pregel BSP supports fan-out and mailbox fan-in channels."},
		{ID: "3", Content: "Voice ASR/TTS is planned for L3 layer."},
	}}
	chain, err := rag.New(rag.Config{Retriever: mem, TopK: 2})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	model := llm.NewFuncModel("mock", func(_ context.Context, msgs []*schema.Message, _ llm.Options) (*schema.Message, error) {
		ctx := ""
		for _, m := range msgs {
			if m != nil && m.Role == schema.System {
				ctx = m.PlainText()
				break
			}
		}
		return schema.AssistantMessage("answer based on: "+ctx[:min(80, len(ctx))]+"...", nil), nil
	}, nil)

	g := compose.NewGraph("rag-demo")
	_ = g.AddRAGNode("retrieve", chain, "query")
	_ = g.AddChatModelNode("answer", model)
	_ = g.AddEdge(compose.START, "retrieve")
	_ = g.AddEdge("retrieve", "answer")
	_ = g.AddEdge("answer", compose.END)

	cg, err := g.Compile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	st, _, err := cg.Invoke(context.Background(), []*schema.Message{schema.UserMessage("What is Pregel BSP?")}, compose.WithStateModifier(func(_ context.Context, st *compose.GraphState) error {
		if st.Vars == nil {
			st.Vars = map[string]any{}
		}
		st.Vars["query"] = "Pregel BSP mailbox"
		return nil
	}))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(compose.FinalMessage(st).PlainText())
	if docs, ok := st.Vars["rag_context"].([]*schema.Document); ok {
		fmt.Fprintln(os.Stderr, "retrieved docs:", len(docs))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
