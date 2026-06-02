package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// CallRPC invokes a JSON-RPC method and returns the raw result.
func (c *Client) CallRPC(ctx context.Context, baseURL, method string, params any) (json.RawMessage, error) {
	if c == nil || c.HTTPClient == nil {
		return nil, fmt.Errorf("a2a: nil client")
	}
	card, _ := c.FetchCard(ctx, baseURL)
	endpoint := c.rpcEndpoint(baseURL, card)
	body, err := json.Marshal(JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  method,
		Params:  mustMarshal(params),
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, stringsReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", ProtocolVersion)
	ApplyAuthHeaders(req, c.Auth)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("a2a: unauthorized")
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out JSONRPCResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, fmt.Errorf("a2a: %s", out.Error.Message)
	}
	b, err := json.Marshal(out.Result)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// ListTasks calls tasks/list.
func (c *Client) ListTasks(ctx context.Context, baseURL string, params TaskListParams) (*TaskListResult, error) {
	raw, err := c.CallRPC(ctx, baseURL, MethodTasksList, params)
	if err != nil {
		return nil, err
	}
	var result TaskListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SetPushNotificationConfig registers a webhook for task updates.
func (c *Client) SetPushNotificationConfig(ctx context.Context, baseURL string, params PushNotificationSetParams) (*PushNotificationConfig, error) {
	raw, err := c.CallRPC(ctx, baseURL, MethodPushNotificationSet, params)
	if err != nil {
		return nil, err
	}
	var cfg PushNotificationConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// GetPushNotificationConfig reads a task push config.
func (c *Client) GetPushNotificationConfig(ctx context.Context, baseURL, taskID string) (*PushNotificationConfig, error) {
	raw, err := c.CallRPC(ctx, baseURL, MethodPushNotificationGet, PushNotificationGetParams{TaskID: taskID})
	if err != nil {
		return nil, err
	}
	var cfg PushNotificationConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// CancelTask cancels a task via JSON-RPC tasks/cancel.
func (c *Client) CancelTask(ctx context.Context, baseURL, taskID string) (*Task, error) {
	raw, err := c.CallRPC(ctx, baseURL, MethodTasksCancel, TaskIDParams{ID: taskID})
	if err != nil {
		return nil, err
	}
	var task Task
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, err
	}
	return &task, nil
}
