// Package tool defines executable tools for LLM orchestration (Eino components/tool subset).
package tool

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// BaseTool exposes metadata for ChatModel.WithTools.
type BaseTool interface {
	Info(ctx context.Context) (*schema.ToolInfo, error)
}

// InvokableTool is a tool ToolsNode can execute.
type InvokableTool interface {
	BaseTool
	InvokableRun(ctx context.Context, argumentsInJSON string) (string, error)
}

// CollectInfos gathers ToolInfo from tools for model binding.
func CollectInfos(ctx context.Context, tools []InvokableTool) ([]*schema.ToolInfo, error) {
	out := make([]*schema.ToolInfo, 0, len(tools))
	for i, t := range tools {
		if t == nil {
			return nil, fmt.Errorf("tool: nil tool at index %d", i)
		}
		info, err := t.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("tool %d info: %w", i, err)
		}
		if info == nil || info.Name == "" {
			return nil, fmt.Errorf("tool %d: empty name", i)
		}
		out = append(out, info)
	}
	return out, nil
}
