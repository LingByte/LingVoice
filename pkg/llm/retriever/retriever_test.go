package retriever_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/retriever"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestInMemoryRetriever(t *testing.T) {
	r := &retriever.InMemoryRetriever{Docs: []*schema.Document{
		{ID: "a", Content: "Pregel BSP orchestration"},
		{ID: "b", Content: "Voice layer ASR TTS"},
	}}
	docs, err := r.Retrieve(context.Background(), "Pregel orchestration", 2)
	if err != nil || len(docs) == 0 || docs[0].ID != "a" {
		t.Fatalf("err=%v docs=%v", err, docs)
	}
}
