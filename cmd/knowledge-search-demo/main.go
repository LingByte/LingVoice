// Command knowledge-search-demo: Bleve keyword retrieval + strategy chunking (no vector DB).
//
//	go run ./cmd/knowledge-search-demo -index /tmp/lv-search -query "mailbox"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/LingByte/LingVoice/pkg/knowledge"
	"github.com/LingByte/LingVoice/pkg/knowledge/retrieve"
	"github.com/LingByte/LingVoice/pkg/llm/rag"
	"github.com/LingByte/LingVoice/pkg/search"
)

func main() {
	indexPath := flag.String("index", "", "Bleve index path (required)")
	query := flag.String("query", "mailbox channel", "search query")
	topK := flag.Int("topk", 3, "topK")
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

	// Keyword-only: vector handler is a no-op store; retrieval uses Bleve.
	store := knowledge.NoopHandler{}
	docs := []knowledge.DocumentInput{
		{ID: "g1", Title: "Graph", Strategy: knowledge.ChunkStrategyStructured,
			Content: "# Graph\n\nPregel BSP uses mailbox channels between supersteps."},
		{ID: "v1", Title: "Voice", Strategy: knowledge.ChunkStrategyStructured,
			Content: "# Voice\n\nASR and TTS belong to the L3 media layer."},
	}

	chain, err := rag.NewKnowledgeChain(ctx, docs, rag.KnowledgeConfig{
		Handler:          store,
		Search:           engine,
		RetrieveStrategy: retrieve.StrategyKeyword,
		Namespace:        "local",
		TopK:             *topK,
		Chunk:            &knowledge.ChunkOptions{MaxChars: 120},
	})
	if err != nil {
		fail(err)
	}
	msgs, err := chain.BuildMessages(ctx, *query)
	if err != nil {
		fail(err)
	}
	fmt.Println("query:", *query)
	for _, m := range msgs {
		if m != nil {
			fmt.Printf("[%s]\n%s\n", m.Role, m.PlainText())
		}
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
