// Command knowledge-hybrid-demo: in-memory vector + Bleve hybrid + optional func rerank (no API key).
//
//	go run ./cmd/knowledge-hybrid-demo -index /tmp/lv-hybrid -query "pregel mailbox"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/LingByte/LingVoice/pkg/knowledge"
	"github.com/LingByte/LingVoice/pkg/knowledge/embed"
	"github.com/LingByte/LingVoice/pkg/knowledge/retrieve"
	"github.com/LingByte/LingVoice/pkg/llm/rag"
	"github.com/LingByte/LingVoice/pkg/search"
)

func main() {
	indexPath := flag.String("index", "", "Bleve index path (required)")
	query := flag.String("query", "pregel mailbox", "RAG query")
	topK := flag.Int("topk", 3, "topK")
	rerank := flag.Bool("rerank", false, "apply length-based func rerank")
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

	embedder, err := embed.New(embed.Config{
		Provider: embed.ProviderFunc,
		Func: embed.FuncConfig{
			Fn: func(_ context.Context, texts []string) ([][]float64, error) {
				out := make([][]float64, len(texts))
				for i, t := range texts {
					out[i] = hashVec(t, 16)
				}
				return out, nil
			},
		},
	})
	if err != nil {
		fail(err)
	}

	handler, err := knowledge.NewHandler(knowledge.HandlerConfig{
		Provider: knowledge.ProviderMemory,
		Embedder: embedder,
	})
	if err != nil {
		fail(err)
	}
	_ = handler.CreateNamespace(ctx, "local")

	docs := []knowledge.DocumentInput{
		{ID: "g1", Title: "Graph", Strategy: knowledge.ChunkStrategyStructured,
			Content: "# Graph\n\nPregel BSP uses mailbox channels between supersteps."},
		{ID: "v1", Title: "Voice", Strategy: knowledge.ChunkStrategyStructured,
			Content: "# Voice\n\nASR and TTS belong to the L3 media layer."},
	}

	ragCfg := rag.KnowledgeConfig{
		Handler:          handler,
		Search:           engine,
		RetrieveStrategy: retrieve.StrategyHybrid,
		Namespace:        "local",
		TopK:             *topK,
		VectorWeight:     0.6,
		Chunk:            &knowledge.ChunkOptions{MaxChars: 120},
		Preamble:         "Answer using hybrid retrieved context.",
	}
	if *rerank {
		ragCfg.Reranker, err = embed.NewRerank(embed.RerankConfig{
			Provider: embed.RerankProviderFunc,
			Func: embed.RerankFuncConfig{
				Fn: func(_ context.Context, _ string, documents []string, topN int) ([]embed.RerankResult, error) {
					type scored struct {
						i     int
						score float64
					}
									ranked := make([]scored, len(documents))
					for i, d := range documents {
						ranked[i] = scored{i: i, score: float64(len(d))}
					}
					sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
					if topN <= 0 || topN > len(ranked) {
						topN = len(ranked)
					}
					out := make([]embed.RerankResult, topN)
					for i := 0; i < topN; i++ {
						out[i] = embed.RerankResult{Index: ranked[i].i, Score: ranked[i].score}
					}
					return out, nil
				},
			},
		})
		if err != nil {
			fail(err)
		}
		ragCfg.RerankCandidates = *topK * 3
	}

	chain, err := rag.NewKnowledgeChain(ctx, docs, ragCfg)
	if err != nil {
		fail(err)
	}
	msgs, err := chain.BuildMessages(ctx, *query)
	if err != nil {
		fail(err)
	}
	fmt.Println("query:", *query, "strategy: hybrid+memory rerank=", *rerank)
	for _, m := range msgs {
		if m != nil {
			fmt.Printf("[%s]\n%s\n", m.Role, m.PlainText())
		}
	}
}

func hashVec(text string, dim int) []float64 {
	vec := make([]float64, dim)
	for i, r := range text {
		vec[i%dim] += float64(int(r)%97) / 97
	}
	return vec
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
