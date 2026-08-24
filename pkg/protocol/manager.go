package protocol

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/LingVoice/pkg/protocol/sip"
	"github.com/LingByte/LingVoice/pkg/protocol/webrtc"
	"github.com/LingByte/LingVoice/pkg/protocol/ws"
)

// Manager 统一管理所有协议服务。
// 上层（会话管理/编排）只跟 Manager 交互，不直接操作各协议。
type Manager struct {
	handler common.EventHandler
	logger  *slog.Logger

	wsServer     *ws.Server
	sipServer    *sip.Server
	webrtcServer *webrtc.Server

	sessions sync.Map // map[string]common.ProtocolSession
}

// NewManager 创建协议管理器
func NewManager(handler common.EventHandler, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		handler: handler,
		logger:  logger.With("component", "protocol-manager"),
	}
}

// WithWebSocket 启用 WebSocket 协议
func (m *Manager) WithWebSocket(config ws.Config) *Manager {
	m.wsServer = ws.NewServer(config, m, m.logger)
	return m
}

// WithSIP 启用 SIP 协议
func (m *Manager) WithSIP(config sip.Config) (*Manager, error) {
	srv, err := sip.NewServer(config, m, m.logger)
	if err != nil {
		return nil, fmt.Errorf("create sip server: %w", err)
	}
	m.sipServer = srv
	return m, nil
}

// WithWebRTC 启用 WebRTC 协议
func (m *Manager) WithWebRTC(config webrtc.Config) *Manager {
	m.webrtcServer = webrtc.NewServer(config, m, m.logger)
	return m
}

// Start 启动所有已配置的协议服务
func (m *Manager) Start() error {
	var errs []error

	// WebSocket
	if m.wsServer != nil {
		go func() {
			if err := m.wsServer.Start(); err != nil {
				m.logger.Error("ws server stopped", "error", err)
			}
		}()
		m.logger.Info("ws server started")
	}

	// SIP
	if m.sipServer != nil {
		go func() {
			if err := m.sipServer.Start(); err != nil {
				m.logger.Error("sip server stopped", "error", err)
			}
		}()
		m.logger.Info("sip server started")
	}

	// WebRTC
	if m.webrtcServer != nil {
		go func() {
			if err := m.webrtcServer.Start(); err != nil {
				m.logger.Error("webrtc server stopped", "error", err)
			}
		}()
		m.logger.Info("webrtc server started")
	}

	if len(errs) > 0 {
		return fmt.Errorf("start errors: %v", errs)
	}
	return nil
}

// Close 关闭所有协议服务
func (m *Manager) Close() {
	if m.sipServer != nil {
		_ = m.sipServer.Close()
	}
}

// GetSession 获取会话（统一接口）
func (m *Manager) GetSession(id string) (common.ProtocolSession, bool) {
	if v, ok := m.sessions.Load(id); ok {
		return v.(common.ProtocolSession), true
	}
	return nil, false
}

// SendCommand 向指定会话下发指令
func (m *Manager) SendCommand(sessionID string, cmd common.ProtocolCommand) error {
	sess, ok := m.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	return sess.SendCommand(cmd)
}

// SendMediaFrame 向指定会话发送音视频帧
func (m *Manager) SendMediaFrame(sessionID string, frame common.MediaFrame) error {
	sess, ok := m.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	return sess.SendMediaFrame(frame)
}

// --- 实现 common.EventHandler ---
// Manager 自身作为 EventHandler 代理，把各协议的事件转发给真正的上层 handler。
// 同时在这里做 session 注册/注销。

func (m *Manager) OnEvent(event common.ProtocolEvent) error {
	// 注册/注销 session
	switch event.Type {
	case common.EventIncomingCall:
		// 从对应协议 server 获取 session 并注册
		if sess := m.lookupSession(event.Protocol, event.SessionID); sess != nil {
			m.sessions.Store(event.SessionID, sess)
		}
	case common.EventHangup:
		m.sessions.Delete(event.SessionID)
	}

	// 转发给上层
	return m.handler.OnEvent(event)
}

func (m *Manager) OnMediaFrame(sessionID string, frame common.MediaFrame) error {
	return m.handler.OnMediaFrame(sessionID, frame)
}

// lookupSession 从对应协议 server 查找 session 对象
func (m *Manager) lookupSession(protocol common.ProtocolType, id string) common.ProtocolSession {
	switch protocol {
	case common.ProtocolWS:
		if m.wsServer != nil {
			if s, ok := m.wsServer.GetSession(id); ok {
				return s
			}
		}
	case common.ProtocolSIP:
		if m.sipServer != nil {
			if s, ok := m.sipServer.GetSession(id); ok {
				return s
			}
		}
	case common.ProtocolWebRTC:
		if m.webrtcServer != nil {
			if s, ok := m.webrtcServer.GetSession(id); ok {
				return s
			}
		}
	}
	return nil
}
