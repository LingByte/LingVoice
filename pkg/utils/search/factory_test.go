package search

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewDefault(t *testing.T) {
	indexPath := filepath.Join(os.TempDir(), "test_search_factory_"+time.Now().Format("20060102150405"))
	defer os.RemoveAll(indexPath)

	e, err := NewDefault(Config{IndexPath: indexPath, DefaultAnalyzer: "standard", DefaultSearchFields: []string{"title", "body"}})
	if err != nil {
		t.Fatalf("NewDefault failed: %v", err)
	}
	if e == nil {
		t.Fatalf("expected engine")
	}
	_ = e.Close()
}

func TestNewDefault_MissingIndexPath(t *testing.T) {
	_, err := NewDefault(Config{})
	if err == nil {
		t.Fatalf("expected error")
	}
}
