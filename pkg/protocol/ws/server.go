package ws

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/LingByte/ling-base/common/logger"
	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/LingVoice/pkg/protocol/media"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Config WebSocket 服务配置
type Config struct {
	Addr           string // 监听地址，如 ":8080"
	Path           string // WS 路径，如 "/ws/voice"
	// 服务端默认支持的编解码（用于协商时选择）
	AudioCodecs      []string
	AudioSampleRate  uint32
	AudioChannels    uint16
	AudioFrameMs     uint16
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Addr:           ":8080",
		Path:           "/ws/voice",
		AudioCodecs:    []string{"opus", "pcmu", "pcma"},
		AudioSampleRate: 48000,
		AudioChannels:   1,
		AudioFrameMs:    20,
	}
}

// Server WebSocket 语音协议服务端
type Server struct {
	config   Config
	handler  common.EventHandler
	sessions sync.Map // map[string]*Session
	log      *zap.Logger
}

// NewServer 创建 WebSocket 语音服务
func NewServer(config Config, handler common.EventHandler, log *zap.Logger) *Server {
	if log == nil {
		log = logger.Lg
	}
	return &Server{
		config:  config,
		handler: handler,
		log:     log.With(zap.String("component", "ws-server")),
	}
}

// Handler 返回 HTTP Handler，可挂到已有 mux
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.config.Path, s.handleWebSocket)
	return mux
}

// Start 启动独立 HTTP 服务
func (s *Server) Start() error {
	s.log.Info("ws server starting", zap.String("addr", s.config.Addr), zap.String("path", s.config.Path))
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

// CloseSession 关闭并移除会话
func (s *Server) CloseSession(id string) {
	if v, ok := s.sessions.LoadAndDelete(id); ok {
		_ = v.(*Session).Close()
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Error("ws upgrade failed", zap.Error(err))
		return
	}
	defer conn.Close()

	sessionID := uuid.NewString()
	session := newSession(sessionID, conn, s.handler)
	s.sessions.Store(sessionID, session)
	defer s.sessions.Delete(sessionID)

	s.log.Info("ws session connected", zap.String("session", sessionID), zap.Stringer("remote", conn.RemoteAddr()))

	// 通知上层：有新连接（相当于来电）
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolWS,
		SessionID: sessionID,
		From:      conn.RemoteAddr().String(),
		To:        s.config.Path,
		Timestamp: time.Now(),
	})

	// 读循环
	s.readLoop(session)

	// 连接结束，通知上层挂断
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventHangup,
		Protocol:  common.ProtocolWS,
		SessionID: sessionID,
		Timestamp: time.Now(),
	})
	s.log.Info("ws session closed", zap.String("session", sessionID))
}

func (s *Server) readLoop(session *Session) {
	for {
		msgType, data, err := session.conn.ReadMessage()
		if err != nil {
			if !session.closed.Load() {
				s.log.Debug("ws read error", zap.String("session", session.id), zap.Error(err))
			}
			return
		}

		switch msgType {
		case websocket.TextMessage:
			if err := s.handleTextMessage(session, data); err != nil {
				s.log.Error("ws handle text message", zap.String("session", session.id), zap.Error(err))
			}
		case websocket.BinaryMessage:
			if err := s.handleBinaryMessage(session, data); err != nil {
				s.log.Error("ws handle binary message", zap.String("session", session.id), zap.Error(err))
			}
		}
	}
}

func (s *Server) handleTextMessage(session *Session, data []byte) error {
	// 先解析出 type 字段
	var base struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &base); err != nil {
		return s.sendError(session, "invalid-format", "invalid JSON: "+err.Error())
	}

	switch base.Type {
	case MsgTypeOffer:
		return s.handleOffer(session, data)
	case MsgTypeStart:
		return s.handleStart(session)
	case MsgTypeStop:
		s.handler.OnEvent(common.ProtocolEvent{
			Type:      common.EventHangup,
			Protocol:  common.ProtocolWS,
			SessionID: session.id,
			Timestamp: time.Now(),
		})
		return nil
	case MsgTypeDTMF:
		var msg DTMFMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return err
		}
		s.log.Debug("ws dtmf", zap.String("session", session.id), zap.String("digit", msg.Digit))
		return nil
	case MsgTypeEvent:
		var msg EventMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return err
		}
		s.log.Debug("ws event", zap.String("session", session.id), zap.String("event", msg.Event))
		return nil
	default:
		return s.sendError(session, "unknown-type", "unknown message type: "+base.Type)
	}
}

func (s *Server) handleOffer(session *Session, data []byte) error {
	if session.negotiated {
		return s.sendError(session, "already-negotiated", "session already negotiated")
	}

	var offer OfferMessage
	if err := json.Unmarshal(data, &offer); err != nil {
		return s.sendError(session, "invalid-offer", "invalid offer: "+err.Error())
	}

	if offer.Version != ProtocolVersion {
		return s.sendError(session, "version-mismatch",
			"protocol version mismatch: server="+itoa(ProtocolVersion))
	}

	// 协商音频编解码
	audio, err := s.negotiateAudio(offer.Media.Audio)
	if err != nil {
		return s.sendError(session, "no-codec", err.Error())
	}
	session.audio = audio

	// 协商视频编解码（可选）
	if offer.Media.Video != nil {
		video := s.negotiateVideo(offer.Media.Video)
		session.video = video
	}

	session.negotiated = true

	// 发送 Answer
	answer := AnswerMessage{
		Type:    MsgTypeAnswer,
		Session: session.id,
	}
	if audio != nil {
		answer.Media.Audio = &AnswerAudio{
			Codec:           audio.Codec.String(),
			SampleRate:      audio.SampleRate,
			Channels:        audio.Channels,
			FrameDurationMs: audio.FrameDurationMs,
		}
	}
	if session.video != nil {
		answer.Media.Video = &AnswerVideo{
			Codec:  session.video.Codec.String(),
			Width:  session.video.Width,
			Height: session.video.Height,
			FPS:    session.video.FPS,
		}
	}
	if err := session.sendJSON(answer); err != nil {
		return err
	}

	s.log.Info("ws negotiated",
		zap.String("session", session.id),
		zap.String("audioCodec", audio.Codec.String()),
		zap.Uint32("sampleRate", audio.SampleRate),
	)
	return nil
}

func (s *Server) handleStart(session *Session) error {
	if !session.negotiated {
		return s.sendError(session, "not-negotiated", "send offer first")
	}

	// 通知上层：媒体就绪
	media := &common.MediaDescription{Audio: session.audio, Video: session.video}
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventMediaReady,
		Protocol:  common.ProtocolWS,
		SessionID: session.id,
		Media:     media,
		Timestamp: time.Now(),
	})

	// 回复 ready
	return session.sendJSON(ReadyMessage{
		Type:      MsgTypeReady,
		Timestamp: time.Now().UnixMilli(),
	})
}

func (s *Server) handleBinaryMessage(session *Session, data []byte) error {
	if !session.negotiated {
		return s.sendError(session, "not-negotiated", "send offer first, then start")
	}

	frame, err := DecodeFrame(data)
	if err != nil {
		return err
	}

	// 填充采样率/声道（从协商结果）
	if frame.Type == common.FrameAudio && session.audio != nil {
		frame.SampleRate = session.audio.SampleRate
		frame.Channels = session.audio.Channels
	}

	// 回调上层
	return s.handler.OnMediaFrame(session.id, frame)
}

// negotiateAudio 从客户端提供的列表中选择服务端支持的编解码。
// 使用 pkg/media encoder registry 验证编解码实际可用性。
func (s *Server) negotiateAudio(offer *OfferAudio) (*common.AudioMedia, error) {
	if offer == nil || len(offer.Codecs) == 0 {
		return nil, ErrCodecNotSupported
	}

	result, err := media.NegotiateAudio(
		s.config.AudioCodecs,
		offer.Codecs,
		offer.SampleRates,
		offer.Channels,
		s.config.AudioSampleRate,
		s.config.AudioChannels,
		s.config.AudioFrameMs,
	)
	if err != nil {
		return nil, ErrCodecNotSupported
	}
	return result.Audio, nil
}

func (s *Server) negotiateVideo(offer *OfferVideo) *common.VideoMedia {
	if offer == nil || len(offer.Codecs) == 0 {
		return nil
	}
	for _, cc := range offer.Codecs {
		codec, err := common.CodecFromString(cc)
		if err != nil {
			continue
		}
		return &common.VideoMedia{
			Codec: codec,
			Width:  640,
			Height: 480,
			FPS:    30,
		}
	}
	return nil
}

func (s *Server) sendError(session *Session, code, message string) error {
	return session.sendJSON(ErrorMessage{
		Type:    MsgTypeError,
		Code:    code,
		Message: message,
	})
}

// itoa 简单 int → string
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
