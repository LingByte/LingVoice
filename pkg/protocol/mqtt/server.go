// Package mqtt implements MQTT-based voice/media transport.
//
// MQTT 适合 IoT 场景和低带宽语音对话：
//   - 信令通过 JSON topic 交换（offer/answer/hangup/command）
//   - 音频帧通过 binary topic 发布/订阅（Opus/PCMU 等压缩格式）
//   - 视频帧理论上也可传（MQTT payload 最大 256MB），但 TCP 无时间戳机制，不推荐实时视频
//
// Topic 设计：
//   lingvoice/{session}/signal    — JSON 信令消息
//   lingvoice/{session}/media/in  — 客户端→服务端 媒体帧（二进制）
//   lingvoice/{session}/media/out — 服务端→客户端 媒体帧（二进制）
//
// 二进制媒体帧格式（与 WS 协议一致）：
//   [1B type][1B codec][4B timestamp][2B sequence][2B sampleRate][2B channels][payload...]
package mqtt

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/ling-base/common/logger"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Config MQTT 服务配置
type Config struct {
	Broker   string // MQTT broker 地址，如 "tcp://localhost:1883"
	ClientID string // 客户端 ID
	TopicPrefix string // topic 前缀，如 "lingvoice"
	QoS      byte    // QoS 级别（0/1/2），音频推荐 0
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Broker:      "tcp://localhost:1883",
		ClientID:    "lingvoice-server",
		TopicPrefix: "lingvoice",
		QoS:         0,
	}
}

// Server MQTT 协议服务
type Server struct {
	config   Config
	handler  common.EventHandler
	sessions sync.Map // map[string]*Session
	log      *zap.Logger
	client   pahomqtt.Client
}

// Session MQTT 会话
type Session struct {
	id        string
	server    *Server
	handler   common.EventHandler
	negotiated bool
	audio     *common.AudioMedia
	video     *common.VideoMedia
	mu        sync.Mutex
	closed    bool
	createdAt time.Time
}

// --- 信令消息类型 ---

const (
	MsgOffer  = "offer"
	MsgAnswer = "answer"
	MsgStart  = "start"
	MsgReady  = "ready"
	MsgStop   = "stop"
	MsgError  = "error"
)

type signalMessage struct {
	Type    string `json:"type"`
	Session string `json:"session,omitempty"`

	// Offer 字段
	Media *offerMedia `json:"media,omitempty"`

	// Answer 字段
	AnswerMedia *answerMedia `json:"answerMedia,omitempty"`

	// Error 字段
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`

	// Ready 字段
	Timestamp int64 `json:"timestamp,omitempty"`
}

type offerMedia struct {
	Audio *offerAudio `json:"audio,omitempty"`
	Video *offerVideo `json:"video,omitempty"`
}

type offerAudio struct {
	Codecs      []string `json:"codecs"`
	SampleRate  uint32   `json:"sampleRate"`
	Channels    uint16   `json:"channels"`
	FrameMs     uint16   `json:"frameMs"`
}

type offerVideo struct {
	Codecs []string `json:"codecs"`
	Width  uint16   `json:"width"`
	Height uint16   `json:"height"`
	FPS    uint8    `json:"fps"`
}

type answerMedia struct {
	Audio *answerAudio `json:"audio,omitempty"`
	Video *answerVideo `json:"video,omitempty"`
}

type answerAudio struct {
	Codec           string `json:"codec"`
	SampleRate      uint32 `json:"sampleRate"`
	Channels        uint16 `json:"channels"`
	FrameDurationMs uint16 `json:"frameMs"`
}

type answerVideo struct {
	Codec  string `json:"codec"`
	Width  uint16 `json:"width"`
	Height uint16 `json:"height"`
	FPS    uint8  `json:"fps"`
}

// NewServer 创建 MQTT 服务
func NewServer(config Config, handler common.EventHandler, log *zap.Logger) *Server {
	if log == nil {
		log = logger.Lg
	}
	return &Server{
		config:  config,
		handler: handler,
		log:     log.With(zap.String("component", "mqtt-server")),
	}
}

// Start 连接 MQTT broker 并订阅信令 topic
func (s *Server) Start() error {
	opts := pahomqtt.NewClientOptions()
	opts.AddBroker(s.config.Broker)
	opts.SetClientID(s.config.ClientID)
	opts.SetAutoReconnect(true)
	opts.SetOnConnectHandler(func(c pahomqtt.Client) {
		s.log.Info("mqtt connected", zap.String("broker", s.config.Broker))
		// 订阅信令通配 topic: lingvoice/+/signal
		signalTopic := fmt.Sprintf("%s/+/signal", s.config.TopicPrefix)
		if token := s.client.Subscribe(signalTopic, s.config.QoS, s.onSignalMessage); token.Wait() && token.Error() != nil {
			s.log.Error("mqtt subscribe signal", zap.Error(token.Error()))
		} else {
			s.log.Info("mqtt subscribed", zap.String("topic", signalTopic))
		}
	})
	opts.SetConnectionLostHandler(func(c pahomqtt.Client, err error) {
		s.log.Error("mqtt connection lost", zap.Error(err))
	})

	s.client = pahomqtt.NewClient(opts)
	if token := s.client.Connect(); token.Wait() && token.Error() != nil {
		return fmt.Errorf("mqtt connect: %w", token.Error())
	}

	s.log.Info("mqtt server starting", zap.String("broker", s.config.Broker), zap.String("clientID", s.config.ClientID))
	return nil
}

// Close 关闭 MQTT 连接
func (s *Server) Close() error {
	if s.client != nil && s.client.IsConnected() {
		s.client.Disconnect(500) // 500ms 等待
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

// onSignalMessage 处理信令消息
func (s *Server) onSignalMessage(_ pahomqtt.Client, msg pahomqtt.Message) {
	// topic: lingvoice/{session}/signal
	topic := msg.Topic()
	sessionID, err := parseSessionFromTopic(topic, s.config.TopicPrefix, "signal")
	if err != nil {
		s.log.Error("mqtt parse topic", zap.String("topic", topic), zap.Error(err))
		return
	}

	var sig signalMessage
	if err := json.Unmarshal(msg.Payload(), &sig); err != nil {
		s.log.Error("mqtt parse signal", zap.String("session", sessionID), zap.Error(err))
		return
	}

	// 如果消息里带了 session 字段，以它为准
	if sig.Session != "" {
		sessionID = sig.Session
	}

	switch sig.Type {
	case MsgOffer:
		s.handleOffer(sessionID, &sig)
	case MsgStart:
		s.handleStart(sessionID)
	case MsgStop:
		s.handleStop(sessionID)
	default:
		s.log.Warn("mqtt unknown signal", zap.String("session", sessionID), zap.String("type", sig.Type))
	}
}

func (s *Server) handleOffer(sessionID string, sig *signalMessage) {
	// 创建或获取 session
	sess, ok := s.GetSession(sessionID)
	if !ok {
		sess = &Session{
			id:        sessionID,
			server:    s,
			handler:   s.handler,
			createdAt: time.Now(),
		}
		s.sessions.Store(sessionID, sess)
	}

	if sess.negotiated {
		s.publishSignal(sess, signalMessage{
			Type:    MsgError,
			Code:    "already-negotiated",
			Message: "session already negotiated",
		})
		return
	}

	// 协商音频
	var audio *common.AudioMedia
	if sig.Media != nil && sig.Media.Audio != nil {
		audio = s.negotiateAudio(sig.Media.Audio)
	}
	sess.audio = audio

	// 协商视频（可选）
	if sig.Media != nil && sig.Media.Video != nil {
		sess.video = s.negotiateVideo(sig.Media.Video)
	}

	sess.negotiated = true

	// 订阅该 session 的媒体 topic: lingvoice/{session}/media/in
	mediaTopic := fmt.Sprintf("%s/%s/media/in", s.config.TopicPrefix, sessionID)
	if token := s.client.Subscribe(mediaTopic, s.config.QoS, s.onMediaMessage); token.Wait() && token.Error() != nil {
		s.log.Error("mqtt subscribe media", zap.String("topic", mediaTopic), zap.Error(token.Error()))
	}

	// 发送 Answer
	answer := signalMessage{
		Type:    MsgAnswer,
		Session: sessionID,
	}
	if audio != nil {
		answer.AnswerMedia = &answerMedia{
			Audio: &answerAudio{
				Codec:           audio.Codec.String(),
				SampleRate:      audio.SampleRate,
				Channels:        audio.Channels,
				FrameDurationMs: audio.FrameDurationMs,
			},
		}
	}
	if sess.video != nil {
		answer.AnswerMedia = &answerMedia{
			Video: &answerVideo{
				Codec:  sess.video.Codec.String(),
				Width:  sess.video.Width,
				Height: sess.video.Height,
				FPS:    sess.video.FPS,
			},
		}
		if answer.AnswerMedia.Audio == nil && audio != nil {
			answer.AnswerMedia.Audio = &answerAudio{
				Codec:           audio.Codec.String(),
				SampleRate:      audio.SampleRate,
				Channels:        audio.Channels,
				FrameDurationMs: audio.FrameDurationMs,
			}
		}
	}
	s.publishSignal(sess, answer)

	s.log.Info("mqtt negotiated", zap.String("session", sessionID), zap.String("codec", audio.Codec.String()))

	// 通知上层：来电
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolMQTT,
		SessionID: sessionID,
		To:        sessionID,
		Timestamp: time.Now(),
	})
}

func (s *Server) handleStart(sessionID string) {
	sess, ok := s.GetSession(sessionID)
	if !ok || !sess.negotiated {
		s.publishSignalByID(sessionID, signalMessage{
			Type:    MsgError,
			Code:    "not-negotiated",
			Message: "send offer first",
		})
		return
	}

	// 通知上层：媒体就绪
	media := &common.MediaDescription{Audio: sess.audio, Video: sess.video}
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventMediaReady,
		Protocol:  common.ProtocolMQTT,
		SessionID: sessionID,
		Media:     media,
		Timestamp: time.Now(),
	})

	// 回复 ready
	s.publishSignal(sess, signalMessage{
		Type:      MsgReady,
		Timestamp: time.Now().UnixMilli(),
	})
}

func (s *Server) handleStop(sessionID string) {
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventHangup,
		Protocol:  common.ProtocolMQTT,
		SessionID: sessionID,
		Timestamp: time.Now(),
	})
	s.closeSession(sessionID)
}

// onMediaMessage 处理媒体帧
func (s *Server) onMediaMessage(_ pahomqtt.Client, msg pahomqtt.Message) {
	topic := msg.Topic()
	sessionID, err := parseSessionFromTopic(topic, s.config.TopicPrefix, "media/in")
	if err != nil {
		return
	}

	sess, ok := s.GetSession(sessionID)
	if !ok || !sess.negotiated {
		return
	}

	frame, err := decodeFrame(msg.Payload())
	if err != nil {
		s.log.Debug("mqtt decode frame", zap.String("session", sessionID), zap.Error(err))
		return
	}

	// 填充采样率/声道
	if frame.Type == common.FrameAudio && sess.audio != nil {
		frame.SampleRate = sess.audio.SampleRate
		frame.Channels = sess.audio.Channels
	}

	s.handler.OnMediaFrame(sessionID, frame)
}

// negotiateAudio 协商音频编解码
func (s *Server) negotiateAudio(offer *offerAudio) *common.AudioMedia {
	supported := map[string]bool{
		"opus": true, "pcmu": true, "pcma": true, "pcm16": true,
	}
	for _, c := range offer.Codecs {
		if supported[c] {
			codec, _ := common.CodecFromString(c)
			sr := offer.SampleRate
			if sr == 0 {
				sr = 48000
			}
			ch := offer.Channels
			if ch == 0 {
				ch = 1
			}
			ms := offer.FrameMs
			if ms == 0 {
				ms = 20
			}
			return &common.AudioMedia{
				Codec:           codec,
				SampleRate:      sr,
				Channels:        ch,
				FrameDurationMs: ms,
			}
		}
	}
	// 默认 Opus
	return &common.AudioMedia{
		Codec:           common.CodecOpus,
		SampleRate:      48000,
		Channels:        1,
		FrameDurationMs: 20,
	}
}

func (s *Server) negotiateVideo(offer *offerVideo) *common.VideoMedia {
	supported := map[string]bool{"h264": true, "vp8": true}
	for _, c := range offer.Codecs {
		if supported[c] {
			codec, _ := common.CodecFromString(c)
			return &common.VideoMedia{
				Codec: codec,
				Width:  offer.Width,
				Height: offer.Height,
				FPS:    offer.FPS,
			}
		}
	}
	return nil
}

// publishSignal 发布信令消息
func (s *Server) publishSignal(sess *Session, msg signalMessage) {
	msg.Session = sess.id
	data, _ := json.Marshal(msg)
	topic := fmt.Sprintf("%s/%s/signal", s.config.TopicPrefix, sess.id)
	s.client.Publish(topic, s.config.QoS, false, data)
}

func (s *Server) publishSignalByID(sessionID string, msg signalMessage) {
	msg.Session = sessionID
	data, _ := json.Marshal(msg)
	topic := fmt.Sprintf("%s/%s/signal", s.config.TopicPrefix, sessionID)
	s.client.Publish(topic, s.config.QoS, false, data)
}

func (s *Server) closeSession(sessionID string) {
	if v, ok := s.sessions.LoadAndDelete(sessionID); ok {
		v.(*Session).Close()
	}
	// 取消订阅媒体 topic
	mediaTopic := fmt.Sprintf("%s/%s/media/in", s.config.TopicPrefix, sessionID)
	s.client.Unsubscribe(mediaTopic)
}

// parseSessionFromTopic 从 topic 解析 session ID
// topic 格式: {prefix}/{session}/{suffix}
func parseSessionFromTopic(topic, prefix, suffix string) (string, error) {
	expected := prefix + "/"
	if len(topic) <= len(expected) {
		return "", fmt.Errorf("topic too short")
	}
	rest := topic[len(expected):]
	// 找最后一个 "/"
	idx := -1
	for i := len(rest) - 1; i >= 0; i-- {
		if rest[i] == '/' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", fmt.Errorf("no session separator")
	}
	suffixPart := rest[idx+1:]
	if suffixPart != suffix {
		return "", fmt.Errorf("suffix mismatch: %s != %s", suffixPart, suffix)
	}
	return rest[:idx], nil
}

// --- Session 方法 ---

func (sess *Session) ID() string                    { return sess.id }
func (sess *Session) Protocol() common.ProtocolType { return common.ProtocolMQTT }

func (sess *Session) SendCommand(cmd common.ProtocolCommand) error {
	switch cmd.Type {
	case common.CmdAnswer:
		return nil // MQTT 协商成功即接听
	case common.CmdReject:
		sess.server.publishSignal(sess, signalMessage{
			Type:    MsgError,
			Code:    "rejected",
			Message: cmd.Reason,
		})
		return sess.Close()
	case common.CmdHangup:
		sess.server.publishSignal(sess, signalMessage{Type: MsgStop})
		return sess.Close()
	case common.CmdStartMedia:
		sess.server.publishSignal(sess, signalMessage{
			Type:      MsgReady,
			Timestamp: time.Now().UnixMilli(),
		})
		return nil
	case common.CmdStopMedia:
		sess.server.publishSignal(sess, signalMessage{Type: MsgStop})
		return nil
	default:
		return nil
	}
}

// SendMediaFrame 向客户端发送媒体帧（发布到 media/out topic）
func (sess *Session) SendMediaFrame(frame common.MediaFrame) error {
	if sess.closed {
		return fmt.Errorf("session closed")
	}
	data := encodeFrame(frame)
	topic := fmt.Sprintf("%s/%s/media/out", sess.server.config.TopicPrefix, sess.id)
	token := sess.server.client.Publish(topic, sess.server.config.QoS, false, data)
	token.Wait()
	return token.Error()
}

func (sess *Session) Close() error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.closed {
		return nil
	}
	sess.closed = true
	return nil
}

// --- 媒体帧编解码（与 WS 协议一致的二进制格式）---

// 帧格式: [1B type][1B codec][4B timestamp][2B sequence][4B sampleRate][2B channels][payload...]
const frameHeaderLen = 1 + 1 + 4 + 2 + 4 + 2

func encodeFrame(frame common.MediaFrame) []byte {
	buf := make([]byte, frameHeaderLen+len(frame.Payload))
	buf[0] = byte(frame.Type)
	buf[1] = byte(frame.Codec)
	binary.BigEndian.PutUint32(buf[2:6], frame.Timestamp)
	binary.BigEndian.PutUint16(buf[6:8], frame.Sequence)
	binary.BigEndian.PutUint32(buf[8:12], frame.SampleRate)
	binary.BigEndian.PutUint16(buf[12:14], frame.Channels)
	copy(buf[14:], frame.Payload)
	return buf
}

func decodeFrame(data []byte) (common.MediaFrame, error) {
	if len(data) < frameHeaderLen {
		return common.MediaFrame{}, fmt.Errorf("frame too short: %d", len(data))
	}
	frame := common.MediaFrame{
		Type:       common.FrameType(data[0]),
		Codec:      common.CodecType(data[1]),
		Timestamp:  binary.BigEndian.Uint32(data[2:6]),
		Sequence:   binary.BigEndian.Uint16(data[6:8]),
		SampleRate: binary.BigEndian.Uint32(data[8:12]),
		Channels:   binary.BigEndian.Uint16(data[12:14]),
		Payload:    data[14:],
	}
	return frame, nil
}

// GenerateSessionID 生成一个 MQTT session ID（供客户端使用）
func GenerateSessionID() string {
	return uuid.NewString()
}
