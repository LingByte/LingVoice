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
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/ling-base/common/logger"
	"github.com/google/uuid"
	"github.com/quic-go/quic-go"
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
	conns    sync.Map // map[string]*quic.Conn (sessionID -> conn)
	listener *quic.Listener
	log      *zap.Logger
	mu       sync.Mutex
	closed   bool
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

// Start 启动 MoQ 服务, 创建实际 QUIC listener 并开始接受连接。
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tlsConfig, err := s.loadOrGenerateTLSConfig()
	if err != nil {
		return fmt.Errorf("load TLS config: %w", err)
	}

	quicConfig := &quic.Config{
		MaxIncomingStreams: int64(s.config.MaxStreams),
		KeepAlivePeriod:    30 * time.Second,
	}

	s.log.Info("moq server starting",
		zap.String("addr", s.config.Addr),
		zap.Int("maxStreams", s.config.MaxStreams))

	listener, err := quic.ListenAddr(s.config.Addr, tlsConfig, quicConfig)
	if err != nil {
		return fmt.Errorf("quic listen %s: %w", s.config.Addr, err)
	}
	s.listener = listener

	go s.acceptLoop(listener)
	return nil
}

// Close 关闭服务及 QUIC listener。
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	listener := s.listener
	s.listener = nil
	s.mu.Unlock()

	if listener != nil {
		if err := listener.Close(); err != nil {
			s.log.Warn("close quic listener", zap.Error(err))
		}
	}

	// 关闭所有活跃 QUIC 连接
	s.conns.Range(func(key, value any) bool {
		if conn, ok := value.(*quic.Conn); ok {
			_ = conn.CloseWithError(0, "server shutdown")
		}
		s.conns.Delete(key)
		return true
	})

	s.log.Info("moq server closed")
	return nil
}

// loadOrGenerateTLSConfig 加载证书文件, 若未配置则生成自签名证书。
func (s *Server) loadOrGenerateTLSConfig() (*tls.Config, error) {
	if s.config.TLSCertFile != "" && s.config.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(s.config.TLSCertFile, s.config.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load key pair: %w", err)
		}
		return &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"moq-transport"},
		}, nil
	}

	// 生成自签名证书
	cert, err := generateSelfSignedCert()
	if err != nil {
		return nil, fmt.Errorf("generate self-signed cert: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"moq-transport"},
	}, nil
}

// generateSelfSignedCert 生成内存中的自签名 TLS 证书 (用于开发/测试)。
func generateSelfSignedCert() (tls.Certificate, error) {
	// 使用 ecdsa 生成密钥对
	priv, err := ecdsaGenerateKey()
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"LingVoice MoQ"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:              []string{"localhost"},
	}

	derBytes, err := x509.CreateCertificate(nil, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM, err := encodeECDSAKey(priv)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("encode key: %w", err)
	}

	return tls.X509KeyPair(certPEM, keyPEM)
}

// acceptLoop 接受 QUIC 连接, 每个连接创建一个 MoQ session 并启动处理 goroutine。
func (s *Server) acceptLoop(listener *quic.Listener) {
	for {
		conn, err := listener.Accept(context.Background())
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			s.log.Warn("accept quic connection", zap.Error(err))
			return
		}
		go s.handleConnection(conn)
	}
}

// handleConnection 处理单个 QUIC 连接: 创建 MoQ session, 接受信令流并解析消息。
func (s *Server) handleConnection(conn *quic.Conn) {
	sess := s.CreateSession()
	s.conns.Store(sess.id, conn)

	s.log.Info("moq connection accepted",
		zap.String("session", sess.id),
		zap.String("remote", conn.RemoteAddr().String()))

	defer func() {
		s.conns.Delete(sess.id)
		_ = conn.CloseWithError(0, "session closed")
	}()

	// 接受并处理所有 stream (stream 0 为信令流, 其余为媒体流)
	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			s.log.Debug("accept stream ended",
				zap.String("session", sess.id),
				zap.Error(err))
			return
		}
		go s.handleStream(sess.id, stream)
	}
}

// handleStream 处理单个 QUIC stream 上的 MoQ 消息。
func (s *Server) handleStream(sessionID string, stream *quic.Stream) {
	defer stream.Close()

	for {
		msgType, body, err := ReadMessage(stream)
		if err != nil {
			s.log.Debug("read message ended",
				zap.String("session", sessionID),
				zap.Error(err))
			return
		}

		s.log.Debug("moq message received",
			zap.String("session", sessionID),
			zap.String("type", msgType.String()))

		switch msgType {
		case MsgSubscribe:
			msg, err := DecodeMessageBody(msgType, body)
			if err != nil {
				s.log.Warn("decode subscribe", zap.Error(err))
				continue
			}
			sub := msg.(*SubscribeMessage)
			if _, err := s.HandleSubscribe(sessionID, sub.TrackName, sub.TrackAlias); err != nil {
				s.log.Warn("handle subscribe", zap.Error(err))
			}

		case MsgAnnounce:
			msg, err := DecodeMessageBody(msgType, body)
			if err != nil {
				s.log.Warn("decode announce", zap.Error(err))
				continue
			}
			ann := msg.(*AnnounceMessage)
			if err := s.HandleAnnounce(sessionID, ann.TrackNamespace); err != nil {
				s.log.Warn("handle announce", zap.Error(err))
			}

		case MsgGoAway:
			if err := s.HandleGoAway(sessionID); err != nil {
				s.log.Warn("handle goaway", zap.Error(err))
			}
			return

		case MsgObject:
			msg, err := DecodeMessageBody(msgType, body)
			if err != nil {
				s.log.Warn("decode object", zap.Error(err))
				continue
			}
			obj := msg.(*ObjectMessage)
			if err := s.HandleObject(sessionID, SubscribeID(0), obj.Payload, obj.Timestamp); err != nil {
				s.log.Warn("handle object", zap.Error(err))
			}

		default:
			s.log.Debug("unhandled message type",
				zap.String("type", msgType.String()))
		}
	}
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

// SendMediaFrame 向指定会话发送媒体帧 (subscriber 方向)。
// 通过 QUIC stream 发送 MoQ Object 消息: type + track_alias + group_id + object_id + timestamp + payload。
func (s *Server) SendMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	v, ok := s.conns.Load(sessionID)
	if !ok {
		s.log.Debug("send media frame: no quic connection",
			zap.String("session", sessionID))
		return fmt.Errorf("no quic connection for session: %s", sessionID)
	}
	conn := v.(*quic.Conn)

	stream, err := conn.OpenStream()
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	defer stream.Close()

	msg := &ObjectMessage{
		TrackAlias: TrackAlias(0),
		GroupID:    0,
		ObjectID:   0,
		Timestamp:  uint64(frame.Timestamp),
		Payload:    frame.Payload,
	}
	if err := WriteObject(stream, msg); err != nil {
		return fmt.Errorf("write object: %w", err)
	}

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
