package knowledge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/utils/base"
)

const (
	// ProviderQdrant Qdrant Vector Database
	ProviderQdrant = "qdrant"

	// ProviderMilvus Milvus Vector Database
	ProviderMilvus = "milvus"
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

// HandlerFactoryParams selects and configures a KnowledgeHandler (reads Qdrant / Milvus settings from environment).
type HandlerFactoryParams struct {
	// Provider is ProviderQdrant or ProviderMilvus (see constants in this package).
	Provider string
	// Namespace is the Qdrant / Milvus collection name.
	Namespace string
	// Embedder is attached to the handler when Provider is Qdrant/Milvus (may be nil for delete-only calls).
	Embedder Embedder
}

// NewKnowledgeHandler returns a backend implementation for the given namespace configuration.
func NewKnowledgeHandler(p HandlerFactoryParams) (KnowledgeHandler, error) {
	switch p.Provider {
	case ProviderQdrant:
		qh := qdrantHandlerFromEnv()
		qh.Embedder = p.Embedder
		return qh, nil
	case ProviderMilvus:
		mh := milvusHandlerFromEnv()
		mh.Embedder = p.Embedder
		return mh, nil
	default:
		return nil, fmt.Errorf("unsupported knowledge provider %q (use %s or %s)", p.Provider, ProviderQdrant, ProviderMilvus)
	}
}

func qdrantHandlerFromEnv() *QdrantHandler {
	timeoutSec := int64(15)
	if raw := strings.TrimSpace(base.GetEnv("QDRANT_TIMEOUT_SECONDS")); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			timeoutSec = n
		}
	}
	return &QdrantHandler{
		BaseURL:    base.GetEnv("QDRANT_BASEURL"),
		APIKey:     base.GetEnv("QDRANT_API_KEY"),
		HTTPClient: &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
		Embedder:   nil,
	}
}

func milvusHandlerFromEnv() *MilvusHandler {
	return &MilvusHandler{
		Address:  base.GetEnv("MILVUS_ADDRESS"),
		Username: base.GetEnv("MILVUS_USERNAME"),
		Password: base.GetEnv("MILVUS_PASSWORD"),
		Token:    base.GetEnv("MILVUS_TOKEN"),
		DBName:   base.GetEnv("MILVUS_DB"),
		Embedder: nil,
		cli:      nil,
	}
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

type Embedder interface {

	// Embed embed inputs
	Embed(ctx context.Context, inputs []string) ([][]float64, error)
}
