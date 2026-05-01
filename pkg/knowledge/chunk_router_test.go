package knowledge

import (
	"context"
	"testing"
)

type stubDetector struct{ dt DocumentType }

func (s stubDetector) DetectDocumentType(ctx context.Context, text string) (DocumentType, error) {
	_ = ctx
	_ = text
	return s.dt, nil
}

type stubChunker struct{ provider string }

func (s stubChunker) Provider() string { return s.provider }
func (s stubChunker) Chunk(ctx context.Context, text string, opts *ChunkOptions) ([]Chunk, error) {
	_ = ctx
	_ = opts
	return []Chunk{{Title: s.provider, Text: text}}, nil
}

func TestRoutingChunker_RoutesByType(t *testing.T) {
	rc := &RoutingChunker{
		Detector:  stubDetector{dt: DocumentTypeTableKV},
		Structured: stubChunker{provider: "s"},
		TableKV:    stubChunker{provider: "t"},
		LLM:        stubChunker{provider: "l"},
	}
	out, err := rc.Chunk(context.Background(), "x", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
	if out[0].Title != "t" {
		t.Fatalf("expected TableKV chunker, got %q", out[0].Title)
	}
}

