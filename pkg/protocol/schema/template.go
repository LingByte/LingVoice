package schema

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

// FormatType selects template rendering engine (Eino schema.FormatType subset).
type FormatType uint8

const (
	FormatFString    FormatType = 0
	FormatGoTemplate FormatType = 1
)

// MessagesTemplate renders zero or more messages from variables.
type MessagesTemplate interface {
	Format(ctx context.Context, vars map[string]any, format FormatType) ([]*Message, error)
}

var fstringVar = regexp.MustCompile(`\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)

// FormatContent renders a template string with variables.
func FormatContent(content string, vars map[string]any, format FormatType) (string, error) {
	switch format {
	case FormatFString:
		return formatFString(content, vars)
	case FormatGoTemplate:
		tmpl, err := template.New("msg").Option("missingkey=error").Parse(content)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		if err := tmpl.Execute(&b, vars); err != nil {
			return "", err
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("schema: unknown format type %d", format)
	}
}

func formatFString(content string, vars map[string]any) (string, error) {
	var missing []string
	out := fstringVar.ReplaceAllStringFunc(content, func(match string) string {
		key := match[1 : len(match)-1]
		v, ok := vars[key]
		if !ok {
			missing = append(missing, key)
			return match
		}
		return fmt.Sprint(v)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("schema: missing template variable(s): %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// Format renders a copy of m with templated content.
func (m *Message) Format(_ context.Context, vars map[string]any, format FormatType) ([]*Message, error) {
	if m == nil {
		return nil, nil
	}
	copied := m.Clone()
	if copied.Content != "" {
		text, err := FormatContent(copied.Content, vars, format)
		if err != nil {
			return nil, err
		}
		copied.Content = text
	}
	return []*Message{copied}, nil
}

// MessagesPlaceholder injects pre-built messages from vars[key].
type MessagesPlaceholder struct {
	Key      string
	Optional bool
}

// Placeholder returns a MessagesPlaceholder template.
func Placeholder(key string, optional bool) MessagesPlaceholder {
	return MessagesPlaceholder{Key: key, Optional: optional}
}

func (p MessagesPlaceholder) Format(_ context.Context, vars map[string]any, _ FormatType) ([]*Message, error) {
	raw, ok := vars[p.Key]
	if !ok || raw == nil {
		if p.Optional {
			return nil, nil
		}
		return nil, fmt.Errorf("schema: missing placeholder %q", p.Key)
	}
	switch v := raw.(type) {
	case []*Message:
		return append([]*Message(nil), v...), nil
	case []Message:
		out := make([]*Message, len(v))
		for i := range v {
			c := v[i]
			out[i] = &c
		}
		return out, nil
	default:
		return nil, fmt.Errorf("schema: placeholder %q must be []*Message", p.Key)
	}
}
