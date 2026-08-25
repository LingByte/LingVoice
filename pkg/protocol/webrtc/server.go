package webrtc

import (
	"encoding/json"
	"fmt"
	"io"
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

// Config WebRTC 服务配置
type Config struct {
	Addr          string // 信令 HTTP/WS 监听地址
	Path          string // 信令路径
	ICEServers    []webrtc.ICEServer // STUN/TURN 服务器
	AudioCodecs   []string // 期望的音频编解码
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Addr:        ":8081",
		Path:        "/webrtc/signal",
		ICEServers:  []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
		AudioCodecs: []string{"opus"},
	}
}

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

// Session WebRTC 会话
type Session struct {
	id           string
	pc           *webrtc.PeerConnection
	handler      common.EventHandler
	mu           sync.Mutex
	closed       bool
	audioTrack   *webrtc.TrackLocalStaticRTP
	audioSender  *webrtc.RTPSender
	createdAt    time.Time
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
	return mux
}

// Start 启动信令服务
func (s *Server) Start() error {
	s.log.Info("webrtc signaling server starting", zap.String("addr", s.config.Addr), zap.String("path", s.config.Path))
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

// handleSignal 处理 WebRTC 信令（WebSocket 传输 SDP/ICE）
func (s *Server) handleSignal(w http.ResponseWriter, r *http.Request) {
	conn, err := signalUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Error("webrtc signal upgrade failed", zap.Error(err))
		return
	}
	defer conn.Close()

	sessionID := uuid.NewString()
	s.log.Info("webrtc signal connected", zap.String("session", sessionID), zap.String("remote", conn.RemoteAddr().String()))

	// 创建 PeerConnection
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: s.config.ICEServers,
	})
	if err != nil {
		s.log.Error("webrtc create PC failed", zap.Error(err))
		return
	}

	session := &Session{
		id:        sessionID,
		pc:        pc,
		handler:   s.handler,
		createdAt: time.Now(),
	}
	s.sessions.Store(sessionID, session)
	defer s.sessions.Delete(sessionID)
	defer pc.Close()

	// 设置 PeerConnection 事件回调
	s.setupPeerConnection(session, conn)

	// 通知上层：来电
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolWebRTC,
		SessionID: sessionID,
		From:      conn.RemoteAddr().String(),
		To:        s.config.Path,
		Timestamp: time.Now(),
	})

	// 信令读循环
	s.signalLoop(session, conn)

	// 通知上层：挂断
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventHangup,
		Protocol:  common.ProtocolWebRTC,
		SessionID: sessionID,
		Timestamp: time.Now(),
	})
}

func (s *Server) setupPeerConnection(session *Session, conn *websocket.Conn) {
	pc := session.pc

	// 收到远端 Track
	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		s.log.Info("webrtc track received",
			zap.String("session", session.id),
			zap.String("kind", track.Kind().String()),
			zap.String("codec", track.Codec().MimeType),
		)

		// 通知上层：媒体就绪
		codec, _ := common.CodecFromString(codecFromMimeType(track.Codec().MimeType))
		media := &common.MediaDescription{
			Audio: &common.AudioMedia{
				Codec:           codec,
				SampleRate:      track.Codec().ClockRate,
				Channels:        uint16(track.Codec().Channels),
				FrameDurationMs: 20,
			},
		}
		s.handler.OnEvent(common.ProtocolEvent{
			Type:      common.EventMediaReady,
			Protocol:  common.ProtocolWebRTC,
			SessionID: session.id,
			Media:     media,
			Timestamp: time.Now(),
		})

		// 读 RTP 包，回调上层
		for {
			rtp, _, err := track.ReadRTP()
			if err != nil {
				if err != io.EOF {
					s.log.Debug("webrtc track read end", zap.String("session", session.id), zap.Error(err))
				}
				return
			}
			frame := common.MediaFrame{
				Type:       common.FrameAudio,
				Codec:      codec,
				Payload:    rtp.Payload,
				Timestamp:  rtp.Timestamp,
				Sequence:   rtp.SequenceNumber,
				SampleRate: track.Codec().ClockRate,
				Channels:   uint16(track.Codec().Channels),
			}
			s.handler.OnMediaFrame(session.id, frame)
		}
	})

	// ICE candidate → 通过信令 WS 发给客户端
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		candidateInit := candidate.ToJSON()
		msg := signalMessage{Type: "candidate", Candidate: candidateInit}
		_ = conn.WriteJSON(msg)
	})

	// 连接状态变化
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		s.log.Info("webrtc connection state", zap.String("session", session.id), zap.String("state", state.String()))
		switch state {
		case webrtc.PeerConnectionStateConnected:
			s.handler.OnEvent(common.ProtocolEvent{
				Type:      common.EventAnswered,
				Protocol:  common.ProtocolWebRTC,
				SessionID: session.id,
				Timestamp: time.Now(),
			})
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			session.mu.Lock()
			session.closed = true
			session.mu.Unlock()
		}
	})
}

// signalMessage 信令消息格式
type signalMessage struct {
	Type      string                `json:"type"`      // "offer", "answer", "candidate"
	SDP       string                `json:"sdp,omitempty"`
	Candidate webrtc.ICECandidateInit `json:"candidate,omitempty"`
}

func (s *Server) signalLoop(session *Session, conn *websocket.Conn) {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			s.log.Debug("webrtc signal read end", zap.String("session", session.id), zap.Error(err))
			return
		}

		var msg signalMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			s.log.Error("webrtc signal parse", zap.String("session", session.id), zap.Error(err))
			continue
		}

		switch msg.Type {
		case "offer":
			s.handleOffer(session, conn, msg)
		case "candidate":
			if err := session.pc.AddICECandidate(msg.Candidate); err != nil {
				s.log.Error("webrtc add ICE candidate", zap.String("session", session.id), zap.Error(err))
			}
		default:
			s.log.Warn("webrtc unknown signal type", zap.String("session", session.id), zap.String("type", msg.Type))
		}
	}
}

func (s *Server) handleOffer(session *Session, conn *websocket.Conn, msg signalMessage) {
	// 设置远端 SDP
	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: msg.SDP}
	if err := session.pc.SetRemoteDescription(offer); err != nil {
		s.log.Error("webrtc set remote description", zap.String("session", session.id), zap.Error(err))
		return
	}

	// 创建本地音频 Track（用于向客户端发音频）
	audioTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		"audio", "lingvoice",
	)
	if err != nil {
		s.log.Error("webrtc create audio track", zap.String("session", session.id), zap.Error(err))
		return
	}
	if _, err := session.pc.AddTrack(audioTrack); err != nil {
		s.log.Error("webrtc add track", zap.String("session", session.id), zap.Error(err))
		return
	}
	session.audioTrack = audioTrack

	// 创建 Answer
	answer, err := session.pc.CreateAnswer(nil)
	if err != nil {
		s.log.Error("webrtc create answer", zap.String("session", session.id), zap.Error(err))
		return
	}
	if err := session.pc.SetLocalDescription(answer); err != nil {
		s.log.Error("webrtc set local description", zap.String("session", session.id), zap.Error(err))
		return
	}

	// 发送 Answer 给客户端
	resp := signalMessage{Type: "answer", SDP: answer.SDP}
	if err := conn.WriteJSON(resp); err != nil {
		s.log.Error("webrtc send answer", zap.String("session", session.id), zap.Error(err))
	}
}

// --- Session 方法 ---

func (sess *Session) ID() string                    { return sess.id }
func (sess *Session) Protocol() common.ProtocolType { return common.ProtocolWebRTC }

func (sess *Session) SendCommand(cmd common.ProtocolCommand) error {
	switch cmd.Type {
	case common.CmdHangup:
		return sess.Close()
	default:
		return nil
	}
}

// SendMediaFrame 通过 WebRTC Track 发送音视频帧
func (sess *Session) SendMediaFrame(frame common.MediaFrame) error {
	if sess.audioTrack == nil {
		return fmt.Errorf("webrtc: no audio track")
	}
	// 将 payload 作为 RTP 包写入 Track
	// 注意：这里假设 payload 已是 RTP 格式，或需要封装为 RTP
	// 实际使用时需要构建 RTP header + payload
	_, err := sess.audioTrack.Write(frame.Payload)
	return err
}

func (sess *Session) Close() error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.closed {
		return nil
	}
	sess.closed = true
	return sess.pc.Close()
}

// --- 辅助 ---

func codecFromMimeType(mimeType string) string {
	// webrtc.MimeTypeOpus = "audio/opus"
	// webrtc.MimeTypePCMU = "audio/PCMU"
	switch mimeType {
	case webrtc.MimeTypeOpus:
		return "opus"
	case webrtc.MimeTypePCMU:
		return "pcmu"
	case webrtc.MimeTypePCMA:
		return "pcma"
	case webrtc.MimeTypeH264:
		return "h264"
	case webrtc.MimeTypeVP8:
		return "vp8"
	default:
		return ""
	}
}
