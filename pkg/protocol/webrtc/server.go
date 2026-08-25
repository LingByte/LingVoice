package webrtc

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/ling-base/common/logger"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

//go:embed testpage.html
var testPageHTML []byte

var signalUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Server WebRTC 信令服务
type Server struct {
	config   Config
	handler  common.EventHandler
	sessions sync.Map // map[string]*Session
	log      *zap.Logger
}

// NewServer 创建 WebRTC 信令服务
func NewServer(config Config, handler common.EventHandler, log *zap.Logger) *Server {
	if log == nil {
		log = logger.Lg
	}
	return &Server{
		config:  config,
		handler: handler,
		log:     log.With(zap.String("component", "webrtc-server")),
	}
}

// Handler 返回 HTTP Handler
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.config.Path, s.handleSignal)
	// 根路径返回测试页面
	mux.HandleFunc("/", s.handleTestPage)
	return mux
}

// handleTestPage 返回 WebRTC 测试页面
func (s *Server) handleTestPage(w http.ResponseWriter, r *http.Request) {
	// 如果请求的是信令路径之外的 API 路径，返回 404
	if r.URL.Path != "/" && r.URL.Path != "/index.html" && r.URL.Path != "/test" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(testPageHTML)
}

// Start 启动信令服务
func (s *Server) Start() error {
	s.log.Info("webrtc signaling server starting",
		zap.String("addr", s.config.Addr),
		zap.String("path", s.config.Path))
	return http.ListenAndServe(s.config.Addr, s.Handler())
}

// GetSession 获取会话
func (s *Server) GetSession(id string) (*Session, bool) {
	v, ok := s.sessions.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*Session), true
}

// RangeSessions 遍历所有会话（fn 返回 false 停止遍历）
func (s *Server) RangeSessions(fn func(*Session) bool) {
	s.sessions.Range(func(key, value interface{}) bool {
		return fn(value.(*Session))
	})
}

// handleSignal 处理 WebRTC 信令（WebSocket 传输 SDP/ICE）
func (s *Server) handleSignal(w http.ResponseWriter, r *http.Request) {
	conn, err := signalUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Error("webrtc signal upgrade failed", zap.Error(err))
		return
	}
	defer conn.Close()

	sessionID := uuid.NewString()
	s.log.Info("webrtc signal connected",
		zap.String("session", sessionID),
		zap.String("remote", conn.RemoteAddr().String()))

	// 创建会话（双 PC）
	sess, err := newSession(sessionID, s.config, s.handler, s.log)
	if err != nil {
		s.log.Error("create session", zap.Error(err))
		return
	}
	s.sessions.Store(sessionID, sess)
	defer s.sessions.Delete(sessionID)
	defer sess.Close()

	// 设置信令发送函数
	writeMu := &sync.Mutex{}
	sess.setSignalSend(func(msg signalMessage) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(msg)
	})

	// 设置 ICE candidate 回调
	sess.pub.pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		sess.sendSignal(signalMessage{
			Type:      "candidate",
			Target:    "publisher",
			Candidate: candidate.ToJSON(),
		})
	})
	sess.sub.pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		sess.sendSignal(signalMessage{
			Type:      "candidate",
			Target:    "subscriber",
			Candidate: candidate.ToJSON(),
		})
	})

	// 创建服务端 DataChannel
	if err := sess.createDataChannels(); err != nil {
		s.log.Error("create data channels", zap.Error(err))
	}

	// 通知上层：来电
	_ = s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolWebRTC,
		SessionID: sessionID,
		From:      conn.RemoteAddr().String(),
		To:        s.config.Path,
		Timestamp: time.Now(),
	})

	// 信令读循环
	s.signalLoop(sess, conn)

	// 通知上层：挂断
	_ = s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventHangup,
		Protocol:  common.ProtocolWebRTC,
		SessionID: sessionID,
		Timestamp: time.Now(),
	})
}

// signalMessage 信令消息格式（双 PC 版本）
type signalMessage struct {
	Type      string                  `json:"type"`      // "pub_offer", "sub_answer", "candidate", "sub_offer", "restart_offer", "pub_answer"
	Target    string                  `json:"target,omitempty"`    // "publisher" / "subscriber"（candidate 用）
	SDP       string                  `json:"sdp,omitempty"`
	Candidate webrtc.ICECandidateInit `json:"candidate,omitempty"`
}

func (s *Server) signalLoop(sess *Session, conn *websocket.Conn) {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			s.log.Debug("webrtc signal read end", zap.String("session", sess.id), zap.Error(err))
			return
		}

		var msg signalMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			s.log.Error("webrtc signal parse", zap.String("session", sess.id), zap.Error(err))
			continue
		}

		switch msg.Type {
		case "pub_offer":
			sess.handlePublisherOffer(msg.SDP)
		case "sub_answer":
			sess.handleSubscriberAnswer(msg.SDP)
		case "candidate":
			sess.handleCandidate(msg.Target, msg.Candidate)
		default:
			s.log.Warn("webrtc unknown signal type",
				zap.String("session", sess.id),
				zap.String("type", msg.Type))
		}
	}
}
