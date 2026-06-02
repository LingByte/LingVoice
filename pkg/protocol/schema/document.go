package schema

// Document is a retrievable text unit (Eino schema.Document subset).
type Document struct {
	ID       string            `json:"id,omitempty"`
	Content  string            `json:"content"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Score    float64           `json:"score,omitempty"`
}

// DocumentsPlainText joins document contents for prompt injection.
func DocumentsPlainText(docs []*Document) string {
	if len(docs) == 0 {
		return ""
	}
	var out string
	for i, d := range docs {
		if d == nil || d.Content == "" {
			continue
		}
		if i > 0 {
			out += "\n\n"
		}
		out += d.Content
	}
	return out
}
