package knowledge

import (
	"context"
	"errors"
	"strings"
)

// RoutingChunker chooses a chunking strategy based on detected DocumentType.
//
// - Structured: deterministic rule chunking (headings -> paragraphs -> sentences -> fallback)
// - Table/KV: table-preserving record chunking
// - Unstructured: LLM chunking (existing implementation)
type RoutingChunker struct {
	Detector DocumentTypeDetector

	Structured Chunker
	TableKV    Chunker
	LLM        Chunker
}

func (c *RoutingChunker) Provider() string { return "router" }

func (c *RoutingChunker) Chunk(ctx context.Context, text string, opts *ChunkOptions) ([]Chunk, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, ErrEmptyText
	}
	if c == nil {
		return nil, errors.New("chunker is nil")
	}
	d := c.Detector
	if d == nil {
		d = &RuleBasedDocumentTypeDetector{}
	}
	dt, err := d.DetectDocumentType(ctx, text)
	if err != nil {
		return nil, err
	}
	var ch Chunker
	switch dt {
	case DocumentTypeStructured:
		ch = c.Structured
	case DocumentTypeTableKV:
		ch = c.TableKV
	case DocumentTypeUnstructured:
		ch = c.LLM
	default:
		ch = c.Structured
	}

	if ch == nil {
		return nil, ErrChunkerNotFound
	}
	out, err := ch.Chunk(ctx, text, opts)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNoChunks
	}
	for i := range out {
		out[i].Index = i
		out[i].Text = strings.TrimSpace(out[i].Text)
	}
	return out, nil
}

var _ Chunker = (*RoutingChunker)(nil)

