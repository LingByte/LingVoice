// Package whip implements WHIP (WebRTC HTTP Ingestion Protocol).
//
// WHIP 是 W3C 标准草案，用于通过 HTTP 推送 WebRTC 媒体流。
// 流程：
//   1. Client POST SDP offer → Server 返回 SDP answer + Location header (session URL)
//   2. Client 通过 PeerConnection 推送音视频
//   3. DELETE session URL → 结束推流
//
// 典型客户端：OBS、FFmpeg、GStreamer、浏览器
package whip

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/LingByte/ling-base/common/logger"
	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// Config WHIP 服务配置
type Config struct {
	Addr       string // 监听地址
	Path       string // WHIP endpoint 路径
	ICEServers []webrtc.ICEServer
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Addr:       ":8082",
		Path:       "/whip",
		ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
	}
}

// Server WHIP 推流接收服务
type Server struct {
	config   Config
	handler  common.EventHandler
	sessions sync.Map // map[string]*Session
	log      *zap.Logger
}

// Session WHIP 推流会话
type Session struct {
	id         string
	pc         *webrtc.PeerConnection
	handler    common.EventHandler
	mu         sync.Mutex
	closed     bool
	createdAt  time.Time
}

// NewServer 创建 WHIP 服务
func NewServer(config Config, handler common.EventHandler, log *zap.Logger) *Server {
	if log == nil {
		log = logger.Lg
	}
	return &Server{
		config:  config,
		handler: handler,
		log:     log.With(zap.String("component", "whip-server")),
	}
}

// Handler 返回 HTTP Handler
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.config.Path, s.handleWHIP)
	mux.HandleFunc(s.config.Path+"/", s.handleSession) // /whip/{sessionID}
	return mux
}

// Start 启动服务
func (s *Server) Start() error {
	s.log.Info("whip server starting", zap.String("addr", s.config.Addr), zap.String("path", s.config.Path))
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

// handleWHIP 处理 WHIP POST（SDP offer → answer）
func (s *Server) handleWHIP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 读取 SDP offer
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}

	// 创建 PeerConnection
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: s.config.ICEServers,
	})
	if err != nil {
		s.log.Error("whip create PC", zap.Error(err))
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

	// 设置 PeerConnection 回调
	s.setupPeerConnection(session)

	// 设置远端 SDP
	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(body)}
	if err := pc.SetRemoteDescription(offer); err != nil {
		s.log.Error("whip set remote desc", zap.String("session", sessionID), zap.Error(err))
		http.Error(w, "set remote desc failed", http.StatusBadRequest)
		pc.Close()
		s.sessions.Delete(sessionID)
		return
	}

	// 创建 Answer
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		s.log.Error("whip create answer", zap.String("session", sessionID), zap.Error(err))
		http.Error(w, "create answer failed", http.StatusInternalServerError)
		pc.Close()
		s.sessions.Delete(sessionID)
		return
	}

	// 设置本地描述（触发 ICE gathering）
	if err := pc.SetLocalDescription(answer); err != nil {
		s.log.Error("whip set local desc", zap.String("session", sessionID), zap.Error(err))
		http.Error(w, "set local desc failed", http.StatusInternalServerError)
		pc.Close()
		s.sessions.Delete(sessionID)
		return
	}

	// 等待 ICE gathering 完成
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	<-gatherComplete

	// 返回最终 answer（含所有 ICE candidates）
	finalAnswer := pc.LocalDescription()
	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Location", s.config.Path+"/"+sessionID)
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(finalAnswer.SDP))

	s.log.Info("whip session created", zap.String("session", sessionID), zap.String("remote", r.RemoteAddr))

	// 通知上层
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolWHIP,
		SessionID: sessionID,
		From:      r.RemoteAddr,
		To:        s.config.Path,
		Timestamp: time.Now(),
	})
}

// handleSession 处理 WHIP session 管理请求（DELETE/PATCH）
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	// 提取 sessionID: /whip/{sessionID}
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
			Protocol:  common.ProtocolWHIP,
			SessionID: sessionID,
			Timestamp: time.Now(),
		})
		w.WriteHeader(http.StatusOK)
	case http.MethodPatch:
		// ICE restart 等场景，暂不实现
		http.Error(w, "PATCH not supported yet", http.StatusNotImplemented)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) setupPeerConnection(session *Session) {
	pc := session.pc

	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		s.log.Info("whip track received",
			zap.String("session", session.id),
			zap.String("kind", track.Kind().String()),
			zap.String("codec", track.Codec().MimeType),
		)

		codec := codecFromMimeType(track.Codec().MimeType)
		trackID := common.TrackID(track.ID())
		var kind common.TrackKind
		var channels uint16
		if track.Kind() == webrtc.RTPCodecTypeAudio {
			kind = common.TrackAudio
			channels = uint16(track.Codec().Channels)
		} else {
			kind = common.TrackVideo
		}

		trackInfo := &common.TrackInfo{
			ID:         trackID,
			Kind:       kind,
			Direction:  common.TrackRecv,
			Codec:      codec,
			SampleRate: track.Codec().ClockRate,
			Channels:   channels,
			SSRC:       uint32(track.SSRC()),
			StreamID:   track.StreamID(),
		}

		s.handler.OnEvent(common.ProtocolEvent{
			Type:      common.EventTrackAdded,
			Protocol:  common.ProtocolWHIP,
			SessionID: session.id,
			Track:     trackInfo,
			Timestamp: time.Now(),
		})

		// 读 RTP 包
		for {
			rtp, _, err := track.ReadRTP()
			if err != nil {
				if err != io.EOF {
					s.log.Debug("whip track read end", zap.String("session", session.id), zap.Error(err))
				}
				return
			}
			frame := common.MediaFrame{
				Type:       frameTypeFromKind(track.Kind()),
				Codec:      codec,
				Payload:    rtp.Payload,
				Timestamp:  rtp.Timestamp,
				Sequence:   rtp.SequenceNumber,
				SampleRate: track.Codec().ClockRate,
				SSRC:       rtp.SSRC,
				Marker:     rtp.Marker,
				RID:        track.RID(),
			}
			s.handler.OnMediaFrame(session.id, trackID, frame)
		}
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		s.log.Info("whip connection state", zap.String("session", session.id), zap.String("state", state.String()))
		if state == webrtc.PeerConnectionStateConnected {
			s.handler.OnEvent(common.ProtocolEvent{
				Type:      common.EventAnswered,
				Protocol:  common.ProtocolWHIP,
				SessionID: session.id,
				Timestamp: time.Now(),
			})
		}
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			session.mu.Lock()
			session.closed = true
			session.mu.Unlock()
			s.handler.OnEvent(common.ProtocolEvent{
				Type:      common.EventHangup,
				Protocol:  common.ProtocolWHIP,
				SessionID: session.id,
				Timestamp: time.Now(),
			})
		}
	})
}

// --- Session 方法 ---

func (sess *Session) ID() string                    { return sess.id }
func (sess *Session) Protocol() common.ProtocolType { return common.ProtocolWHIP }

func (sess *Session) SendCommand(cmd common.ProtocolCommand) error {
	switch cmd.Type {
	case common.CmdHangup:
		return sess.Close()
	default:
		return nil
	}
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

func codecFromMimeType(mimeType string) common.CodecType {
	switch mimeType {
	case webrtc.MimeTypeOpus:
		return common.CodecOpus
	case webrtc.MimeTypePCMU:
		return common.CodecPCMU
	case webrtc.MimeTypePCMA:
		return common.CodecPCMA
	case webrtc.MimeTypeH264:
		return common.CodecH264
	case webrtc.MimeTypeVP8:
		return common.CodecVP8
	default:
		return common.CodecOpus
	}
}

func frameTypeFromKind(kind webrtc.RTPCodecType) common.FrameType {
	if kind == webrtc.RTPCodecTypeVideo {
		return common.FrameVideo
	}
	return common.FrameAudio
}

// 确保 context 被使用
var _ = context.Background
