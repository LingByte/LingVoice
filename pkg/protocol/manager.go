package protocol

import (
	"fmt"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/api"
	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/LingVoice/pkg/protocol/mqtt"
	"github.com/LingByte/LingVoice/pkg/protocol/rtmp"
	"github.com/LingByte/LingVoice/pkg/protocol/sip"
	"github.com/LingByte/LingVoice/pkg/protocol/webrtc"
	"github.com/LingByte/LingVoice/pkg/protocol/whep"
	"github.com/LingByte/LingVoice/pkg/protocol/whip"
	"github.com/LingByte/LingVoice/pkg/protocol/ws"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

// Manager 统一管理所有协议服务。
// 上层（会话管理/编排）只跟 Manager 交互，不直接操作各协议。
type Manager struct {
	handler common.EventHandler
	log     *zap.Logger

	wsServer      *ws.Server
	sipServer     *sip.Server
	webrtcServer  *webrtc.Server
	rtmpServer    *rtmp.Server
	whipServer    *whip.Server
	whepServer    *whep.Server
	mqttServer    *mqtt.Server
	apiServer     *api.Server

	sessions     sync.Map // map[string]common.ProtocolSession
	enabledProtos []common.ProtocolType
}

// NewManager 创建协议管理器
func NewManager(handler common.EventHandler, log *zap.Logger) *Manager {
	if log == nil {
		log = logger.Lg
	}
	return &Manager{
		handler: handler,
		log:     log.With(zap.String("component", "protocol-manager")),
	}
}

// WithWebSocket 启用 WebSocket 协议
func (m *Manager) WithWebSocket(config ws.Config) *Manager {
	m.wsServer = ws.NewServer(config, m, m.log)
	m.enabledProtos = append(m.enabledProtos, common.ProtocolWS)
	return m
}

// WithSIP 启用 SIP 协议
func (m *Manager) WithSIP(config sip.Config) (*Manager, error) {
	srv, err := sip.NewServer(config, m, m.log)
	if err != nil {
		return nil, fmt.Errorf("create sip server: %w", err)
	}
	m.sipServer = srv
	m.enabledProtos = append(m.enabledProtos, common.ProtocolSIP)
	return m, nil
}

// WithWebRTC 启用 WebRTC 协议
func (m *Manager) WithWebRTC(config webrtc.Config) *Manager {
	m.webrtcServer = webrtc.NewServer(config, m, m.log)
	m.enabledProtos = append(m.enabledProtos, common.ProtocolWebRTC)
	return m
}

// WithRTMP 启用 RTMP 协议
func (m *Manager) WithRTMP(config rtmp.Config) *Manager {
	m.rtmpServer = rtmp.NewServer(config, m, m.log)
	m.enabledProtos = append(m.enabledProtos, common.ProtocolRTMP)
	return m
}

// WithWHIP 启用 WHIP 协议
func (m *Manager) WithWHIP(config whip.Config) *Manager {
	m.whipServer = whip.NewServer(config, m, m.log)
	m.enabledProtos = append(m.enabledProtos, common.ProtocolWHIP)
	return m
}

// WithWHEP 启用 WHEP 协议
func (m *Manager) WithWHEP(config whep.Config) *Manager {
	m.whepServer = whep.NewServer(config, m, m.log)
	m.enabledProtos = append(m.enabledProtos, common.ProtocolWHEP)
	return m
}

// WithMQTT 启用 MQTT 协议
func (m *Manager) WithMQTT(config mqtt.Config) *Manager {
	m.mqttServer = mqtt.NewServer(config, m, m.log)
	m.enabledProtos = append(m.enabledProtos, common.ProtocolMQTT)
	return m
}

// WithAPI 启用 REST API 控制面
func (m *Manager) WithAPI(config api.Config) *Manager {
	m.apiServer = api.NewServer(config, m, m.log)
	m.apiServer.SetProtocols(m.enabledProtos)
	return m
}

// EnabledProtocols 返回已启用的协议列表
func (m *Manager) EnabledProtocols() []common.ProtocolType {
	return m.enabledProtos
}

// Start 启动所有已配置的协议服务
func (m *Manager) Start() error {
	// WebSocket
	if m.wsServer != nil {
		go func() {
			if err := m.wsServer.Start(); err != nil {
				m.log.Error("ws server stopped", zap.Error(err))
			}
		}()
		m.log.Info("ws server started")
	}

	// SIP
	if m.sipServer != nil {
		go func() {
			if err := m.sipServer.Start(); err != nil {
				m.log.Error("sip server stopped", zap.Error(err))
			}
		}()
		m.log.Info("sip server started")
	}

	// WebRTC
	if m.webrtcServer != nil {
		go func() {
			if err := m.webrtcServer.Start(); err != nil {
				m.log.Error("webrtc server stopped", zap.Error(err))
			}
		}()
		m.log.Info("webrtc server started")
	}

	// RTMP
	if m.rtmpServer != nil {
		if err := m.rtmpServer.Start(); err != nil {
			return fmt.Errorf("start rtmp: %w", err)
		}
		m.log.Info("rtmp server started")
	}

	// WHIP
	if m.whipServer != nil {
		go func() {
			if err := m.whipServer.Start(); err != nil {
				m.log.Error("whip server stopped", zap.Error(err))
			}
		}()
		m.log.Info("whip server started")
	}

	// WHEP
	if m.whepServer != nil {
		go func() {
			if err := m.whepServer.Start(); err != nil {
				m.log.Error("whep server stopped", zap.Error(err))
			}
		}()
		m.log.Info("whep server started")
	}

	// MQTT
	if m.mqttServer != nil {
		if err := m.mqttServer.Start(); err != nil {
			return fmt.Errorf("start mqtt: %w", err)
		}
		m.log.Info("mqtt server started")
	}

	// REST API
	if m.apiServer != nil {
		go func() {
			if err := m.apiServer.Start(); err != nil {
				m.log.Error("api server stopped", zap.Error(err))
			}
		}()
		m.log.Info("rest api server started")
	}

	return nil
}

// Close 关闭所有协议服务
func (m *Manager) Close() {
	if m.sipServer != nil {
		_ = m.sipServer.Close()
	}
	if m.rtmpServer != nil {
		_ = m.rtmpServer.Close()
	}
	if m.mqttServer != nil {
		_ = m.mqttServer.Close()
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

// SendMediaFrame 向指定会话的指定轨道发送音视频帧
func (m *Manager) SendMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	sess, ok := m.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	ms, ok := sess.(common.MediaSession)
	if !ok {
		return fmt.Errorf("session %s does not support media", sessionID)
	}
	return ms.SendMediaFrame(trackID, frame)
}

// --- 实现 common.EventHandler ---
// Manager 自身作为 EventHandler 代理，把各协议的事件转发给真正的上层 handler。
// 同时在这里做 session 注册/注销。

func (m *Manager) OnEvent(event common.ProtocolEvent) error {
	// 注册/注销 session
	switch event.Type {
	case common.EventIncomingCall:
		if sess := m.lookupSession(event.Protocol, event.SessionID); sess != nil {
			m.sessions.Store(event.SessionID, sess)
		}
	case common.EventHangup:
		m.sessions.Delete(event.SessionID)
	}

	// 转发给上层
	return m.handler.OnEvent(event)
}

func (m *Manager) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	return m.handler.OnMediaFrame(sessionID, trackID, frame)
}

func (m *Manager) OnData(sessionID string, msg common.DataMessage) error {
	return m.handler.OnData(sessionID, msg)
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
	case common.ProtocolRTMP:
		if m.rtmpServer != nil {
			if s, ok := m.rtmpServer.GetSession(id); ok {
				return s
			}
		}
	case common.ProtocolWHIP:
		if m.whipServer != nil {
			if s, ok := m.whipServer.GetSession(id); ok {
				return s
			}
		}
	case common.ProtocolWHEP:
		if m.whepServer != nil {
			if s, ok := m.whepServer.GetSession(id); ok {
				return s
			}
		}
	case common.ProtocolMQTT:
		if m.mqttServer != nil {
			if s, ok := m.mqttServer.GetSession(id); ok {
				return s
			}
		}
	}
	return nil
}
