// Package moq implements Media over QUIC (MoQ) transport.
//
// MoQ 是 IETF 正在标准化的媒体传输协议, 基于 QUIC:
//   - 低延迟: 利用 QUIC 的 0-RTT 和流多路复用
//   - 可靠+不可靠混合: 关键帧可靠传输, 非关键帧可丢弃
//   - 订阅模型: publisher/subscriber pattern
//   - 多轨道: 音视频多轨道并行
//
// 当前实现状态: 框架就绪, 待 QUIC 库集成。
// 协议层 (Go) 负责:
//   - MoQ 信令 (SUBSCRIBE/ANNOUNCE/GOAWAY)
//   - 会话管理
//   - 轨道协商
// 媒体层 (Rust) 负责:
//   - QUIC 流上的媒体帧传输
//   - 优先级调度
//   - 拥塞控制
package moq

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/ling-base/common/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// MoQ 版本
const (
	VersionDraft07 = 0x07
)

// MessageType MoQ 消息类型
type MessageType uint64

const (
	MsgClientSetup  MessageType = 0x40
	MsgServerSetup  MessageType = 0x41
	MsgSubscribe    MessageType = 0x10
	MsgSubscribeOk  MessageType = 0x11
	MsgSubscribeErr MessageType = 0x12
	MsgUnsubscribe  MessageType = 0x13
	MsgAnnounce     MessageType = 0x20
	MsgAnnounceOk   MessageType = 0x21
	MsgAnnounceErr  MessageType = 0x22
	MsgUnannounce   MessageType = 0x23
	MsgGoAway       MessageType = 0x30
	MsgObject       MessageType = 0x00
)

func (m MessageType) String() string {
	switch m {
	case MsgClientSetup:
		return "CLIENT_SETUP"
	case MsgServerSetup:
		return "SERVER_SETUP"
	case MsgSubscribe:
		return "SUBSCRIBE"
	case MsgSubscribeOk:
		return "SUBSCRIBE_OK"
	case MsgSubscribeErr:
		return "SUBSCRIBE_ERR"
	case MsgUnsubscribe:
		return "UNSUBSCRIBE"
	case MsgAnnounce:
		return "ANNOUNCE"
	case MsgAnnounceOk:
		return "ANNOUNCE_OK"
	case MsgAnnounceErr:
		return "ANNOUNCE_ERR"
	case MsgUnannounce:
		return "UNANNOUNCE"
	case MsgGoAway:
		return "GOAWAY"
	case MsgObject:
		return "OBJECT"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", m)
	}
}

// TrackAlias MoQ track alias
type TrackAlias uint64

// SubscribeID MoQ subscribe ID
type SubscribeID uint64

// Session MoQ 会话
type Session struct {
	mu          sync.Mutex
	id          string
	subscriptions map[SubscribeID]*Subscription
	announcements map[string]bool // track namespace -> announced
	createdAt   time.Time
	closed      bool
}

// Subscription MoQ 订阅
type Subscription struct {
	ID          SubscribeID
	TrackAlias  TrackAlias
	TrackName   string
	Subscriber  string
	CreatedAt   time.Time
}

// Config MoQ 服务配置
type Config struct {
	Addr       string // QUIC 监听地址
	TLSCertFile string // TLS 证书
	TLSKeyFile  string // TLS 私钥
	MaxStreams  int    // 最大并发流
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Addr:       ":9000",
		MaxStreams: 100,
	}
}

// Server MoQ 服务
type Server struct {
	config   Config
	handler  common.EventHandler
	sessions sync.Map // map[string]*Session
	log      *zap.Logger
}

// NewServer 创建 MoQ 服务
func NewServer(config Config, handler common.EventHandler, log *zap.Logger) *Server {
	if log == nil {
		if logger.Lg != nil {
			log = logger.Lg
		} else {
			log = zap.NewNop()
		}
	}
	return &Server{
		config:  config,
		handler: handler,
		log:     log.With(zap.String("component", "moq-server")),
	}
}

// Start 启动 MoQ 服务
// 当前为框架实现, 实际 QUIC 监听需要 quic-go 库
func (s *Server) Start() error {
	s.log.Info("moq server starting (framework mode)",
		zap.String("addr", s.config.Addr),
		zap.Int("maxStreams", s.config.MaxStreams))
	// TODO: 实际 QUIC 监听需要 quic-go 库
	// listener, err := quic.ListenAddr(s.config.Addr, tlsConfig, quicConfig)
	// go s.acceptLoop(listener)
	return nil
}

// Close 关闭服务
func (s *Server) Close() error {
	s.log.Info("moq server closing")
	return nil
}

// CreateSession 创建新会话
func (s *Server) CreateSession() *Session {
	sess := &Session{
		id:            uuid.NewString(),
		subscriptions: make(map[SubscribeID]*Subscription),
		announcements: make(map[string]bool),
		createdAt:     time.Now(),
	}
	s.sessions.Store(sess.id, sess)
	return sess
}

// GetSession 获取会话
func (s *Server) GetSession(id string) (*Session, bool) {
	v, ok := s.sessions.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*Session), true
}

// HandleSubscribe 处理 SUBSCRIBE 消息
func (s *Server) HandleSubscribe(sessionID string, trackName string, alias TrackAlias) (*Subscription, error) {
	sess, ok := s.GetSession(sessionID)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()

	subID := SubscribeID(len(sess.subscriptions))
	sub := &Subscription{
		ID:         subID,
		TrackAlias: alias,
		TrackName:  trackName,
		CreatedAt:  time.Now(),
	}
	sess.subscriptions[subID] = sub

	s.log.Info("subscribe",
		zap.String("session", sessionID),
		zap.Uint64("subID", uint64(subID)),
		zap.String("track", trackName))

	// 通知上层
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventTrackAdded,
		Protocol:  common.ProtocolType("moq"),
		SessionID: sessionID,
		Track: &common.TrackInfo{
			ID:   common.TrackID(trackName),
			Kind: common.TrackVideo, // MoQ 不区分音视频, 默认视频
		},
		Timestamp: time.Now(),
	})

	return sub, nil
}

// HandleUnsubscribe 处理 UNSUBSCRIBE 消息
func (s *Server) HandleUnsubscribe(sessionID string, subID SubscribeID) error {
	sess, ok := s.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()

	if sub, exists := sess.subscriptions[subID]; exists {
		delete(sess.subscriptions, subID)
		s.log.Info("unsubscribe",
			zap.String("session", sessionID),
			zap.Uint64("subID", uint64(subID)),
			zap.String("track", sub.TrackName))
	}

	return nil
}

// HandleAnnounce 处理 ANNOUNCE 消息 (publisher 宣布流)
func (s *Server) HandleAnnounce(sessionID string, trackNamespace string) error {
	sess, ok := s.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()

	sess.announcements[trackNamespace] = true
	s.log.Info("announce",
		zap.String("session", sessionID),
		zap.String("namespace", trackNamespace))

	// 通知上层
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolType("moq"),
		SessionID: sessionID,
		From:      trackNamespace,
		Timestamp: time.Now(),
	})

	return nil
}

// HandleObject 处理媒体对象 (publisher 发送的媒体数据)
func (s *Server) HandleObject(sessionID string, subID SubscribeID, payload []byte, timestamp uint64) error {
	sess, ok := s.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	sess.mu.Lock()
	sub, exists := sess.subscriptions[subID]
	sess.mu.Unlock()

	if !exists {
		return fmt.Errorf("subscription not found: %d", subID)
	}

	// 转发媒体帧到上层
	frame := common.MediaFrame{
		Type:      common.FrameVideo,
		Payload:   payload,
		Timestamp: uint32(timestamp),
	}
	return s.handler.OnMediaFrame(sessionID, common.TrackID(sub.TrackName), frame)
}

// HandleGoAway 处理 GOAWY 消息
func (s *Server) HandleGoAway(sessionID string) error {
	sess, ok := s.GetSession(sessionID)
	if !ok {
		return nil
	}

	sess.mu.Lock()
	sess.closed = true
	sess.mu.Unlock()

	s.sessions.Delete(sessionID)

	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventHangup,
		Protocol:  common.ProtocolType("moq"),
		SessionID: sessionID,
		Timestamp: time.Now(),
	})

	return nil
}

// SessionStats MoQ 会话统计
type SessionStats struct {
	ID              string
	Subscriptions   int
	Announcements   int
	CreatedAt       time.Time
	Age             time.Duration
}

// GetSessionStats 获取会话统计
func (s *Server) GetSessionStats(sessionID string) (*SessionStats, error) {
	sess, ok := s.GetSession(sessionID)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()

	return &SessionStats{
		ID:            sess.id,
		Subscriptions: len(sess.subscriptions),
		Announcements: len(sess.announcements),
		CreatedAt:     sess.createdAt,
		Age:           time.Since(sess.createdAt),
	}, nil
}

// ListSessions 列出所有会话
func (s *Server) ListSessions() []*Session {
	var list []*Session
	s.sessions.Range(func(key, value any) bool {
		list = append(list, value.(*Session))
		return true
	})
	return list
}

// SendMediaFrame 向指定会话发送媒体帧 (subscriber 方向)
func (s *Server) SendMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	// 在实际实现中, 这里通过 QUIC 流发送 MoQ Object 消息
	s.log.Debug("send media frame",
		zap.String("session", sessionID),
		zap.String("track", string(trackID)),
		zap.Int("payload", len(frame.Payload)))
	return nil
}

// SendCommand 向会话发送控制命令
func (s *Server) SendCommand(sessionID string, cmd common.ProtocolCommand) error {
	switch cmd.Type {
	case common.CmdHangup:
		return s.HandleGoAway(sessionID)
	default:
		return fmt.Errorf("unsupported command: %d", cmd.Type)
	}
}

// ContextKey 用于 context 传递 MoQ 会话
type ContextKey struct{}

// WithSession 将 sessionID 注入 context
func WithSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, ContextKey{}, sessionID)
}

// SessionFromContext 从 context 提取 sessionID
func SessionFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ContextKey{}).(string); ok {
		return v
	}
	return ""
}
