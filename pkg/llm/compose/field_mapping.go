package compose

import (
	"fmt"
	"reflect"
)

// FieldMapping maps one field path to another (Eino compose.FieldMapping subset).
type FieldMapping struct {
	FromNode string
	From     string
	To       string
}

// FromField creates a mapping from a source field path.
func FromField(path string) FieldMapping {
	return FieldMapping{From: path}
}

// FromNodeField maps a field from a predecessor workflow node output.
func FromNodeField(node, path string) FieldMapping {
	return FieldMapping{FromNode: node, From: path}
}

// ToField sets the destination field path.
func (m FieldMapping) ToField(path string) FieldMapping {
	m.To = path
	return m
}

// MapFields copies values between maps using dot-path field names.
func MapFields(from, to map[string]any, mappings ...FieldMapping) error {
	if to == nil {
		return fmt.Errorf("compose: nil target map")
	}
	for _, m := range mappings {
		if m.From == "" || m.To == "" {
			continue
		}
		v, ok := getPath(from, m.From)
		if !ok {
			continue
		}
		if err := setPath(to, m.To, v); err != nil {
			return err
		}
	}
	return nil
}

func getPath(m map[string]any, path string) (any, bool) {
	if m == nil {
		return nil, false
	}
	cur := any(m)
	for _, seg := range splitPath(path) {
		asMap, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := asMap[seg]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

func setPath(m map[string]any, path string, value any) error {
	segs := splitPath(path)
	if len(segs) == 0 {
		return fmt.Errorf("compose: empty path")
	}
	cur := m
	for _, seg := range segs[:len(segs)-1] {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
	cur[segs[len(segs)-1]] = value
	return nil
}

func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	out := []string{}
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			if i > start {
				out = append(out, path[start:i])
			}
			start = i + 1
		}
	}
	if start < len(path) {
		out = append(out, path[start:])
	}
	return out
}

// MapStructToMap copies exported struct fields into a map by json tag or field name.
func MapStructToMap(src any) map[string]any {
	out := map[string]any{}
	v := reflect.ValueOf(src)
	for v.IsValid() && v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return out
	}
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		key := f.Name
		if tag := f.Tag.Get("json"); tag != "" && tag != "-" {
			key = tag
		}
		out[key] = v.Field(i).Interface()
	}
	return out
}
