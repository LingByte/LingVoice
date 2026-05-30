// Command knowledge-qdrant-demo: strategy chunking + Qdrant + optional Bleve hybrid retrieval.
//
// All settings via flags (pkg/knowledge does not read env).
//
//	go run ./cmd/knowledge-qdrant-demo \
//	  -qdrant http://127.0.0.1:6333 \
//	  -embed-base https://api.openai.com/v1 -embed-key $KEY \
//	  -search-index /tmp/lingvoice-bleve \
//	  -retrieve hybrid \
//	  -query "pregel mailbox"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/knowledge"
	"github.com/LingByte/LingVoice/pkg/knowledge/embed"
	"github.com/LingByte/LingVoice/pkg/knowledge/retrieve"
	"github.com/LingByte/LingVoice/pkg/llm/openai"
	"github.com/LingByte/LingVoice/pkg/llm/rag"
	"github.com/LingByte/LingVoice/pkg/search"
)

func main() {
	fs := flag.NewFlagSet("knowledge-qdrant-demo", flag.ExitOnError)
	qdrantURL := fs.String("qdrant", "", "Qdrant base URL (required)")
	qdrantKey := fs.String("qdrant-key", "", "Qdrant API key")
	namespace := fs.String("namespace", "lingvoice_demo", "Qdrant collection")
	embedBase := fs.String("embed-base", "", "Embedding API base URL (required)")
	embedKey := fs.String("embed-key", "", "Embedding API key (required)")
	embedModel := fs.String("embed-model", "text-embedding-3-small", "Embedding model")
	embedProvider := fs.String("embed-provider", "openai", "Embedding provider: openai|nvidia")
	llmBase := fs.String("llm-base", "", "LLM base URL for unstructured chunking (optional)")
	llmKey := fs.String("llm-key", "", "LLM API key (defaults to embed-key)")
	llmModel := fs.String("llm-model", "gpt-4o-mini", "LLM model")
	query := fs.String("query", "pregel mailbox streaming", "RAG query")
	topK := fs.Int("topk", 3, "retrieval topK")
	retrieveMode := fs.String("retrieve", "vector", "vector|keyword|hybrid")
	searchIndex := fs.String("search-index", "", "Bleve index path for keyword/hybrid")
	rerankBase := fs.String("rerank-base", "", "Rerank API base URL (optional, SiliconFlow-compatible)")
	rerankKey := fs.String("rerank-key", "", "Rerank API key")
	rerankModel := fs.String("rerank-model", "", "Rerank model")
	rerankCandidates := fs.Int("rerank-candidates", 0, "candidate pool before rerank (default topk*3)")
	_ = fs.Parse(os.Args[1:])

	if *qdrantURL == "" || *embedBase == "" || *embedKey == "" {
		fmt.Fprintln(os.Stderr, "required: -qdrant -embed-base -embed-key")
		os.Exit(2)
	}
	if *llmKey == "" {
		*llmKey = *embedKey
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	embedModelClient, err := openai.NewEmbeddingModel(openai.EmbeddingConfig{
		APIKey:  *embedKey,
		BaseURL: *embedBase,
		Model:   *embedModel,
	})
	if err != nil {
		fail(err)
	}
	embedCfg := embed.Config{Client: embedModelClient}
	switch strings.ToLower(*embedProvider) {
	case string(embed.ProviderNVIDIA):
		embedCfg = embed.Config{
			Provider: embed.ProviderNVIDIA,
			NVIDIA: embed.NVIDIAConfig{
				BaseURL: *embedBase, APIKey: *embedKey, Model: *embedModel,
			},
		}
	default:
		embedCfg = embed.Config{
			Provider: embed.ProviderOpenAI,
			OpenAI: embed.OpenAIConfig{
				BaseURL: *embedBase, APIKey: *embedKey, Model: *embedModel,
			},
		}
	}
	embedder, err := embed.New(embedCfg)
	if err != nil {
		fail(err)
	}

	handler, err := knowledge.NewHandler(knowledge.HandlerConfig{
		Provider: knowledge.ProviderQdrant,
		Qdrant: knowledge.QdrantConfig{
			BaseURL: *qdrantURL, APIKey: *qdrantKey, Timeout: 30 * time.Second,
		},
		Embedder: embedder,
	})
	if err != nil {
		fail(err)
	}
	if err := handler.Ping(ctx); err != nil {
		fail(fmt.Errorf("qdrant ping: %w", err))
	}
	_ = handler.CreateNamespace(ctx, *namespace)

	var searchEngine search.Engine
	if *searchIndex != "" {
		searchEngine, err = search.New(search.Config{
			IndexPath:           *searchIndex,
			DefaultSearchFields: []string{"title", "content", "body"},
			QueryTimeout:        10 * time.Second,
		}, search.BuildIndexMapping(""))
		if err != nil {
			fail(err)
		}
		defer searchEngine.Close()
	}

	var llmCompleter knowledge.TextCompleter
	if strings.TrimSpace(*llmBase) != "" {
		chat, err := openai.NewChatModel(openai.Config{
			APIKey: *llmKey, BaseURL: *llmBase, Model: *llmModel,
		})
		if err != nil {
			fail(err)
		}
		llmCompleter = &knowledge.ChatModelCompleter{Model: chat, Name: *llmModel}
	}

	strategy := retrieve.Strategy(*retrieveMode)
	if strategy == retrieve.StrategyKeyword || strategy == retrieve.StrategyHybrid {
		if searchEngine == nil {
			fail(fmt.Errorf("keyword/hybrid retrieval requires -search-index"))
		}
	}

	docs := []knowledge.DocumentInput{
		{
			ID: "pregel", Title: "Pregel", Source: "demo", Strategy: knowledge.ChunkStrategyStructured,
			Content: "# Pregel BSP\n\nGraphRun executes supersteps until quiescence.\n\n## Channels\n\nMailbox and LastValue channels coordinate node mail.",
		},
		{
			ID: "a2a", Title: "A2A", Source: "demo", Strategy: knowledge.ChunkStrategyStructured,
			Content: "# Agent2Agent\n\nJSON-RPC 2.0 over HTTP with SSE streaming for message/stream.",
		},
	}

	ragCfg := rag.KnowledgeConfig{
		Handler:          handler,
		Search:           searchEngine,
		RetrieveStrategy: strategy,
		Namespace:        *namespace,
		TopK:             *topK,
		LLM:              llmCompleter,
		LLMModel:         *llmModel,
		Chunk:            &knowledge.ChunkOptions{MaxChars: 200, OverlapChars: 30},
		Preamble:         "Answer using the indexed knowledge base context.",
		RerankCandidates: *rerankCandidates,
	}
	if strings.TrimSpace(*rerankBase) != "" {
		if *rerankKey == "" {
			*rerankKey = *embedKey
		}
		if *rerankModel == "" {
			fail(fmt.Errorf("rerank requires -rerank-model"))
		}
		reranker, err := embed.NewRerank(embed.RerankConfig{
			Provider: embed.RerankProviderSiliconFlow,
			SiliconFlow: embed.SiliconFlowRerankConfig{
				BaseURL: *rerankBase, APIKey: *rerankKey, Model: *rerankModel,
			},
		})
		if err != nil {
			fail(err)
		}
		ragCfg.Reranker = reranker
	}

	chain, err := rag.NewKnowledgeChain(ctx, docs, ragCfg)
	if err != nil {
		fail(err)
	}

	msgs, err := chain.BuildMessages(ctx, *query)
	if err != nil {
		fail(err)
	}
	fmt.Println("query:", *query, "retrieve:", strategy)
	for _, m := range msgs {
		if m == nil {
			continue
		}
		fmt.Printf("[%s]\n%s\n\n", m.Role, m.PlainText())
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
