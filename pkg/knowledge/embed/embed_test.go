package embed_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/knowledge/embed"
)

func TestNew_NVIDIA(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{map[string]any{"embedding": []float64{0.1, 0.2}, "index": 0}},
		})
	}))
	defer srv.Close()

	e, err := embed.New(embed.Config{
		Provider: embed.ProviderNVIDIA,
		NVIDIA: embed.NVIDIAConfig{
			BaseURL: srv.URL, APIKey: "k", Model: "m",
			HTTPClient: &http.Client{Timeout: 2 * time.Second},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	vecs, err := e.Embed(context.Background(), []string{" hello "})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer k" {
		t.Fatalf("auth=%q", gotAuth)
	}
	if len(vecs) != 1 || len(vecs[0]) != 2 {
		t.Fatalf("vecs=%v", vecs)
	}
	if e.Provider() != embed.ProviderNVIDIA {
		t.Fatalf("provider=%q", e.Provider())
	}
}

func TestFromFloat32Client(t *testing.T) {
	e := embed.FromFloat32Client(embed.ProviderOpenAI, stubF32{})
	vecs, err := e.Embed(context.Background(), []string{"x"})
	if err != nil || len(vecs[0]) != 2 {
		t.Fatalf("vecs=%v err=%v", vecs, err)
	}
}

type stubF32 struct{}

func (stubF32) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0.5, 0.5}
	}
	return out, nil
}

func TestNew_OpenAIProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{
				map[string]any{"embedding": []float64{0.1}, "index": 0},
				map[string]any{"embedding": []float64{0.2}, "index": 1},
			},
		})
	}))
	defer srv.Close()
	e, err := embed.New(embed.Config{
		Provider: embed.ProviderOpenAI,
		OpenAI: embed.OpenAIConfig{
			BaseURL: srv.URL, APIKey: "k", Model: "m",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	vecs, err := e.Embed(context.Background(), []string{"a", "b"})
	if err != nil || len(vecs) != 2 {
		t.Fatalf("vecs=%v err=%v", vecs, err)
	}
	if strings.TrimSpace("") != "" {
		t.Fatal("unused import guard")
	}
}
