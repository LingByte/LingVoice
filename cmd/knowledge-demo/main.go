// Command knowledge-demo: strategy-based chunking preview (no external services).
//
// For real Qdrant indexing see cmd/knowledge-qdrant-demo.
//
//	go run ./cmd/knowledge-demo
//	go run ./cmd/knowledge-demo -dir ./docs
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/LingByte/LingVoice/pkg/knowledge"
)

func main() {
	dir := flag.String("dir", "", "optional directory of .md/.txt files to chunk")
	flag.Parse()

	fmt.Fprintln(os.Stderr, "demo: knowledge-demo | strategy routing chunk preview (dry-run)")

	store := &printHandler{}
	idx, err := knowledge.NewIndexerFromConfig(knowledge.IndexerConfig{Handler: store})
	if err != nil {
		fail(err)
	}

	samples := defaultSamples()
	if *dir != "" {
		loaded, err := knowledge.LoadDocumentsFromDir(*dir, &knowledge.LoadOptions{
			Strategy: knowledge.ChunkStrategyAuto,
			Source:   *dir,
		})
		if err != nil {
			fail(err)
		}
		samples = loaded
	}

	results, err := idx.IndexDocuments(context.Background(), samples, &knowledge.IndexOptions{
		Namespace: "demo",
		Chunk:     &knowledge.ChunkOptions{MaxChars: 120, OverlapChars: 20},
	})
	if err != nil {
		fail(err)
	}
	for _, r := range results {
		fmt.Printf("doc=%s chunks=%d strategy=%s type=%s\n", r.DocumentID, r.ChunkCount, r.Strategy, r.DocType)
	}
}

func defaultSamples() []knowledge.DocumentInput {
	return []knowledge.DocumentInput{
		{
			ID: "manual", Title: "Pregel Guide", Strategy: knowledge.ChunkStrategyStructured,
			Content: "# Pregel\n\nBSP supersteps run until quiescence.\n\n## Channels\n\nMailbox and LastValue channels.",
		},
		{
			ID: "resume", Title: "Candidate", Strategy: knowledge.ChunkStrategyTableKV,
			Content: strings.Join([]string{
				"name: Ling", "title: Engineer", "skills: Go, LLM", "years: 8",
				"location: Shanghai", "email: ling@example.com",
			}, "\n"),
		},
		{
			ID: "auto-table", Title: "Team",
			Content: "| name | role |\n| --- | --- |\n| Ada | lead |\n| Bob | dev |",
		},
	}
}

type printHandler struct{}

func (printHandler) Provider() string { return "print" }

func (printHandler) Upsert(_ context.Context, records []knowledge.Record, _ *knowledge.UpsertOptions) error {
	for _, r := range records {
		strategy, _ := r.Metadata["chunk_strategy"].(string)
		fmt.Printf("  [%s] %s (%d chars)\n", strategy, r.ID, len([]rune(r.Content)))
	}
	return nil
}

func (printHandler) Query(context.Context, string, *knowledge.QueryOptions) ([]knowledge.QueryResult, error) {
	return nil, nil
}
func (printHandler) Get(context.Context, []string, *knowledge.GetOptions) ([]knowledge.Record, error) {
	return nil, nil
}
func (printHandler) List(context.Context, *knowledge.ListOptions) (*knowledge.ListResult, error) {
	return nil, nil
}
func (printHandler) Delete(context.Context, []string, *knowledge.DeleteOptions) error { return nil }
func (printHandler) Ping(context.Context) error                                     { return nil }
func (printHandler) CreateNamespace(context.Context, string) error                  { return nil }
func (printHandler) DeleteNamespace(context.Context, string) error                  { return nil }
func (printHandler) ListNamespaces(context.Context) ([]string, error)             { return nil, nil }

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
