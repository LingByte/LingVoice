// Command rag-vector-demo: vector embedding RAG (local mock embedder).
//
//	go run ./cmd/rag-vector-demo
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/LingByte/LingVoice/pkg/llm/rag"
	"github.com/LingByte/LingVoice/pkg/llm/retriever"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func main() {
	fmt.Fprintln(os.Stderr, "demo: rag-vector-demo | vector embedding retriever")

	embedder := &retriever.FuncEmbedder{
		Dim: 8,
		Fn: func(_ context.Context, texts []string) ([][]float32, error) {
			out := make([][]float32, len(texts))
			for i, t := range texts {
				out[i] = hashEmbed(t, 8)
			}
			return out, nil
		},
	}
	docs := []*schema.Document{
		{ID: "1", Content: "Pregel BSP supports mailbox channels and superstep streaming."},
		{ID: "2", Content: "A2A agents support bearer auth and mTLS transport."},
		{ID: "3", Content: "Voice ASR TTS belongs to L3 layer."},
	}
	chain, err := rag.NewVectorChain(context.Background(), embedder, docs, rag.Config{TopK: 2})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	msgs, err := chain.BuildMessages(context.Background(), "pregel streaming mailbox")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(msgs[0].PlainText())
}

func hashEmbed(text string, dim int) []float32 {
	vec := make([]float32, dim)
	for i, r := range text {
		vec[i%dim] += float32(int(r)%97) / 97
	}
	return vec
}
