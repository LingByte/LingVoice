package knowledge

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

type fakeEmbedder struct {
	dim int
}

func (f fakeEmbedder) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	out := make([][]float64, 0, len(inputs))
	for _, in := range inputs {
		vec := make([]float64, f.dim)
		ln := float64(len(in))
		for i := 0; i < f.dim; i++ {
			vec[i] = ln + float64(i+1)
		}
		out = append(out, vec)
	}
	return out, nil
}

func qdrantReachable(t *testing.T, baseURL string, client *http.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/collections", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("qdrant not reachable at %s: %v", baseURL, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		t.Skipf("qdrant not ready at %s: status=%d", baseURL, resp.StatusCode)
	}
}

func TestQdrant_RealRequests_Localhost6333_UpsertAndQuery(t *testing.T) {
	const (
		baseURL = "http://localhost:6333"
		dim     = 3
	)

	client := &http.Client{Timeout: 10 * time.Second}
	qdrantReachable(t, baseURL, client)

	collection := "lingvoice_test_" + time.Now().UTC().Format("20060102_150405")

	qh := &QdrantHandler{
		BaseURL:    baseURL,
		Collection: collection,
		HTTPClient: client,
		Embedder:   fakeEmbedder{dim: dim},
	}
	defer func() { _ = qh.DeleteNamespace(context.Background(), collection) }()

	now := time.Now().UTC()
	records := []Record{
		{ID: "1", Content: "hello", Source: "s1", Title: "t1", Tags: []string{"a"}, Metadata: map[string]any{"k": "v"}, CreatedAt: now, UpdatedAt: now},
		// 注意：fakeEmbedder 基于字符串长度生成向量；world 与 hello 长度相同会导致向量并列，top1 不稳定。
		{ID: "2", Content: "world!", Source: "s2", Title: "t2", Tags: []string{"b"}, Metadata: map[string]any{"k": "v2"}, CreatedAt: now, UpdatedAt: now},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := qh.Upsert(ctx, records, &UpsertOptions{Namespace: collection, BatchSize: 10}); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	got, err := qh.Query(ctx, "hello", &QueryOptions{Namespace: collection, TopK: 1, MinScore: 0})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) mismatch: got=%d want=1", len(got))
	}
	if got[0].Record.ID != "1" {
		t.Fatalf("record.id mismatch: got=%s want=1", got[0].Record.ID)
	}
	if got[0].Record.Content != "hello" {
		t.Fatalf("record.content mismatch: got=%s want=hello", got[0].Record.Content)
	}
	if got[0].Record.Metadata["k"] != "v" {
		t.Fatalf("metadata.k mismatch: got=%v want=v", got[0].Record.Metadata["k"])
	}
}

func TestQdrant_RealRequests_Localhost6333_CreateNamespace(t *testing.T) {
	const (
		baseURL = "http://localhost:6333"
		dim     = 3
	)

	client := &http.Client{Timeout: 10 * time.Second}
	qdrantReachable(t, baseURL, client)
	ns := "lingvoice_test_ns_" + time.Now().UTC().Format("20060102_150405")
	qh := &QdrantHandler{
		BaseURL:    baseURL,
		Collection: ns,
		HTTPClient: client,
		Embedder:   fakeEmbedder{dim: dim},
	}
	defer func() { _ = qh.DeleteNamespace(context.Background(), ns) }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := qh.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("CreateNamespace failed: %v", err)
	}
	namespaces, err := qh.ListNamespaces(ctx)
	if err != nil {
		t.Fatalf("ListNamespace failed: %v", err)
	}
	for i := range namespaces {
		fmt.Println(namespaces[i])
	}
}
