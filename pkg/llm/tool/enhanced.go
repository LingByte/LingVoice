package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// EnhancedInvokableTool returns structured multimodal results (Eino EnhancedInvokableTool).
type EnhancedInvokableTool interface {
	BaseTool
	EnhancedInvokableRun(ctx context.Context, argumentsInJSON string) (*schema.ToolResult, error)
}

// InferEnhancedTool builds an EnhancedInvokableTool from a typed function.
func InferEnhancedTool[T, D any](name, desc string, fn func(ctx context.Context, input T) (D, error)) (*EnhancedFuncTool, error) {
	params, err := StructToToolParams[T]()
	if err != nil {
		return nil, err
	}
	info := &schema.ToolInfo{Name: name, Desc: desc, Params: params}
	return &EnhancedFuncTool{
		info: info,
		fn: func(ctx context.Context, argumentsInJSON string) (*schema.ToolResult, error) {
			var in T
			if argumentsInJSON != "" && argumentsInJSON != "{}" {
				if err := json.Unmarshal([]byte(argumentsInJSON), &in); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			out, err := fn(ctx, in)
			if err != nil {
				return nil, err
			}
			switch v := any(out).(type) {
			case *schema.ToolResult:
				return v, nil
			case schema.ToolResult:
				return &v, nil
			case string:
				return &schema.ToolResult{Text: v}, nil
			default:
				b, err := json.Marshal(out)
				if err != nil {
					return nil, err
				}
				return &schema.ToolResult{Text: string(b)}, nil
			}
		},
	}, nil
}

// EnhancedFuncTool wraps a JSON runner returning ToolResult.
type EnhancedFuncTool struct {
	info *schema.ToolInfo
	fn   func(ctx context.Context, argumentsInJSON string) (*schema.ToolResult, error)
}

func (t *EnhancedFuncTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	if t == nil || t.info == nil {
		return nil, fmt.Errorf("tool: nil enhanced tool")
	}
	return t.info, nil
}

func (t *EnhancedFuncTool) EnhancedInvokableRun(ctx context.Context, argumentsInJSON string) (*schema.ToolResult, error) {
	if t == nil || t.fn == nil {
		return nil, fmt.Errorf("tool: nil enhanced runner")
	}
	return t.fn(ctx, argumentsInJSON)
}

// AsInvokable adapts EnhancedInvokableTool to InvokableTool for ToolNode.
func AsInvokable(t EnhancedInvokableTool) InvokableTool {
	if t == nil {
		return nil
	}
	return invokableAdapter{t: t}
}

type invokableAdapter struct{ t EnhancedInvokableTool }

func (a invokableAdapter) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return a.t.Info(ctx)
}

func (a invokableAdapter) InvokableRun(ctx context.Context, argumentsInJSON string) (string, error) {
	r, err := a.t.EnhancedInvokableRun(ctx, argumentsInJSON)
	if err != nil {
		return "", err
	}
	if r == nil {
		return "", nil
	}
	return r.ToToolMessageContent(), nil
}
