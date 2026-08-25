// Package whep implements WHEP (WebRTC HTTP Egress Protocol).
//
// WHEP 是 WHIP 的反向协议，用于通过 HTTP 拉取 WebRTC 媒体流。
// 流程：
//   1. Client POST SDP offer → Server 创建 Track，返回 SDP answer + Location header
//   2. Server 通过 PeerConnection 向 Client 推送音视频
//   3. DELETE session URL → 结束拉流
//
// 典型客户端：浏览器、VLC、播放器
package whep

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/ling-base/common/logger"
	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// Config WHEP 服务配置
type Config struct {
	Addr       string // 监听地址
	Path       string // WHEP endpoint 路径
	ICEServers []webrtc.ICEServer
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Addr:       ":8083",
		Path:       "/whep",
		ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
	}
}

// Server WHEP 拉流服务
type Server struct {
	config   Config
	handler  common.EventHandler
	sessions sync.Map // map[string]*Session
	log      *zap.Logger
}

// Session WHEP 拉流会话
type Session struct {
	id         string
	pc         *webrtc.PeerConnection
	audioTrack *webrtc.TrackLocalStaticRTP
	videoTrack *webrtc.TrackLocalStaticRTP
	handler    common.EventHandler
	mu         sync.Mutex
	closed     bool
	createdAt  time.Time
}

// NewServer 创建 WHEP 服务
func NewServer(config Config, handler common.EventHandler, log *zap.Logger) *Server {
	if log == nil {
		log = logger.Lg
	}
	return &Server{
		config:  config,
		handler: handler,
		log:     log.With(zap.String("component", "whep-server")),
	}
}

// Handler 返回 HTTP Handler
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.config.Path, s.handleWHEP)
	mux.HandleFunc(s.config.Path+"/", s.handleSession)
	return mux
}

// Start 启动服务
func (s *Server) Start() error {
	s.log.Info("whep server starting", zap.String("addr", s.config.Addr), zap.String("path", s.config.Path))
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

// handleWHEP 处理 WHEP POST（SDP offer → answer）
func (s *Server) handleWHEP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: s.config.ICEServers,
	})
	if err != nil {
		s.log.Error("whep create PC", zap.Error(err))
		http.Error(w, "create PC failed", http.StatusInternalServerError)
		return
	}

	sessionID := uuid.NewString()
	session := &Session{
		id:        sessionID,
		pc:        pc,
		handler:   s.handler,
		createdAt: time.Now(),
	}
	s.sessions.Store(sessionID, session)

	// 创建音频 Track（Opus，用于向客户端推音频）
	audioTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		"audio", "lingvoice",
	)
	if err != nil {
		s.log.Error("whep create audio track", zap.Error(err))
		http.Error(w, "create track failed", http.StatusInternalServerError)
		pc.Close()
		s.sessions.Delete(sessionID)
		return
	}
	if _, err := pc.AddTrack(audioTrack); err != nil {
		s.log.Error("whep add audio track", zap.Error(err))
		http.Error(w, "add track failed", http.StatusInternalServerError)
		pc.Close()
		s.sessions.Delete(sessionID)
		return
	}
	session.audioTrack = audioTrack

	// 创建视频 Track（H264，可选）
	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000},
		"video", "lingvoice",
	)
	if err == nil {
		if _, err := pc.AddTrack(videoTrack); err == nil {
			session.videoTrack = videoTrack
		}
	}

	// 设置远端 SDP
	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(body)}
	if err := pc.SetRemoteDescription(offer); err != nil {
		s.log.Error("whep set remote desc", zap.Error(err))
		http.Error(w, "set remote desc failed", http.StatusBadRequest)
		pc.Close()
		s.sessions.Delete(sessionID)
		return
	}

	// 创建 Answer
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		s.log.Error("whep create answer", zap.Error(err))
		http.Error(w, "create answer failed", http.StatusInternalServerError)
		pc.Close()
		s.sessions.Delete(sessionID)
		return
	}

	if err := pc.SetLocalDescription(answer); err != nil {
		s.log.Error("whep set local desc", zap.Error(err))
		http.Error(w, "set local desc failed", http.StatusInternalServerError)
		pc.Close()
		s.sessions.Delete(sessionID)
		return
	}

	// 等待 ICE gathering
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	<-gatherComplete

	finalAnswer := pc.LocalDescription()
	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Location", s.config.Path+"/"+sessionID)
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(finalAnswer.SDP))

	s.log.Info("whep session created", zap.String("session", sessionID), zap.String("remote", r.RemoteAddr))

	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolWHEP,
		SessionID: sessionID,
		From:      r.RemoteAddr,
		To:        s.config.Path,
		Timestamp: time.Now(),
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		s.log.Info("whep connection state", zap.String("session", sessionID), zap.String("state", state.String()))
		switch state {
		case webrtc.PeerConnectionStateConnected:
			s.handler.OnEvent(common.ProtocolEvent{
				Type:      common.EventAnswered,
				Protocol:  common.ProtocolWHEP,
				SessionID: sessionID,
				Timestamp: time.Now(),
			})
			s.handler.OnEvent(common.ProtocolEvent{
				Type:      common.EventMediaReady,
				Protocol:  common.ProtocolWHEP,
				SessionID: sessionID,
				Media: &common.MediaDescription{
					Audio: &common.AudioMedia{Codec: common.CodecOpus, SampleRate: 48000, Channels: 2, FrameDurationMs: 20},
				},
				Timestamp: time.Now(),
			})
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			session.mu.Lock()
			session.closed = true
			session.mu.Unlock()
			s.handler.OnEvent(common.ProtocolEvent{
				Type:      common.EventHangup,
				Protocol:  common.ProtocolWHEP,
				SessionID: sessionID,
				Timestamp: time.Now(),
			})
		}
	})
}

// handleSession 处理 WHEP session 管理
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, s.config.Path+"/")
	sessionID := strings.SplitN(path, "/", 2)[0]

	session, ok := s.GetSession(sessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		_ = session.Close()
		s.sessions.Delete(sessionID)
		s.handler.OnEvent(common.ProtocolEvent{
			Type:      common.EventHangup,
			Protocol:  common.ProtocolWHEP,
			SessionID: sessionID,
			Timestamp: time.Now(),
		})
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- Session 方法 ---

func (sess *Session) ID() string                    { return sess.id }
func (sess *Session) Protocol() common.ProtocolType { return common.ProtocolWHEP }

func (sess *Session) SendCommand(cmd common.ProtocolCommand) error {
	switch cmd.Type {
	case common.CmdHangup:
		return sess.Close()
	default:
		return nil
	}
}

// SendMediaFrame 向拉流客户端发送音视频帧
func (sess *Session) SendMediaFrame(frame common.MediaFrame) error {
	if frame.Type == common.FrameAudio && sess.audioTrack != nil {
		_, err := sess.audioTrack.Write(frame.Payload)
		return err
	}
	if frame.Type == common.FrameVideo && sess.videoTrack != nil {
		_, err := sess.videoTrack.Write(frame.Payload)
		return err
	}
	return fmt.Errorf("whep: no matching track for frame type %d", frame.Type)
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
