package knowledge

import (
	"context"
	"time"
)

const (
	// ProviderQdrant Qdrant Vector Database
	ProviderQdrant = "qdrant"

	// ProviderMilvus Milvus Vector Database
	ProviderMilvus = "milvus"
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

// Record 知识库记录（RAG 核心结构）
type Record struct {
	ID        string         `json:"id"`
	Source    string         `json:"source"` // 来源：file/url/api etc.
	Title     string         `json:"title"`
	Content   string         `json:"content"` // 原文片段
	Vector    []float32      `json:"vector"`  // 向量（必加）
	Tags      []string       `json:"tags"`
	Metadata  map[string]any `json:"metadata"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type UpsertOptions struct {
	Namespace string
	Overwrite bool
	BatchSize int // 大批量自动切分
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

type Embedder interface {

	// Embed embed inputs
	Embed(ctx context.Context, inputs []string) ([][]float64, error)
}
