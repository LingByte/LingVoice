package rag_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/rag"
	"github.com/LingByte/LingVoice/pkg/llm/retriever"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestRAGChain(t *testing.T) {
	chain, err := rag.New(rag.Config{
		Retriever: &retriever.InMemoryRetriever{Docs: []*schema.Document{
			{Content: "Graph supports ProcessState and mailbox channels."},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := chain.BuildMessages(context.Background(), "ProcessState")
	if err != nil || len(msgs) != 2 {
		t.Fatalf("err=%v msgs=%d", err, len(msgs))
	}
	if msgs[0].Role != schema.System || msgs[1].Role != schema.User {
		t.Fatalf("roles=%v %v", msgs[0].Role, msgs[1].Role)
	}
}
