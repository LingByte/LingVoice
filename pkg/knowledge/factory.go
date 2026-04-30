package knowledge

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/utils/base"
)

// envFirst returns the first non-empty trimmed value from the given env keys.
func envFirst(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(base.GetEnv(k)); v != "" {
			return v
		}
	}
	return ""
}

func envCSVList(keys ...string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, k := range keys {
		raw := strings.TrimSpace(base.GetEnv(k))
		if raw == "" {
			continue
		}
		for _, p := range strings.Split(raw, ",") {
			if s := strings.TrimSpace(p); s != "" {
				if _, ok := seen[s]; ok {
					continue
				}
				seen[s] = struct{}{}
				out = append(out, s)
			}
		}
	}
	return out
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

// NormalizeKnowledgeProvider returns ProviderQdrant or ProviderMilvus, or the trimmed lower input if unknown.
func NormalizeKnowledgeProvider(s string) string {
	v := strings.TrimSpace(strings.ToLower(s))
	switch v {
	case "", ProviderQdrant:
		return ProviderQdrant
	case ProviderMilvus:
		return ProviderMilvus
	default:
		return v
	}
}

// NewKnowledgeHandler returns a backend implementation for the given namespace configuration.
func NewKnowledgeHandler(p HandlerFactoryParams) (KnowledgeHandler, error) {
	ns := strings.TrimSpace(p.Namespace)
	switch prov := NormalizeKnowledgeProvider(p.Provider); prov {
	case ProviderQdrant:
		if ns == "" {
			return nil, errors.New("namespace (Qdrant collection name) is required")
		}
		qh := qdrantHandlerFromEnv()
		qh.Embedder = p.Embedder
		return qh, nil
	case ProviderMilvus:
		if ns == "" {
			return nil, errors.New("namespace (Milvus collection name) is required")
		}
		mh := milvusHandlerFromEnv()
		mh.Embedder = p.Embedder
		return mh, nil
	default:
		return nil, fmt.Errorf("unsupported knowledge provider %q (use %s or %s)", p.Provider, ProviderQdrant, ProviderMilvus)
	}
}

func qdrantHandlerFromEnv() *QdrantHandler {
	baseURL := strings.TrimSpace(base.GetEnv("QDRANT_BASEURL"))
	apiKey := strings.TrimSpace(base.GetEnv("QDRANT_API_KEY"))
	timeoutSec := int64(15)
	if raw := strings.TrimSpace(base.GetEnv("QDRANT_TIMEOUT_SECONDS")); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			timeoutSec = n
		}
	}
	return &QdrantHandler{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
		Embedder:   nil,
	}
}

func milvusHandlerFromEnv() *MilvusHandler {
	addr := strings.TrimSpace(base.GetEnv("MILVUS_ADDRESS")) // host:port
	user := strings.TrimSpace(base.GetEnv("MILVUS_USERNAME"))
	pass := strings.TrimSpace(base.GetEnv("MILVUS_PASSWORD"))
	token := strings.TrimSpace(base.GetEnv("MILVUS_TOKEN"))
	db := strings.TrimSpace(base.GetEnv("MILVUS_DB"))
	return &MilvusHandler{
		Address:  addr,
		Username: user,
		Password: pass,
		Token:    token,
		DBName:   db,
		Embedder: nil,
		cli:      nil,
	}
}
