package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/LingByte/LingVoice/pkg/llm/internal/httputil"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// TaskStreamEvent is one SSE event from tasks/resubscribe or message/stream.
type TaskStreamEvent struct {
	Status   *TaskStatusUpdateEvent   `json:"status,omitempty"`
	Artifact *TaskArtifactUpdateEvent `json:"artifact,omitempty"`
}

// SubscribeToTask opens an SSE stream via JSON-RPC tasks/resubscribe.
func (c *Client) SubscribeToTask(ctx context.Context, baseURL, taskID string) (*schema.StreamReader[TaskStreamEvent], error) {
	return c.subscribeTask(ctx, baseURL, taskID, MethodTasksResubscribe)
}

// ResubscribeToTask is an alias for SubscribeToTask (A2A spec name).
func (c *Client) ResubscribeToTask(ctx context.Context, baseURL, taskID string) (*schema.StreamReader[TaskStreamEvent], error) {
	return c.SubscribeToTask(ctx, baseURL, taskID)
}

func (c *Client) subscribeTask(ctx context.Context, baseURL, taskID, method string) (*schema.StreamReader[TaskStreamEvent], error) {
	if c == nil || c.HTTPClient == nil {
		return nil, fmt.Errorf("a2a: nil client")
	}
	if taskID == "" {
		return nil, fmt.Errorf("a2a: task id required")
	}
	card, _ := c.FetchCard(ctx, baseURL)
	endpoint := c.rpcEndpoint(baseURL, card)

	outSR, outSW := schema.Pipe[TaskStreamEvent](16)
	go func() {
		defer outSW.Close()
		headers := sseHeaders(c.Auth)
		err := httputil.PostSSE(ctx, c.HTTPClient, endpoint, headers, JSONRPCRequest{
			JSONRPC: JSONRPCVersion,
			ID:      json.RawMessage(`"sub"`),
			Method:  method,
			Params:  mustMarshal(TaskIDParams{ID: taskID}),
		}, func(data string) error {
			ev, final, err := parseTaskStreamData(data)
			if err != nil {
				return err
			}
			if ev != nil {
				outSW.Send(*ev, nil)
			}
			if final {
				return io.EOF
			}
			return nil
		})
		if err != nil && !errors.Is(err, io.EOF) {
			outSW.Send(TaskStreamEvent{}, err)
		}
	}()
	return outSR, nil
}

func parseTaskStreamData(data string) (*TaskStreamEvent, bool, error) {
	var rpc JSONRPCResponse
	if err := json.Unmarshal([]byte(data), &rpc); err != nil {
		return nil, false, err
	}
	if rpc.Error != nil {
		return nil, false, fmt.Errorf("a2a: stream error: %s", rpc.Error.Message)
	}
	raw, err := json.Marshal(rpc.Result)
	if err != nil {
		return nil, false, err
	}
	var art TaskArtifactUpdateEvent
	if json.Unmarshal(raw, &art) == nil && art.Kind == "artifact-update" {
		return &TaskStreamEvent{Artifact: &art}, art.LastChunk, nil
	}
	var status TaskStatusUpdateEvent
	if json.Unmarshal(raw, &status) == nil && status.Kind == "status-update" {
		return &TaskStreamEvent{Status: &status}, status.Final, nil
	}
	return nil, false, nil
}

func sseHeaders(auth AuthConfig) map[string]string {
	headers := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "text/event-stream",
		"A2A-Version":  ProtocolVersion,
	}
	if auth.BearerToken != "" {
		headers["Authorization"] = "Bearer " + auth.BearerToken
	}
	if auth.APIKey != "" {
		header := auth.APIKeyHeader
		if header == "" {
			header = "X-API-Key"
		}
		headers[header] = auth.APIKey
	}
	return headers
}

// DeletePushNotificationConfig removes a task push config.
func (c *Client) DeletePushNotificationConfig(ctx context.Context, baseURL, taskID string) error {
	_, err := c.CallRPC(ctx, baseURL, MethodPushNotificationDelete, PushNotificationDeleteParams{TaskID: taskID})
	return err
}

// ListPushNotificationConfigs lists push configs, optionally filtered by task ID.
func (c *Client) ListPushNotificationConfigs(ctx context.Context, baseURL, taskID string) (*PushNotificationListResult, error) {
	raw, err := c.CallRPC(ctx, baseURL, MethodPushNotificationList, PushNotificationListParams{TaskID: taskID})
	if err != nil {
		return nil, err
	}
	var result PushNotificationListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListPushDeadLetters returns failed push deliveries from the agent dead-letter queue.
func (c *Client) ListPushDeadLetters(ctx context.Context, baseURL string) (*PushDeadLetterListResult, error) {
	raw, err := c.CallRPC(ctx, baseURL, MethodPushDeadLetterList, struct{}{})
	if err != nil {
		return nil, err
	}
	var result PushDeadLetterListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RedrivePushDeadLetters retries dead-letter webhook deliveries.
func (c *Client) RedrivePushDeadLetters(ctx context.Context, baseURL, taskID string) (*PushDeadLetterRedriveResult, error) {
	raw, err := c.CallRPC(ctx, baseURL, MethodPushDeadLetterRedrive, PushDeadLetterRedriveParams{TaskID: taskID})
	if err != nil {
		return nil, err
	}
	var result PushDeadLetterRedriveResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
