// Command knowledge-service-demo: knowledge.Service index/retrieve/update/delete lifecycle (local Bleve).
//
//	go run ./cmd/knowledge-service-demo -index /tmp/lv-service -dir ./docs
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/LingByte/LingVoice/pkg/knowledge"
	"github.com/LingByte/LingVoice/pkg/knowledge/retrieve"
	"github.com/LingByte/LingVoice/pkg/search"
)

func main() {
	indexPath := flag.String("index", "", "Bleve index path (required)")
	dir := flag.String("dir", "", "optional directory of .md/.txt files to index")
	query := flag.String("query", "pregel mailbox", "retrieval query")
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
			TopK:     3,
		},
		TopK: 3,
	})
	if err != nil {
		fail(err)
	}

	docs := defaultDocs()
	if *dir != "" {
		loaded, err := knowledge.LoadDocumentsFromDir(*dir, &knowledge.LoadOptions{
			Strategy: knowledge.ChunkStrategyStructured,
			Source:   *dir,
		})
		if err != nil {
			fail(err)
		}
		docs = loaded
	}

	opts := &knowledge.IndexOptions{Chunk: &knowledge.ChunkOptions{MaxChars: 160}}
	res, err := svc.IndexDocuments(ctx, docs, opts)
	if err != nil {
		fail(err)
	}
	fmt.Println("indexed:", len(res))

	hits, err := svc.Retrieve(ctx, *query, 3)
	if err != nil {
		fail(err)
	}
	fmt.Println("retrieve:", *query, "hits=", len(hits))
	for _, h := range hits {
		if h != nil {
			fmt.Printf("  - %s score=%.2f %q\n", h.ID, h.Score, truncate(h.Content, 60))
		}
	}

	if len(docs) > 0 {
		docs[0].Content = docs[0].Content + "\n\nUpdated section about streaming mailboxes."
		if _, err := svc.UpdateDocuments(ctx, []knowledge.DocumentInput{docs[0]}, opts); err != nil {
			fail(err)
		}
		fmt.Println("updated:", docs[0].ID)
		if err := svc.DeleteDocuments(ctx, []string{docs[0].ID}, opts); err != nil {
			fail(err)
		}
		fmt.Println("deleted:", docs[0].ID)
	}
}

func defaultDocs() []knowledge.DocumentInput {
	return []knowledge.DocumentInput{
		{ID: "g1", Title: "Graph", Strategy: knowledge.ChunkStrategyStructured,
			Content: "# Graph\n\nPregel BSP uses mailbox channels between supersteps."},
		{ID: "a2a", Title: "A2A", Strategy: knowledge.ChunkStrategyStructured,
			Content: "# A2A\n\nJSON-RPC tasks/resubscribe and push notification webhooks."},
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
