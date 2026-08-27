package cluster

// This file defines the JSON message types used by the HTTP-based peer relay
// protocol. The cluster uses a simple HTTP/JSON API instead of full gRPC to
// avoid the protoc toolchain dependency while remaining fully functional.
//
// 端点 (Endpoints):
//   POST /relay/heartbeat  - 心跳交换节点状态
//   POST /relay/media      - 转发媒体帧
//   POST /relay/signal     - 转发信令
//   POST /relay/lookup     - 查找流位置
//   POST /relay/register   - 注册远端流

// HeartbeatRequest 心跳请求
type HeartbeatRequest struct {
	NodeID       string `json:"node_id"`
	Address      string `json:"address"`
	LoadScore    int64  `json:"load_score"`
	SessionCount int64  `json:"session_count"`
	StreamCount  int64  `json:"stream_count"`
}

// HeartbeatResponse 心跳响应
type HeartbeatResponse struct {
	NodeID    string `json:"node_id"`
	LoadScore int64  `json:"load_score"`
}

// RelayMediaRequest 媒体转发请求
type RelayMediaRequest struct {
	FromNode  string `json:"from_node"`
	ToNode    string `json:"to_node"`
	StreamID  string `json:"stream_id"`
	TrackID   string `json:"track_id"`
	Payload   []byte `json:"payload"`
	Timestamp uint32 `json:"timestamp"`
	Sequence  uint32 `json:"sequence"`
	Codec     string `json:"codec"`
	IsVideo   bool   `json:"is_video"`
}

// RelayMediaResponse 媒体转发响应
type RelayMediaResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// RelaySignalRequest 信令转发请求
type RelaySignalRequest struct {
	FromNode   string `json:"from_node"`
	ToNode     string `json:"to_node"`
	StreamID   string `json:"stream_id"`
	SignalType string `json:"signal_type"`
	Payload    []byte `json:"payload"`
}

// RelaySignalResponse 信令转发响应
type RelaySignalResponse struct {
	Success bool   `json:"success"`
	Payload []byte `json:"payload"`
}

// LookupStreamRequest 流查找请求
type LookupStreamRequest struct {
	StreamID string `json:"stream_id"`
}

// LookupStreamResponse 流查找响应
type LookupStreamResponse struct {
	Found        bool     `json:"found"`
	NodeID       string   `json:"node_id"`
	ReplicaNodes []string `json:"replica_nodes"`
}

// RegisterStreamRequest 流注册请求 (远端节点告知本节点它持有某流)
type RegisterStreamRequest struct {
	StreamID string `json:"stream_id"`
	NodeID   string `json:"node_id"`
}

// RegisterStreamResponse 流注册响应
type RegisterStreamResponse struct {
	Success bool `json:"success"`
}

// MediaRelayHandler 处理从 peer 转发过来的媒体帧。
// 返回 error 表示本地处理失败。
type MediaRelayHandler func(req RelayMediaRequest) error

// SignalRelayHandler 处理从 peer 转发过来的信令。
// 返回的 payload 作为响应回传给发起方。
type SignalRelayHandler func(req RelaySignalRequest) ([]byte, error)
