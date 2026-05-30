package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDocumentsFromDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# Alpha\n\ncontent a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skip.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	docs, err := LoadDocumentsFromDir(dir, &LoadOptions{Strategy: ChunkStrategyStructured, Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ID != "a" || docs[0].Title != "Alpha" || docs[0].Strategy != ChunkStrategyStructured {
		t.Fatalf("docs=%+v", docs)
	}
}

func TestNoopHandlerImplementsInterface(t *testing.T) {
	var _ KnowledgeHandler = NoopHandler{}
	if (NoopHandler{}).Provider() != "noop" {
		t.Fatal("provider")
	}
}
