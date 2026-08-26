package rtmp

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

// Config RTMP 服务配置
type Config struct {
	Addr string // 监听地址，如 ":1935"
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{Addr: ":1935"}
}

// Server RTMP 协议服务（纯协议层，不做媒体分发）。
// 只实现 publish/ingest（推流入站），不实现 play/egress。
type Server struct {
	config   Config
	handler  common.EventHandler
	sessions sync.Map // map[string]*Conn
	log      *zap.Logger
	listener net.Listener
}

// NewServer 创建 RTMP 服务
func NewServer(config Config, handler common.EventHandler, log *zap.Logger) *Server {
	if log == nil {
		log = logger.Lg
	}
	return &Server{
		config:  config,
		handler: handler,
		log:     log.With(zap.String("component", "rtmp-server")),
	}
}

// Start 启动 RTMP TCP 监听
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		return fmt.Errorf("rtmp listen %s: %w", s.config.Addr, err)
	}
	s.listener = ln
	s.log.Info("rtmp server starting", zap.String("addr", s.config.Addr))

	go s.acceptLoop()
	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

// Close 关闭服务
func (s *Server) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// GetSession 获取会话（返回 *Conn）
func (s *Server) GetSession(id string) (*Conn, bool) {
	v, ok := s.sessions.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*Conn), true
}

// handleConn 处理一条 TCP 连接：handshake → 命令/媒体循环
func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	remote := conn.RemoteAddr().String()
	s.log.Debug("rtmp incoming connection", zap.String("remote", remote))

	// 1. RTMP handshake
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err := Handshake(conn); err != nil {
		s.log.Error("rtmp handshake failed", zap.String("remote", remote), zap.Error(err))
		return
	}
	conn.SetReadDeadline(time.Time{})

	// 2. 创建 Conn 处理器并进入 serve 循环
	c := NewConn(conn, s.handler, s.log)
	s.sessions.Store(c.SessionID(), c)
	defer s.sessions.Delete(c.SessionID())

	if err := c.Serve(); err != nil {
		s.log.Debug("rtmp conn serve ended",
			zap.String("session", c.SessionID()),
			zap.String("remote", remote),
			zap.Error(err))
	}
}
