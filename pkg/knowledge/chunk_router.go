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

	strategy := ChunkStrategyAuto
	if opts != nil && opts.Strategy != "" {
		strategy = opts.Strategy
	}

	var (
		ch Chunker
		dt DocumentType
	)
	if strategy != ChunkStrategyAuto {
		var err error
		ch, dt, err = c.chunkerForStrategy(strategy)
		if err != nil {
			return nil, err
		}
	} else {
		d := c.Detector
		if d == nil {
			d = &RuleBasedDocumentTypeDetector{}
		}
		var err error
		dt, err = d.DetectDocumentType(ctx, text)
		if err != nil {
			return nil, err
		}
		ch = c.chunkerForType(dt)
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
	strategyName := strategyNameFor(ch, strategy, dt)
	for i := range out {
		out[i].Index = i
		out[i].Text = strings.TrimSpace(out[i].Text)
		if out[i].Metadata == nil {
			out[i].Metadata = map[string]any{}
		}
		out[i].Metadata["chunk_strategy"] = strategyName
		out[i].Metadata["document_type"] = dt.String()
	}
	return out, nil
}

func (c *RoutingChunker) chunkerForType(dt DocumentType) Chunker {
	switch dt {
	case DocumentTypeStructured:
		return c.Structured
	case DocumentTypeTableKV:
		return c.TableKV
	case DocumentTypeUnstructured:
		if c.LLM != nil {
			return c.LLM
		}
		return c.Structured
	default:
		return c.Structured
	}
}

func (c *RoutingChunker) chunkerForStrategy(strategy ChunkStrategy) (Chunker, DocumentType, error) {
	switch strategy {
	case ChunkStrategyStructured:
		if c.Structured == nil {
			return nil, DocumentTypeStructured, ErrChunkerNotFound
		}
		return c.Structured, DocumentTypeStructured, nil
	case ChunkStrategyTableKV:
		if c.TableKV == nil {
			return nil, DocumentTypeTableKV, ErrChunkerNotFound
		}
		return c.TableKV, DocumentTypeTableKV, nil
	case ChunkStrategyLLM:
		if c.LLM == nil {
			if c.Structured != nil {
				return c.Structured, DocumentTypeUnstructured, nil
			}
			return nil, DocumentTypeUnstructured, ErrChunkerNotFound
		}
		return c.LLM, DocumentTypeUnstructured, nil
	default:
		return nil, DocumentTypeUnknown, ErrInvalidChunkOpt
	}
}

func strategyNameFor(ch Chunker, forced ChunkStrategy, dt DocumentType) string {
	if forced != "" && forced != ChunkStrategyAuto {
		return string(forced)
	}
	if ch != nil {
		return ch.Provider()
	}
	return dt.String()
}

// DefaultRoutingChunker builds a [RoutingChunker] with [RuleBasedDocumentTypeDetector],
// [StructuredRuleChunker], [TableKVChunker], and an optional LLM arm for unstructured text.
// When llm is nil, unstructured documents fall back to structured rule chunking.
func DefaultRoutingChunker(llm Chunker) *RoutingChunker {
	return &RoutingChunker{
		Detector:   &RuleBasedDocumentTypeDetector{},
		Structured: &StructuredRuleChunker{},
		TableKV:    &TableKVChunker{},
		LLM:        llm,
	}
}

var _ Chunker = (*RoutingChunker)(nil)
