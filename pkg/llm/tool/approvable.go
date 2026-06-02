package tool

import (
	"context"
	"fmt"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// ApprovalInfo is shown to a human when a tool needs approval.
type ApprovalInfo struct {
	ToolName        string `json:"tool_name"`
	ArgumentsInJSON string `json:"arguments"`
	ToolCallID      string `json:"tool_call_id,omitempty"`
}

// ApprovalResult is the human decision passed on resume.
type ApprovalResult struct {
	Approved         bool    `json:"approved"`
	DisapproveReason *string `json:"disapprove_reason,omitempty"`
}

// ApprovableTool wraps an InvokableTool with HITL approval (Eino approvableTool pattern).
type ApprovableTool struct {
	Inner InvokableTool
}

// WrapApprovable returns a tool that interrupts before running Inner.
func WrapApprovable(inner InvokableTool) *ApprovableTool {
	return &ApprovableTool{Inner: inner}
}

func (a *ApprovableTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	if a == nil || a.Inner == nil {
		return nil, fmt.Errorf("tool: nil approvable tool")
	}
	return a.Inner.Info(ctx)
}

func (a *ApprovableTool) InvokableRun(ctx context.Context, argumentsInJSON string) (string, error) {
	if a == nil || a.Inner == nil {
		return "", fmt.Errorf("tool: nil approvable tool")
	}
	info, err := a.Info(ctx)
	if err != nil {
		return "", err
	}

	was, _, stored := GetInterruptState[string](ctx)
	if !was {
		return "", StatefulInterrupt(ctx, &ApprovalInfo{
			ToolName:        info.Name,
			ArgumentsInJSON: argumentsInJSON,
			ToolCallID:      ToolCallID(ctx),
		}, argumentsInJSON)
	}

	intrID := ActiveInterruptID(ctx)
	target, has, decision := GetResumeContext[*ApprovalResult](ctx, intrID)
	if !target || !has {
		return "", StatefulInterrupt(ctx, &ApprovalInfo{
			ToolName:        info.Name,
			ArgumentsInJSON: stored,
			ToolCallID:      ToolCallID(ctx),
		}, stored)
	}
	if decision.Approved {
		return a.Inner.InvokableRun(ctx, stored)
	}
	if decision.DisapproveReason != nil {
		return fmt.Sprintf("tool %q disapproved: %s", info.Name, *decision.DisapproveReason), nil
	}
	return fmt.Sprintf("tool %q disapproved", info.Name), nil
}

var _ InvokableTool = (*ApprovableTool)(nil)
