package tool

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// InferTool builds an InvokableTool by inferring JSON schema from struct T (Eino InferTool subset).
func InferTool[T, D any](name, desc string, fn func(ctx context.Context, in T) (D, error)) (InvokableTool, error) {
	params, err := StructToToolParams[T]()
	if err != nil {
		return nil, err
	}
	info := &schema.ToolInfo{Name: name, Desc: desc, Params: params}
	return JSONFuncTool(info, fn), nil
}

// StructToToolParams reflects struct fields into schema.ToolParams.
func StructToToolParams[T any]() (*schema.ToolParams, error) {
	var zero T
	t := reflect.TypeOf(zero)
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("tool: infer schema requires struct type, got %v", t)
	}
	fields := make(map[string]*schema.ParameterInfo)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		jsonTag := f.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		name, omitEmpty := parseJSONTag(jsonTag, f.Name)
		if name == "" {
			continue
		}
		desc := f.Tag.Get("desc")
		if desc == "" {
			desc = f.Tag.Get("jsonschema_description")
		}
		pi, err := typeToParam(f.Type)
		if err != nil {
			return nil, fmt.Errorf("tool: field %q: %w", name, err)
		}
		pi.Desc = desc
		if !omitEmpty {
			pi.Required = true
		}
		fields[name] = pi
	}
	raw, err := (&schema.ToolInfo{Params: &schema.ToolParams{Fields: fields}}).ParamsJSONSchema()
	if err != nil {
		return nil, err
	}
	return &schema.ToolParams{JSONSchema: raw}, nil
}

func parseJSONTag(tag, fallback string) (name string, omitempty bool) {
	if tag == "" {
		return fallback, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = fallback
	}
	for _, p := range parts[1:] {
		if p == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty
}

func typeToParam(t reflect.Type) (*schema.ParameterInfo, error) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return &schema.ParameterInfo{Type: schema.String}, nil
	case reflect.Bool:
		return &schema.ParameterInfo{Type: schema.Boolean}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &schema.ParameterInfo{Type: schema.Integer}, nil
	case reflect.Float32, reflect.Float64:
		return &schema.ParameterInfo{Type: schema.Number}, nil
	case reflect.Slice, reflect.Array:
		elem, err := typeToParam(t.Elem())
		if err != nil {
			return nil, err
		}
		return &schema.ParameterInfo{Type: schema.Array, ElemInfo: elem}, nil
	case reflect.Map:
		return &schema.ParameterInfo{Type: schema.Object}, nil
	case reflect.Struct:
		sub := make(map[string]*schema.ParameterInfo)
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			n, _ := parseJSONTag(f.Tag.Get("json"), f.Name)
			if n == "" || f.Tag.Get("json") == "-" {
				continue
			}
			pi, err := typeToParam(f.Type)
			if err != nil {
				return nil, err
			}
			sub[n] = pi
		}
		return &schema.ParameterInfo{Type: schema.Object, SubParams: sub}, nil
	default:
		return nil, fmt.Errorf("unsupported type %s", t.Kind())
	}
}
