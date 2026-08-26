// Package rtsp implements an RTSP server that receives pushed streams
// (RTSP source / ingest mode) and forwards RTP packets to the upper layer.
package rtsp

import (
	"bufio"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/ling-base/common/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Config RTSP 服务配置
type Config struct {
	Addr string // 监听地址，如 ":554"
}

// DefaultConfig 默认配置（端口 554）
func DefaultConfig() Config {
	return Config{Addr: ":554"}
}

// Server RTSP 协议服务（接收推流模式）
type Server struct {
	config   Config
	handler  common.EventHandler
	sessions sync.Map // map[string]*Session
	log      *zap.Logger
	listener net.Listener
	closed   bool
	mu       sync.Mutex
}

// NewServer 创建 RTSP 服务
func NewServer(config Config, handler common.EventHandler, log *zap.Logger) *Server {
	if log == nil {
		log = logger.Lg
	}
	return &Server{
		config:  config,
		handler: handler,
		log:     log.With(zap.String("component", "rtsp-server")),
	}
}

// Start 启动 RTSP 服务
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		return fmt.Errorf("rtsp listen %s: %w", s.config.Addr, err)
	}
	s.listener = ln
	s.log.Info("rtsp server starting", zap.String("addr", s.config.Addr))

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				s.mu.Lock()
				closed := s.closed
				s.mu.Unlock()
				if closed {
					return
				}
				s.log.Error("rtsp accept", zap.Error(err))
				continue
			}
			go s.handleConn(conn)
		}
	}()
	return nil
}

// Close 关闭服务
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// GetSession 获取会话
func (s *Server) GetSession(id string) (*Session, bool) {
	v, ok := s.sessions.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*Session), true
}

// CloseSession 关闭并移除会话
func (s *Server) CloseSession(id string) {
	if v, ok := s.sessions.LoadAndDelete(id); ok {
		_ = v.(*Session).Close()
	}
}

// handleConn 处理一个 RTSP TCP 连接。
// 连接复用：信令和 interleaved 媒体数据在同一 TCP 上。
func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	sessionID := uuid.NewString()
	session := newSession(sessionID, conn, s.handler, s)
	s.sessions.Store(sessionID, session)
	defer s.sessions.Delete(sessionID)

	s.log.Info("rtsp connection",
		zap.String("session", sessionID),
		zap.String("remote", conn.RemoteAddr().String()))

	// 通知上层：有新连接（来电）
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolRTSP,
		SessionID: sessionID,
		From:      conn.RemoteAddr().String(),
		To:        "",
		Timestamp: time.Now(),
	})

	defer func() {
		s.handler.OnEvent(common.ProtocolEvent{
			Type:      common.EventHangup,
			Protocol:  common.ProtocolRTSP,
			SessionID: sessionID,
			Timestamp: time.Now(),
		})
		s.log.Info("rtsp session closed", zap.String("session", sessionID))
	}()

	reader := bufio.NewReaderSize(conn, 64*1024)

	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		// 检查第一个字节：'$' 表示 interleaved 帧，否则是 RTSP 信令
		b, err := reader.Peek(1)
		if err != nil {
			if !session.closed.Load() {
				s.log.Debug("rtsp read peek", zap.String("session", sessionID), zap.Error(err))
			}
			return
		}

		if b[0] == interleavedMagic {
			// interleaved RTP/RTCP 帧
			channel, data, err := session.transport.ReadInterleaved()
			if err != nil {
				if !session.closed.Load() {
					s.log.Debug("rtsp read interleaved", zap.String("session", sessionID), zap.Error(err))
				}
				return
			}
			conn.SetReadDeadline(time.Time{})
			// 只处理偶数 channel（RTP），奇数是 RTCP（忽略）
			if channel%2 == 0 {
				s.handleRtpData(session, channel, data)
			}
			continue
		}

		// RTSP 信令
		req, err := parseRequest(reader)
		if err != nil {
			if !session.closed.Load() {
				s.log.Debug("rtsp parse request", zap.String("session", sessionID), zap.Error(err))
			}
			return
		}
		conn.SetReadDeadline(time.Time{})
		if err := s.handleRequest(session, req, conn); err != nil {
			s.log.Error("rtsp handle request",
				zap.String("session", sessionID),
				zap.String("method", req.Method),
				zap.Error(err))
			return
		}

		if req.Method == MethodTeardown {
			return
		}
	}
}
