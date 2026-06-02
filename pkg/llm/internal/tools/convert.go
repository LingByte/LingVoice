package tools

import (
	"encoding/json"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// OpenAITool builds an OpenAI Chat Completions tool definition.
func OpenAITool(info *schema.ToolInfo) (map[string]any, error) {
	if info == nil {
		return nil, nil
	}
	params, err := info.ParamsJSONSchema()
	if err != nil {
		return nil, err
	}
	fn := map[string]any{
		"name":        info.Name,
		"description": info.Desc,
	}
	if len(params) > 0 {
		var schemaObj any
		if err := json.Unmarshal(params, &schemaObj); err != nil {
			return nil, err
		}
		fn["parameters"] = schemaObj
	} else {
		fn["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return map[string]any{
		"type":     "function",
		"function": fn,
	}, nil
}

// AnthropicTool builds an Anthropic Messages API tool definition.
func AnthropicTool(info *schema.ToolInfo) (map[string]any, error) {
	if info == nil {
		return nil, nil
	}
	params, err := info.ParamsJSONSchema()
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"name":         info.Name,
		"description":  info.Desc,
		"input_schema": map[string]any{"type": "object", "properties": map[string]any{}},
	}
	if len(params) > 0 {
		var schemaObj map[string]any
		if err := json.Unmarshal(params, &schemaObj); err != nil {
			return nil, err
		}
		out["input_schema"] = schemaObj
	}
	return out, nil
}

// MergeTools returns per-call tools if set, otherwise defaults.
func MergeTools(defaults, perCall []*schema.ToolInfo) []*schema.ToolInfo {
	if len(perCall) > 0 {
		return perCall
	}
	return defaults
}

// OpenAIToolChoice maps schema tool choice to OpenAI tool_choice value.
func OpenAIToolChoice(c schema.ToolChoice, hasTools bool) any {
	if !hasTools {
		return nil
	}
	switch c {
	case schema.ToolChoiceForbidden:
		return "none"
	case schema.ToolChoiceForced:
		return "required"
	default:
		return "auto"
	}
}

// AnthropicToolChoice maps schema tool choice to Anthropic tool_choice type.
func AnthropicToolChoice(c schema.ToolChoice, hasTools bool) map[string]any {
	if !hasTools {
		return nil
	}
	switch c {
	case schema.ToolChoiceForbidden:
		return map[string]any{"type": "none"}
	case schema.ToolChoiceForced:
		return map[string]any{"type": "any"}
	default:
		return map[string]any{"type": "auto"}
	}
}
