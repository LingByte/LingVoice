package knowledge

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/knowledge/embed"
	"github.com/LingByte/LingVoice/pkg/search"
)

// QdrantConfig configures a Qdrant HTTP backend (all fields are explicit; no env lookup).
type QdrantConfig struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
}

// MilvusConfig configures a Milvus gRPC backend (all fields are explicit; no env lookup).
type MilvusConfig struct {
	Address  string
	Username string
	Password string
	Token    string
	DBName   string
}

// HandlerConfig selects and configures a KnowledgeHandler.
type HandlerConfig struct {
	Provider string
	Qdrant   QdrantConfig
	Milvus   MilvusConfig
	Embedder Embedder
	Embed    embed.Config
}

// IndexerConfig configures strategy-based chunking and indexing.
type IndexerConfig struct {
	Handler  KnowledgeHandler
	Chunker  Chunker
	LLM      TextCompleter
	LLMModel string
	Search   search.Engine
}

// NewHandler returns a vector-store backend from explicit configuration.
func NewHandler(cfg HandlerConfig) (KnowledgeHandler, error) {
	embedder, err := resolveEmbedder(cfg)
	if err != nil {
		return nil, err
	}
	switch cfg.Provider {
	case ProviderQdrant:
		return NewQdrantHandler(cfg.Qdrant, embedder)
	case ProviderMilvus:
		return NewMilvusHandler(cfg.Milvus, embedder)
	case ProviderMemory:
		return NewMemoryHandler(embedder)
	default:
		return nil, fmt.Errorf("unsupported knowledge provider %q (use %s, %s or %s)", cfg.Provider, ProviderQdrant, ProviderMilvus, ProviderMemory)
	}
}

func resolveEmbedder(cfg HandlerConfig) (Embedder, error) {
	if cfg.Embedder != nil {
		return cfg.Embedder, nil
	}
	hasEmbed := cfg.Embed.Provider != "" || cfg.Embed.Client != nil ||
		cfg.Embed.OpenAI.APIKey != "" || cfg.Embed.NVIDIA.APIKey != "" || cfg.Embed.Func.Fn != nil
	if !hasEmbed {
		return nil, nil
	}
	return embed.New(cfg.Embed)
}

// NewQdrantHandler builds a Qdrant handler from cfg.
func NewQdrantHandler(cfg QdrantConfig, embedder Embedder) (*QdrantHandler, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, ErrBaseURL
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &QdrantHandler{
		BaseURL:    strings.TrimSpace(cfg.BaseURL),
		APIKey:     strings.TrimSpace(cfg.APIKey),
		HTTPClient: &http.Client{Timeout: timeout},
		Embedder:   embedder,
	}, nil
}

// NewMilvusHandler builds a Milvus handler from cfg.
func NewMilvusHandler(cfg MilvusConfig, embedder Embedder) (*MilvusHandler, error) {
	if strings.TrimSpace(cfg.Address) == "" {
		return nil, fmt.Errorf("milvus: address is required")
	}
	return &MilvusHandler{
		Address:  strings.TrimSpace(cfg.Address),
		Username: strings.TrimSpace(cfg.Username),
		Password: strings.TrimSpace(cfg.Password),
		Token:    strings.TrimSpace(cfg.Token),
		DBName:   strings.TrimSpace(cfg.DBName),
		Embedder: embedder,
	}, nil
}

// NewIndexerFromConfig builds an indexer with optional LLM-backed chunking and search indexing.
func NewIndexerFromConfig(cfg IndexerConfig) (*Indexer, error) {
	if cfg.Handler == nil {
		return nil, fmt.Errorf("knowledge: nil handler")
	}
	chunker := cfg.Chunker
	if chunker == nil {
		var llmChunker Chunker
		if cfg.LLM != nil {
			llmChunker = &LLMChunker{LLM: cfg.LLM, Model: cfg.LLMModel}
		}
		chunker = DefaultRoutingChunker(llmChunker)
	}
	idx, err := NewIndexer(cfg.Handler, chunker)
	if err != nil {
		return nil, err
	}
	idx.Search = cfg.Search
	return idx, nil
}
