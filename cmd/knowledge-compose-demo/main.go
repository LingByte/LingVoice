// Command knowledge-compose-demo: knowledge Service + compose RAG graph (local Bleve, no API key).
//
//	go run ./cmd/knowledge-compose-demo -index /tmp/lv-compose
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/LingByte/LingVoice/pkg/knowledge"
	"github.com/LingByte/LingVoice/pkg/knowledge/retrieve"
	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/llm/rag"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
	"github.com/LingByte/LingVoice/pkg/search"
)

func main() {
	indexPath := flag.String("index", "", "Bleve index path (required)")
	query := flag.String("query", "pregel mailbox", "RAG query")
	flag.Parse()
	if *indexPath == "" {
		fmt.Fprintln(os.Stderr, "required: -index")
		os.Exit(2)
	}

	ctx := context.Background()
	engine, err := search.New(search.Config{
		IndexPath:           *indexPath,
		DefaultSearchFields: []string{"title", "content", "body"},
		QueryTimeout:        10 * time.Second,
	}, search.BuildIndexMapping(""))
	if err != nil {
		fail(err)
	}
	defer engine.Close()

	svc, err := knowledge.NewService(knowledge.ServiceConfig{
		Handler: knowledge.NoopHandler{},
		Indexer: knowledge.IndexerConfig{Search: engine},
		Retrieve: retrieve.Config{
			Strategy: retrieve.StrategyKeyword,
			Search:   engine,
			TopK:     2,
		},
		TopK: 2,
	})
	if err != nil {
		fail(err)
	}

	docs := []knowledge.DocumentInput{
		{ID: "g1", Title: "Graph", Strategy: knowledge.ChunkStrategyStructured,
			Content: "# Graph\n\nPregel BSP uses mailbox channels between supersteps."},
		{ID: "v1", Title: "Voice", Strategy: knowledge.ChunkStrategyStructured,
			Content: "# Voice\n\nASR and TTS belong to the L3 media layer."},
	}
	if _, err := svc.IndexDocuments(ctx, docs, &knowledge.IndexOptions{Chunk: &knowledge.ChunkOptions{MaxChars: 120}}); err != nil {
		fail(err)
	}

	chain, err := rag.ChainFromService(svc, "Answer using indexed knowledge context.")
	if err != nil {
		fail(err)
	}

	model := llm.NewFuncModel("mock", func(_ context.Context, msgs []*schema.Message, _ llm.Options) (*schema.Message, error) {
		ctxText := ""
		for _, m := range msgs {
			if m != nil && m.Role == schema.System {
				ctxText = m.PlainText()
				break
			}
		}
		if len(ctxText) > 96 {
			ctxText = ctxText[:96]
		}
		return schema.AssistantMessage("compose answer: "+ctxText, nil), nil
	}, nil)

	g := compose.NewGraph("knowledge-compose")
	_ = g.AddRAGNode("retrieve", chain, "query")
	_ = g.AddChatModelNode("answer", model)
	_ = g.AddEdge(compose.START, "retrieve")
	_ = g.AddEdge("retrieve", "answer")
	_ = g.AddEdge("answer", compose.END)

	cg, err := g.Compile()
	if err != nil {
		fail(err)
	}
	st, _, err := cg.Invoke(ctx, []*schema.Message{schema.UserMessage(*query)}, compose.WithStateModifier(func(_ context.Context, st *compose.GraphState) error {
		if st.Vars == nil {
			st.Vars = map[string]any{}
		}
		st.Vars["query"] = *query
		return nil
	}))
	if err != nil {
		fail(err)
	}
	fmt.Println(compose.FinalMessage(st).PlainText())
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
