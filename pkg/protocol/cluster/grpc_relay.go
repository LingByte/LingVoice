package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// GrpcRelayClient 持有到 peer 节点的 HTTP/JSON 连接。
//
// 虽然命名为 GrpcRelayClient, 但实际使用 HTTP/JSON 通信 (与 relay.go
// 定义的端点一致), 避免引入 proto 工具链依赖。每个 peer 节点对应一个
// GrpcRelayClient 实例, 由 Manager 持有并复用。
type GrpcRelayClient struct {
	peerAddr string // 如 "10.0.0.1:50051"
	baseURL  string // 如 "http://10.0.0.1:50051"
	client   *http.Client
}

// NewGrpcRelayClient 创建到 peer 节点的 relay 客户端。
// peerAddr 为 peer 的监听地址, 如 "10.0.0.1:50051"。
func NewGrpcRelayClient(peerAddr string) *GrpcRelayClient {
	return &GrpcRelayClient{
		peerAddr: peerAddr,
		baseURL:  "http://" + peerAddr,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// post 向 peer 发送 JSON POST 请求, 并将响应解码到 resp。
func (c *GrpcRelayClient) post(ctx context.Context, path string, req, resp any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", httpResp.StatusCode, url)
	}

	if err := json.NewDecoder(httpResp.Body).Decode(resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// SendHeartbeat 向 peer 发送心跳, 携带本节点状态。
func (c *GrpcRelayClient) SendHeartbeat(ctx context.Context, req HeartbeatRequest) (*HeartbeatResponse, error) {
	resp := &HeartbeatResponse{}
	if err := c.post(ctx, "/relay/heartbeat", req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// RelayMedia 向 peer 转发媒体帧。
func (c *GrpcRelayClient) RelayMedia(ctx context.Context, req RelayMediaRequest) (*RelayMediaResponse, error) {
	resp := &RelayMediaResponse{}
	if err := c.post(ctx, "/relay/media", req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// RelaySignal 向 peer 转发信令。
func (c *GrpcRelayClient) RelaySignal(ctx context.Context, req RelaySignalRequest) (*RelaySignalResponse, error) {
	resp := &RelaySignalResponse{}
	if err := c.post(ctx, "/relay/signal", req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// LookupStream 向 peer 查询流的位置信息。
func (c *GrpcRelayClient) LookupStream(ctx context.Context, req LookupStreamRequest) (*LookupStreamResponse, error) {
	resp := &LookupStreamResponse{}
	if err := c.post(ctx, "/relay/lookup", req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// RegisterStream 向 peer 注册远端流。
func (c *GrpcRelayClient) RegisterStream(ctx context.Context, req RegisterStreamRequest) (*RegisterStreamResponse, error) {
	resp := &RegisterStreamResponse{}
	if err := c.post(ctx, "/relay/register", req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// Close 关闭 HTTP client, 释放底层连接。
func (c *GrpcRelayClient) Close() {
	if c.client != nil {
		c.client.CloseIdleConnections()
	}
}
