// Package cluster implements inter-node relay and cluster coordination
// for distributed LingVoice deployment.
//
// 集群架构:
//   - 节点发现: 静态配置或动态注册
//   - 流路由: 当本地节点没有某流时, 转发到有该流的节点
//   - 负载均衡: 新流按节点负载分配
//   - 心跳检测: 定期检测节点健康状态
//   - 故障转移: 节点故障时迁移流到其他节点
//
// 协议层职责 (Go):
//   - 节点注册/发现
//   - 流路由决策
//   - 跨节点信令转发
//   - 负载统计上报
//
// 媒体层职责 (Rust):
//   - 实际媒体帧跨节点传输
//   - RTP relay
//   - 转码中继
package cluster

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LingByte/ling-base/common/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// NodeStatus 节点状态
type NodeStatus int

const (
	NodeOnline  NodeStatus = iota // 在线
	NodeDegraded                  // 降级 (高负载或部分故障)
	NodeOffline                   // 离线
)

func (s NodeStatus) String() string {
	switch s {
	case NodeOnline:
		return "online"
	case NodeDegraded:
		return "degraded"
	case NodeOffline:
		return "offline"
	default:
		return "unknown"
	}
}

// NodeInfo 集群节点信息
type NodeInfo struct {
	ID          string
	Address     string // gRPC 地址, 如 "10.0.0.1:50051"
	Region      string // 区域/机房
	Status      NodeStatus
	LoadScore   atomic.Int64 // 负载分数 (0-100, 越低越好)
	SessionCount atomic.Int64
	StreamCount  atomic.Int64
	LastHeartbeat time.Time
	Metadata     map[string]string

	// relayClient 是到该 peer 的 HTTP/JSON relay 客户端, 懒创建并复用。
	// 仅对 peer 节点有意义, 本地节点为 nil。
	relayClient *GrpcRelayClient
}

// RelayClient 返回该节点的 GrpcRelayClient, 若不存在则懒创建。
// 对本地节点返回 nil。
func (n *NodeInfo) RelayClient() *GrpcRelayClient {
	if n.relayClient == nil {
		n.relayClient = NewGrpcRelayClient(n.Address)
	}
	return n.relayClient
}

// StreamLocation 流的位置信息
type StreamLocation struct {
	StreamID    string
	NodeID      string    // 当前持有该流的节点
	ReplicaNodes []string // 副本节点列表
	CreatedAt   time.Time
}

// Config 集群配置
type Config struct {
	NodeID       string        // 本节点 ID, 空则自动生成
	Address      string        // 本节点 gRPC 监听地址
	Region       string        // 区域
	Seeds        []string      // 种子节点地址列表
	HeartbeatInterval time.Duration // 心跳间隔, 默认 5s
	HeartbeatTimeout  time.Duration // 心跳超时, 默认 15s
	MaxReplicas  int           // 每个流的最大副本数, 默认 2
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		NodeID:            uuid.NewString(),
		Address:           ":50051",
		Region:            "default",
		HeartbeatInterval: 5 * time.Second,
		HeartbeatTimeout:  15 * time.Second,
		MaxReplicas:       2,
	}
}

// Manager 集群管理器
type Manager struct {
	config  Config
	local   *NodeInfo
	peers   sync.Map // map[string]*NodeInfo
	streams sync.Map // map[string]*StreamLocation
	log     *zap.Logger
	stopCh  chan struct{}
}

// NewManager 创建集群管理器
func NewManager(config Config, log *zap.Logger) *Manager {
	if log == nil {
		if logger.Lg != nil {
			log = logger.Lg
		} else {
			log = zap.NewNop()
		}
	}

	if config.NodeID == "" {
		config.NodeID = uuid.NewString()
	}

	local := &NodeInfo{
		ID:        config.NodeID,
		Address:   config.Address,
		Region:    config.Region,
		Status:    NodeOnline,
		LastHeartbeat: time.Now(),
		Metadata:  make(map[string]string),
	}

	return &Manager{
		config: config,
		local:  local,
		log:    log.With(zap.String("component", "cluster-manager"), zap.String("node", config.NodeID)),
		stopCh: make(chan struct{}),
	}
}

// Start 启动集群管理器
func (m *Manager) Start() error {
	m.log.Info("cluster manager starting",
		zap.String("nodeID", m.config.NodeID),
		zap.String("address", m.config.Address),
		zap.Int("seeds", len(m.config.Seeds)))

	// 注册种子节点
	for _, seed := range m.config.Seeds {
		if seed == m.config.Address {
			continue
		}
		m.AddPeer(&NodeInfo{
			ID:        seed, // 使用地址作为临时 ID
			Address:   seed,
			Status:    NodeOnline,
			LastHeartbeat: time.Now(),
			Metadata:  make(map[string]string),
		})
	}

	go m.heartbeatLoop()
	return nil
}

// Stop 停止集群管理器
func (m *Manager) Stop() {
	close(m.stopCh)
	m.log.Info("cluster manager stopped")
}

// LocalNode 返回本地节点信息
func (m *Manager) LocalNode() *NodeInfo {
	return m.local
}

// AddPeer 添加/更新 peer 节点
func (m *Manager) AddPeer(node *NodeInfo) {
	m.peers.Store(node.ID, node)
	m.log.Info("peer added", zap.String("peer", node.ID), zap.String("addr", node.Address))
}

// RemovePeer 移除 peer 节点
func (m *Manager) RemovePeer(nodeID string) {
	m.peers.Delete(nodeID)
	m.log.Info("peer removed", zap.String("peer", nodeID))
}

// GetPeer 获取 peer 节点
func (m *Manager) GetPeer(nodeID string) (*NodeInfo, bool) {
	v, ok := m.peers.Load(nodeID)
	if !ok {
		return nil, false
	}
	return v.(*NodeInfo), true
}

// ListPeers 列出所有 peer 节点
func (m *Manager) ListPeers() []*NodeInfo {
	var list []*NodeInfo
	m.peers.Range(func(key, value any) bool {
		list = append(list, value.(*NodeInfo))
		return true
	})
	return list
}

// RegisterStream 注册本地流
func (m *Manager) RegisterStream(streamID string) *StreamLocation {
	loc := &StreamLocation{
		StreamID:  streamID,
		NodeID:    m.config.NodeID,
		CreatedAt: time.Now(),
	}
	m.streams.Store(streamID, loc)
	m.local.StreamCount.Add(1)
	m.log.Info("stream registered", zap.String("stream", streamID))
	return loc
}

// UnregisterStream 注销本地流
func (m *Manager) UnregisterStream(streamID string) {
	m.streams.Delete(streamID)
	m.local.StreamCount.Add(-1)
	m.log.Info("stream unregistered", zap.String("stream", streamID))
}

// LookupStream 查找流的位置
func (m *Manager) LookupStream(streamID string) (*StreamLocation, bool) {
	v, ok := m.streams.Load(streamID)
	if !ok {
		return nil, false
	}
	return v.(*StreamLocation), true
}

// RouteStream 路由流请求到正确的节点
// 如果流在本地, 返回本地节点; 否则返回持有该流的 peer 节点
func (m *Manager) RouteStream(streamID string) (*NodeInfo, error) {
	loc, ok := m.LookupStream(streamID)
	if !ok {
		return nil, fmt.Errorf("stream not found: %s", streamID)
	}

	if loc.NodeID == m.config.NodeID {
		return m.local, nil
	}

	peer, ok := m.GetPeer(loc.NodeID)
	if !ok || peer.Status == NodeOffline {
		// 尝试副本
		for _, replicaID := range loc.ReplicaNodes {
			if replicaID == m.config.NodeID {
				return m.local, nil
			}
			if p, ok := m.GetPeer(replicaID); ok && p.Status == NodeOnline {
				return p, nil
			}
		}
		return nil, fmt.Errorf("stream node unavailable: %s", loc.NodeID)
	}

	return peer, nil
}

// SelectNodeForNewStream 为新流选择最佳节点 (负载最低)
func (m *Manager) SelectNodeForNewStream() *NodeInfo {
	var best *NodeInfo
	bestScore := int64(101)

	// 考虑本地节点
	localScore := m.local.LoadScore.Load()
	if localScore < bestScore && m.local.Status == NodeOnline {
		best = m.local
		bestScore = localScore
	}

	// 考虑 peer 节点
	m.peers.Range(func(key, value any) bool {
		peer := value.(*NodeInfo)
		if peer.Status != NodeOnline {
			return true
		}
		score := peer.LoadScore.Load()
		if score < bestScore {
			best = peer
			bestScore = score
		}
		return true
	})

	return best
}

// UpdateLoad 更新本节点负载分数
func (m *Manager) UpdateLoad(score int64) {
	m.local.LoadScore.Store(score)
}

// IncSession 增加会话计数
func (m *Manager) IncSession() {
	m.local.SessionCount.Add(1)
}

// DecSession 减少会话计数
func (m *Manager) DecSession() {
	m.local.SessionCount.Add(-1)
}

// heartbeatLoop 心跳循环
func (m *Manager) heartbeatLoop() {
	ticker := time.NewTicker(m.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.sendHeartbeats()
			m.checkPeerHealth()
		}
	}
}

func (m *Manager) sendHeartbeats() {
	m.local.LastHeartbeat = time.Now()

	req := HeartbeatRequest{
		NodeID:       m.config.NodeID,
		Address:      m.config.Address,
		LoadScore:    m.local.LoadScore.Load(),
		SessionCount: m.local.SessionCount.Load(),
		StreamCount:  m.local.StreamCount.Load(),
	}

	m.peers.Range(func(key, value any) bool {
		peer := value.(*NodeInfo)
		// 仅向 online/degraded peer 发送心跳, 离线节点跳过
		if peer.Status == NodeOffline {
			return true
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		resp, err := peer.RelayClient().SendHeartbeat(ctx, req)
		cancel()
		if err != nil {
			m.log.Warn("heartbeat failed",
				zap.String("peer", peer.ID),
				zap.String("addr", peer.Address),
				zap.Error(err))
			// 心跳失败, 标记为降级, 由 checkPeerHealth 进一步处理
			if peer.Status != NodeDegraded {
				peer.Status = NodeDegraded
			}
			return true
		}

		// 收到响应后更新 peer 的负载信息和最近心跳时间
		peer.LastHeartbeat = time.Now()
		peer.LoadScore.Store(resp.LoadScore)
		m.log.Debug("heartbeat ok",
			zap.String("peer", peer.ID),
			zap.Int64("load", resp.LoadScore))
		return true
	})
}

func (m *Manager) checkPeerHealth() {
	timeout := m.config.HeartbeatTimeout
	now := time.Now()

	m.peers.Range(func(key, value any) bool {
		peer := value.(*NodeInfo)
		since := now.Sub(peer.LastHeartbeat)
		if since > timeout {
			if peer.Status != NodeOffline {
				peer.Status = NodeOffline
				m.log.Warn("peer went offline", zap.String("peer", peer.ID), zap.Duration("since", since))
			}
		} else if since > timeout/2 {
			if peer.Status != NodeDegraded {
				peer.Status = NodeDegraded
				m.log.Warn("peer degraded", zap.String("peer", peer.ID), zap.Duration("since", since))
			}
		}
		return true
	})
}

// ClusterStats 集群统计信息
type ClusterStats struct {
	LocalNodeID    string
	TotalNodes     int
	OnlineNodes    int
	DegradedNodes  int
	OfflineNodes   int
	TotalStreams   int
	TotalSessions  int64
}

// Stats 返回集群统计
func (m *Manager) Stats() ClusterStats {
	stats := ClusterStats{
		LocalNodeID:   m.config.NodeID,
		TotalStreams:  0,
		TotalSessions: m.local.SessionCount.Load(),
	}

	m.peers.Range(func(key, value any) bool {
		peer := value.(*NodeInfo)
		stats.TotalNodes++
		stats.TotalSessions += peer.SessionCount.Load()
		switch peer.Status {
		case NodeOnline:
			stats.OnlineNodes++
		case NodeDegraded:
			stats.DegradedNodes++
		case NodeOffline:
			stats.OfflineNodes++
		}
		return true
	})
	stats.TotalNodes++ // include local

	m.streams.Range(func(key, value any) bool {
		stats.TotalStreams++
		return true
	})

	return stats
}

// RelayMessage 跨节点中继消息
type RelayMessage struct {
	FromNode string
	ToNode   string
	StreamID string
	Type     string // "signal" | "media" | "control"
	Payload  []byte
}

// RelayToNode 转发消息到指定节点。
// 根据 msg.Type 选择 RelayMedia 或 RelaySignal, 连接失败时标记 peer 为降级。
func (m *Manager) RelayToNode(ctx context.Context, nodeID string, msg RelayMessage) error {
	peer, ok := m.GetPeer(nodeID)
	if !ok {
		return fmt.Errorf("peer not found: %s", nodeID)
	}
	if peer.Status == NodeOffline {
		return fmt.Errorf("peer offline: %s", nodeID)
	}

	m.log.Debug("relay message",
		zap.String("from", msg.FromNode),
		zap.String("to", msg.ToNode),
		zap.String("stream", msg.StreamID),
		zap.String("type", msg.Type),
		zap.Int("payload_size", len(msg.Payload)))

	client := peer.RelayClient()

	switch msg.Type {
	case "media":
		req := RelayMediaRequest{
			FromNode: msg.FromNode,
			ToNode:   msg.ToNode,
			StreamID: msg.StreamID,
			Payload:  msg.Payload,
		}
		resp, err := client.RelayMedia(ctx, req)
		if err != nil {
			m.markPeerDegraded(peer, err)
			return fmt.Errorf("relay media: %w", err)
		}
		if !resp.Success {
			return fmt.Errorf("relay media rejected: %s", resp.Error)
		}
		return nil

	case "signal":
		req := RelaySignalRequest{
			FromNode:   msg.FromNode,
			ToNode:     msg.ToNode,
			StreamID:   msg.StreamID,
			SignalType: msg.Type,
			Payload:    msg.Payload,
		}
		resp, err := client.RelaySignal(ctx, req)
		if err != nil {
			m.markPeerDegraded(peer, err)
			return fmt.Errorf("relay signal: %w", err)
		}
		if !resp.Success {
			return fmt.Errorf("relay signal rejected")
		}
		return nil

	default:
		return fmt.Errorf("unsupported relay type: %s", msg.Type)
	}
}

// markPeerDegraded 在转发/心跳失败时将 peer 标记为降级。
func (m *Manager) markPeerDegraded(peer *NodeInfo, cause error) {
	if peer.Status == NodeOnline {
		peer.Status = NodeDegraded
		m.log.Warn("peer marked degraded due to relay failure",
			zap.String("peer", peer.ID),
			zap.String("addr", peer.Address),
			zap.Error(cause))
	}
}
