package knowledge

import (
	"context"
	"errors"
	"time"

	"github.com/LingByte/LingVoice/pkg/knowledge/embed"
	"github.com/LingByte/LingVoice/pkg/utils"
)

const (
	// ProviderQdrant Qdrant Vector Database
	ProviderQdrant = "qdrant"

	// ProviderMilvus Milvus Vector Database
	ProviderMilvus = "milvus"

	// ProviderMemory in-process vector store for local demos/tests
	ProviderMemory = "memory"
)

var (
	ErrHandlerNotFound        = errors.New("handler not be null")
	ErrBaseURL                = errors.New("BaseURL is required")
	ErrCollectionNotFound     = errors.New("Collection is required")
	ErrEmbedderNotFound       = errors.New("Embedder is required")
	ErrRecordNotFound         = errors.New("record not found")
	ErrNamespaceNotFound      = errors.New("namespace not found")
	ErrInvalidVectorDimension = errors.New("invalid vector dimension")
	ErrEmptyQuery             = errors.New("empty query text")
	ErrEmptyText              = errors.New("empty text")
	ErrInvalidChunkOpt        = errors.New("invalid chunk options")
	ErrNoChunks               = errors.New("no chunks generated")
	ErrChunkerNotFound        = errors.New("no suitable chunker for document type")
)

type DocumentType int

const (
	DocumentTypeUnknown      DocumentType = iota
	DocumentTypeStructured                // 有标题、章节、段落（手册、论文、markdown）
	DocumentTypeTableKV                   // 表格、键值对、表单、简历
	DocumentTypeUnstructured              // 杂乱、OCR、无标点、无段落（必须 LLM）
)

func (d DocumentType) String() string {
	switch d {
	case DocumentTypeStructured:
		return "structured"
	case DocumentTypeTableKV:
		return "table_kv"
	case DocumentTypeUnstructured:
		return "unstructured"
	default:
		return "unknown"
	}
}

// ChunkStrategy selects which chunking arm to use.
// ChunkStrategyAuto runs document-type detection via RoutingChunker.
type ChunkStrategy string

const (
	ChunkStrategyAuto       ChunkStrategy = "auto"
	ChunkStrategyStructured ChunkStrategy = "structured"
	ChunkStrategyTableKV    ChunkStrategy = "table_kv"
	ChunkStrategyLLM        ChunkStrategy = "llm"
)

// Chunk is one retrieval-oriented segment produced by a Chunker.
type Chunk struct {
	Index    int
	Title    string
	Text     string
	Metadata map[string]any
}

// ChunkOptions controls chunk size, overlap and optional title metadata.
type ChunkOptions struct {
	// MaxChars is the target maximum characters per chunk. When 0, chunkers use their own defaults.
	MaxChars int
	// OverlapChars is the overlap size between consecutive chunks.
	// If set to -1, chunkers may disable overlap.
	OverlapChars int
	// MinChars is a lower bound; very small chunks may be dropped/merged.
	MinChars int

	DocumentTitle string

	// Strategy forces a chunking arm. Empty or ChunkStrategyAuto uses document-type routing.
	Strategy ChunkStrategy

	// PreChunkClean is passed to utils.CleanText before an LLM call.
	// If nil, some implementations will enable StripMarkdown and DedupLines by default.
	PreChunkClean *utils.CleanTextOptions
}

// Chunker splits long text into chunks (implementations may use deterministic rules or an LLM).
type Chunker interface {
	Provider() string
	Chunk(ctx context.Context, text string, opts *ChunkOptions) ([]Chunk, error)
}

// DocumentTypeDetector decides which chunking strategy should be used for a document.
type DocumentTypeDetector interface {
	DetectDocumentType(ctx context.Context, text string) (DocumentType, error)
}

type FilterOp string

const (
	FilterOpEqual       FilterOp = "$eq"
	FilterOpNotEqual    FilterOp = "$ne"
	FilterOpIn          FilterOp = "$in"
	FilterOpNotIn       FilterOp = "$nin"
	FilterOpGt          FilterOp = "$gt"
	FilterOpGte         FilterOp = "$gte"
	FilterOpLt          FilterOp = "$lt"
	FilterOpLte         FilterOp = "$lte"
	FilterOpContainsAll FilterOp = "$all"
	FilterOpContainsAny FilterOp = "$any"
)

type Filter struct {
	Field    string   `json:"field"`
	Operator FilterOp `json:"operator"`
	Value    []any    `json:"value"`
}

// Record 知识库记录
type Record struct {
	ID        string         `json:"id"`
	Source    string         `json:"source"` // 来源file/url/api etc.
	Title     string         `json:"title"`
	Content   string         `json:"content"` // 原文片段
	Vector    []float32      `json:"vector"`  // 向量
	Tags      []string       `json:"tags"`
	Metadata  map[string]any `json:"metadata"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type UpsertOptions struct {
	Namespace string
	Overwrite bool
	BatchSize int
}

type QueryOptions struct {
	Namespace string
	TopK      int
	MinScore  float64  // 分数阈值
	Filters   []Filter // 复杂过滤
	Model     string   // embedding 模型
}

type QueryResult struct {
	Record Record  `json:"record"`
	Score  float64 `json:"score"`
}

type GetOptions struct {
	Namespace string
}

type DeleteOptions struct {
	Namespace string
}

type ListOptions struct {
	Namespace string
	Limit     int
	Offset    string
	Filters   []Filter
	OrderBy   string // "created_at" "updated_at"
	OrderDir  string // "asc" "desc"
}

type ListResult struct {
	Records    []Record `json:"records"`
	NextOffset string   `json:"next_offset,omitempty"`
}

// KnowledgeHandler abstract knowledge interface
type KnowledgeHandler interface {
	Provider() string

	// Upsert write and update files
	Upsert(ctx context.Context, records []Record, opts *UpsertOptions) error

	// Query Query for txt
	Query(ctx context.Context, text string, opts *QueryOptions) ([]QueryResult, error)

	// Get get by id
	Get(ctx context.Context, ids []string, opts *GetOptions) ([]Record, error)

	// List list query for page
	List(ctx context.Context, opts *ListOptions) (*ListResult, error)

	// Delete delete file document
	Delete(ctx context.Context, ids []string, opts *DeleteOptions) error

	// Ping health check
	Ping(ctx context.Context) error

	// CreateNamespace create new namespace
	CreateNamespace(ctx context.Context, name string) error

	// DeleteNamespace delete namespack
	DeleteNamespace(ctx context.Context, name string) error

	// ListNamespaces List database namespace
	ListNamespaces(ctx context.Context) ([]string, error)
}

type Embedder = embed.Embedder
