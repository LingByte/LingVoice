package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// FuncTool wraps a function as InvokableTool.
type FuncTool struct {
	info *schema.ToolInfo
	fn   func(ctx context.Context, argumentsInJSON string) (string, error)
}

// NewFuncTool builds an InvokableTool from schema and runner.
func NewFuncTool(info *schema.ToolInfo, fn func(ctx context.Context, argumentsInJSON string) (string, error)) *FuncTool {
	return &FuncTool{info: info, fn: fn}
}

func (t *FuncTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	if t == nil || t.info == nil {
		return nil, fmt.Errorf("tool: nil info")
	}
	return t.info, nil
}

func (t *FuncTool) InvokableRun(ctx context.Context, argumentsInJSON string) (string, error) {
	if t == nil || t.fn == nil {
		return "", fmt.Errorf("tool: nil runner")
	}
	return t.fn(ctx, argumentsInJSON)
}

// JSONFuncTool decodes args into T, encodes D as JSON string result.
func JSONFuncTool[T, D any](info *schema.ToolInfo, fn func(ctx context.Context, input T) (D, error)) *FuncTool {
	return NewFuncTool(info, func(ctx context.Context, argumentsInJSON string) (string, error) {
		var in T
		if argumentsInJSON != "" && argumentsInJSON != "{}" {
			if err := json.Unmarshal([]byte(argumentsInJSON), &in); err != nil {
				return "", fmt.Errorf("decode args: %w", err)
			}
		}
		out, err := fn(ctx, in)
		if err != nil {
			return "", err
		}
		switch v := any(out).(type) {
		case string:
			return v, nil
		default:
			b, err := json.Marshal(out)
			if err != nil {
				return "", err
			}
			return string(b), nil
		}
	})
}

var _ InvokableTool = (*FuncTool)(nil)
