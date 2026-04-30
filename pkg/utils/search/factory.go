package search

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func NewDefault(cfg Config) (Engine, error) {
	if strings.TrimSpace(cfg.IndexPath) == "" {
		return nil, errors.New("IndexPath is required")
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = 5 * time.Second
	}
	if cfg.QueryTimeout <= 0 {
		cfg.QueryTimeout = 5 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 200
	}
	m := BuildIndexMapping(cfg.DefaultAnalyzer)
	return New(cfg, m)
}

func NewTempDefault(indexName string) (Engine, string, error) {
	if strings.TrimSpace(indexName) == "" {
		indexName = "search_index"
	}
	path := filepath.Join(os.TempDir(), "ling_"+indexName)
	cfg := Config{IndexPath: path, DefaultAnalyzer: "standard", DefaultSearchFields: []string{"title", "content", "description", "body"}}
	e, err := NewDefault(cfg)
	return e, path, err
}
