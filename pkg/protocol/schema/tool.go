package schema

import (
	"encoding/json"
	"fmt"
	"sort"
)

// DataType is a JSON Schema primitive type for tool parameters.
type DataType string

const (
	Object  DataType = "object"
	Number  DataType = "number"
	Integer DataType = "integer"
	String  DataType = "string"
	Array   DataType = "array"
	Null    DataType = "null"
	Boolean DataType = "boolean"
)

// FunctionCall is the function payload inside a ToolCall.
type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ToolCall is a model-requested tool invocation on an assistant message.
type ToolCall struct {
	// Index identifies streaming chunks that belong to the same call.
	Index *int `json:"index,omitempty"`
	ID    string `json:"id"`
	Type  string `json:"type"`
	Function FunctionCall `json:"function"`
	Extra map[string]any `json:"extra,omitempty"`
}

// ParameterInfo describes one tool parameter (map-based schema).
type ParameterInfo struct {
	Type      DataType `json:"type"`
	ElemInfo  *ParameterInfo `json:"elem_info,omitempty"`
	SubParams map[string]*ParameterInfo `json:"sub_params,omitempty"`
	Desc      string `json:"desc,omitempty"`
	Enum      []string `json:"enum,omitempty"`
	Required  bool `json:"required,omitempty"`
}

// ToolParams holds a tool parameter schema as either a field map or raw JSON Schema.
type ToolParams struct {
	Fields     map[string]*ParameterInfo `json:"fields,omitempty"`
	JSONSchema json.RawMessage           `json:"json_schema,omitempty"`
}

// ToolInfo describes a tool exposed to the model.
type ToolInfo struct {
	Name   string `json:"name"`
	Desc   string `json:"desc"`
	Params *ToolParams `json:"params,omitempty"`
	Extra  map[string]any `json:"extra,omitempty"`
}

// ParamsJSONSchema returns parameters as JSON Schema bytes for provider adapters.
// When Fields is set, a minimal object schema is synthesized.
func (t *ToolInfo) ParamsJSONSchema() (json.RawMessage, error) {
	if t == nil || t.Params == nil {
		return nil, nil
	}
	if len(t.Params.JSONSchema) > 0 {
		return t.Params.JSONSchema, nil
	}
	if len(t.Params.Fields) == 0 {
		return nil, nil
	}
	sc := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []string{},
	}
	props := sc["properties"].(map[string]any)
	required := sc["required"].([]string)
	keys := make([]string, 0, len(t.Params.Fields))
	for k := range t.Params.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := t.Params.Fields[k]
		props[k] = paramInfoToMap(p)
		if p.Required {
			required = append(required, k)
		}
	}
	sc["required"] = required
	return json.Marshal(sc)
}

func paramInfoToMap(p *ParameterInfo) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	m := map[string]any{
		"type": string(p.Type),
	}
	if p.Desc != "" {
		m["description"] = p.Desc
	}
	if len(p.Enum) > 0 {
		m["enum"] = p.Enum
	}
	if p.ElemInfo != nil {
		m["items"] = paramInfoToMap(p.ElemInfo)
	}
	if len(p.SubParams) > 0 {
		subProps := map[string]any{}
		subRequired := []string{}
		subKeys := make([]string, 0, len(p.SubParams))
		for k := range p.SubParams {
			subKeys = append(subKeys, k)
		}
		sort.Strings(subKeys)
		for _, k := range subKeys {
			sp := p.SubParams[k]
			subProps[k] = paramInfoToMap(sp)
			if sp.Required {
				subRequired = append(subRequired, k)
			}
		}
		m["properties"] = subProps
		if len(subRequired) > 0 {
			m["required"] = subRequired
		}
	}
	return m
}

// ToolChoice controls whether the model may call tools.
type ToolChoice string

const (
	ToolChoiceForbidden ToolChoice = "forbidden" // OpenAI "none"
	ToolChoiceAllowed   ToolChoice = "allowed"   // OpenAI "auto"
	ToolChoiceForced    ToolChoice = "forced"    // OpenAI "required"
)

func concatToolCalls(chunks []ToolCall) ([]ToolCall, error) {
	var merged []ToolCall
	byIndex := make(map[int][]int)
	for i := range chunks {
		if chunks[i].Index == nil {
			merged = append(merged, chunks[i])
			continue
		}
		idx := *chunks[i].Index
		byIndex[idx] = append(byIndex[idx], i)
	}
	for index, positions := range byIndex {
		if len(positions) == 0 {
			continue
		}
		base := chunks[positions[0]]
		idx := index
		out := ToolCall{Index: &idx, ID: base.ID, Type: base.Type, Function: base.Function}
		var args string
		id, typ, name := base.ID, base.Type, base.Function.Name
		for _, pos := range positions {
			c := chunks[pos]
			if c.ID != "" {
				if id == "" {
					id = c.ID
				} else if id != c.ID {
					return nil, fmt.Errorf("schema: tool call id mismatch %q vs %q", id, c.ID)
				}
			}
			if c.Type != "" {
				if typ == "" {
					typ = c.Type
				} else if typ != c.Type {
					return nil, fmt.Errorf("schema: tool call type mismatch %q vs %q", typ, c.Type)
				}
			}
			if c.Function.Name != "" {
				if name == "" {
					name = c.Function.Name
				} else if name != c.Function.Name {
					return nil, fmt.Errorf("schema: tool call name mismatch %q vs %q", name, c.Function.Name)
				}
			}
			args += c.Function.Arguments
		}
		out.ID = id
		out.Type = typ
		out.Function.Name = name
		out.Function.Arguments = args
		merged = append(merged, out)
	}
	return merged, nil
}
