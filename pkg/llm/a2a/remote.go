package a2a

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func jsonReader(b []byte) io.Reader {
	return bytes.NewReader(b)
}

// RemoteAgent adapts an A2A client for in-process delegation.
type RemoteAgent struct {
	Client  *Client
	BaseURL string
	Name    string
}

// Generate sends messages to the remote agent.
func (r *RemoteAgent) Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	if r == nil || r.Client == nil {
		return nil, fmt.Errorf("a2a: nil remote agent")
	}
	return r.Client.SendMessages(ctx, r.BaseURL, messages)
}

// Stream generates a streaming response from the remote agent.
func (r *RemoteAgent) Stream(ctx context.Context, messages []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
	if r == nil || r.Client == nil {
		return nil, fmt.Errorf("a2a: nil remote agent")
	}
	return r.Client.StreamMessages(ctx, r.BaseURL, messages)
}

// GetTask fetches remote task state.
func (r *RemoteAgent) GetTask(ctx context.Context, taskID string) (*Task, error) {
	if r == nil || r.Client == nil {
		return nil, fmt.Errorf("a2a: nil remote agent")
	}
	return r.Client.GetTask(ctx, r.BaseURL, taskID)
}

// ListTasks lists remote tasks.
func (r *RemoteAgent) ListTasks(ctx context.Context, params TaskListParams) (*TaskListResult, error) {
	if r == nil || r.Client == nil {
		return nil, fmt.Errorf("a2a: nil remote agent")
	}
	return r.Client.ListTasks(ctx, r.BaseURL, params)
}

// CancelTask cancels a remote task.
func (r *RemoteAgent) CancelTask(ctx context.Context, taskID string) (*Task, error) {
	if r == nil || r.Client == nil {
		return nil, fmt.Errorf("a2a: nil remote agent")
	}
	return r.Client.CancelTask(ctx, r.BaseURL, taskID)
}

// SubscribeToTask opens an SSE stream for task updates.
func (r *RemoteAgent) SubscribeToTask(ctx context.Context, taskID string) (*schema.StreamReader[TaskStreamEvent], error) {
	if r == nil || r.Client == nil {
		return nil, fmt.Errorf("a2a: nil remote agent")
	}
	return r.Client.SubscribeToTask(ctx, r.BaseURL, taskID)
}
