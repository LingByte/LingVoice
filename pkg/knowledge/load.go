package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadOptions configures directory ingestion.
type LoadOptions struct {
	// Extensions limits files loaded (default .md, .txt, .markdown).
	Extensions []string
	// Strategy applied to every loaded document when non-empty.
	Strategy ChunkStrategy
	// Source label stored on each document.
	Source string
}

// LoadDocumentsFromDir reads text/markdown files into DocumentInput values.
func LoadDocumentsFromDir(dir string, opts *LoadOptions) ([]DocumentInput, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("knowledge: dir is required")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("knowledge: %q is not a directory", dir)
	}
	exts := loadExtensions(opts)
	var docs []DocumentInput
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !hasLoadExtension(path, exts) {
			return nil
		}
		doc, err := LoadDocumentFromFile(path, opts)
		if err != nil {
			return fmt.Errorf("knowledge: load %q: %w", path, err)
		}
		docs = append(docs, doc)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return docs, nil
}

// LoadDocumentFromFile reads one file into a DocumentInput.
func LoadDocumentFromFile(path string, opts *LoadOptions) (DocumentInput, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return DocumentInput{}, fmt.Errorf("knowledge: path is required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return DocumentInput{}, err
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	title := base
	content := string(b)
	if strings.HasPrefix(content, "# ") {
		if line, _, ok := strings.Cut(content, "\n"); ok {
			title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	doc := DocumentInput{
		ID:      base,
		Title:   title,
		Source:  filepath.Dir(path),
		Content: content,
	}
	if opts != nil {
		if opts.Source != "" {
			doc.Source = opts.Source
		}
		if opts.Strategy != "" {
			doc.Strategy = opts.Strategy
		}
	}
	return doc, nil
}

func loadExtensions(opts *LoadOptions) map[string]struct{} {
	exts := opts.Extensions
	if len(exts) == 0 {
		exts = []string{".md", ".txt", ".markdown"}
	}
	set := make(map[string]struct{}, len(exts))
	for _, e := range exts {
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		set[strings.ToLower(e)] = struct{}{}
	}
	return set
}

func hasLoadExtension(path string, exts map[string]struct{}) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := exts[ext]
	return ok
}
