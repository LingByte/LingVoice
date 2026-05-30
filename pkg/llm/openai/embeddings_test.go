package openai_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/openai"
)

func TestEmbeddingModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3],"index":0}]}`))
	}))
	defer srv.Close()

	m, err := openai.NewEmbeddingModel(openai.EmbeddingConfig{
		APIKey:  "test",
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	vecs, err := m.Embed(context.Background(), []string{"hello"})
	if err != nil || len(vecs) != 1 || len(vecs[0]) != 3 {
		t.Fatalf("err=%v vecs=%v", err, vecs)
	}
}
