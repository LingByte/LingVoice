package webrtc

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// Session WebRTC 会话（双 PC 架构）
type Session struct {
	id        string
	config    Config
	log       *zap.Logger
	handler   common.EventHandler

	pub  *publisher
	sub  *subscriber
	qos  *QoSReporter

	// DataChannel
	mu             sync.RWMutex
	dataChannels   map[string]*webrtc.DataChannel

	// 状态
	closed        atomic.Bool
	connectedAt   atomic.Value // time.Time

	// 信令连接（用于发送消息给客户端）
	signalMu      sync.Mutex
	signalSend    func(msg signalMessage) error
}

func newSession(id string, cfg Config, handler common.EventHandler, log *zap.Logger) (*Session, error) {
	pub, err := newPublisher(cfg, log)
	if err != nil {
		return nil, fmt.Errorf("create publisher: %w", err)
	}

	sub, err := newSubscriber(cfg, log)
	if err != nil {
		pub.close()
		return nil, fmt.Errorf("create subscriber: %w", err)
	}

	sess := &Session{
		id:           id,
		config:       cfg,
		log:          log.With(zap.String("session", id)),
		handler:      handler,
		pub:          pub,
		sub:          sub,
		qos:          newQoSReporter(pub, sub),
		dataChannels: make(map[string]*webrtc.DataChannel),
	}

	sess.setupCallbacks()
	return sess, nil
}

func (s *Session) setupCallbacks() {
	// Publisher 回调
	s.pub.onFrame = func(trackID common.TrackID, frame common.MediaFrame) {
		_ = s.handler.OnMediaFrame(s.id, trackID, frame)
	}
	s.pub.onTrack = func(info common.TrackInfo) {
		_ = s.handler.OnEvent(common.ProtocolEvent{
			Type:      common.EventTrackAdded,
			Protocol:  common.ProtocolWebRTC,
			SessionID: s.id,
			Track:     &info,
			Timestamp: time.Now(),
		})
	}
	s.pub.onStateChange = func(state webrtc.PeerConnectionState) {
		s.handleConnectionState(state)
	}

	// Subscriber 回调
	s.sub.onOfferNeeded = func(offer webrtc.SessionDescription) {
		s.sendSignal(signalMessage{Type: "sub_offer", SDP: offer.SDP})
	}
	s.sub.onStateChange = func(state webrtc.PeerConnectionState) {
		s.log.Info("subscriber state", zap.String("state", state.String()))
	}

	// Publisher DataChannel（客户端发起的）
	s.pub.pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		s.setupDataChannel(dc)
	})
}

func (s *Session) handleConnectionState(state webrtc.PeerConnectionState) {
	s.log.Info("publisher connection state", zap.String("state", state.String()))

	switch state {
	case webrtc.PeerConnectionStateConnected:
		s.connectedAt.Store(time.Now())
		s.qos.SetConnected()
		_ = s.handler.OnEvent(common.ProtocolEvent{
			Type:      common.EventAnswered,
			Protocol:  common.ProtocolWebRTC,
			SessionID: s.id,
			Timestamp: time.Now(),
		})

	case webrtc.PeerConnectionStateFailed:
		// ICE restart 可恢复，不立即挂断
		s.log.Warn("publisher PC failed, attempting ICE restart")

	case webrtc.PeerConnectionStateClosed:
		s.closed.Store(true)
	}
}

// --- SignalSession 接口 ---

func (s *Session) ID() string                    { return s.id }
func (s *Session) Protocol() common.ProtocolType { return common.ProtocolWebRTC }

func (s *Session) SendCommand(cmd common.ProtocolCommand) error {
	switch cmd.Type {
	case common.CmdHangup:
		return s.Close()
	case common.CmdIceRestart:
		return s.iceRestart()
	default:
		return nil
	}
}

// --- MediaSession 接口 ---

func (s *Session) SendMediaFrame(trackID common.TrackID, frame common.MediaFrame) error {
	return s.sub.sendMediaFrame(trackID, frame)
}

func (s *Session) Tracks() []common.TrackInfo {
	var tracks []common.TrackInfo
	tracks = append(tracks, s.pub.tracks()...)
	tracks = append(tracks, s.sub.tracks()...)
	return tracks
}

func (s *Session) MediaStats() map[common.TrackID]common.TrackStats {
	result := make(map[common.TrackID]common.TrackStats)
	for id, st := range s.pub.trackStats() {
		result[id] = st
	}
	for id, st := range s.sub.trackStats() {
		result[id] = st
	}
	return result
}

// --- TrackManager 接口 ---

func (s *Session) AddTrack(cfg common.TrackConfig) (common.TrackID, error) {
	return s.sub.addTrack(cfg)
}

func (s *Session) RemoveTrack(trackID common.TrackID) error {
	return s.sub.removeTrack(trackID)
}

// --- DataSession 接口 ---

func (s *Session) SendData(channel string, data []byte) error {
	s.mu.RLock()
	dc, ok := s.dataChannels[channel]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("data channel not found: %s", channel)
	}
	// 优先用 SendText 发送（让前端 onmessage 收到 string 而非 ArrayBuffer）
	return dc.SendText(string(data))
}

// --- QoS ---

// QoS 采集当前 QoS 指标
func (s *Session) QoS() SessionStats {
	return s.qos.Collect()
}

// --- ICE restart ---

func (s *Session) iceRestart() error {
	// pion v4: 通过 CreateOffer + ICERestart: true 触发 ICE restart
	// 重启 publisher PC 的 ICE
	offer, err := s.pub.pc.CreateOffer(&webrtc.OfferOptions{
		ICERestart: true,
	})
	if err != nil {
		return fmt.Errorf("create publisher restart offer: %w", err)
	}
	if err := s.pub.pc.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("set publisher local restart offer: %w", err)
	}

	// 发送给客户端
	s.sendSignal(signalMessage{Type: "restart_offer", SDP: offer.SDP})
	return nil
}

// --- 信令处理 ---

// setSignalSend 设置信令发送函数
func (s *Session) setSignalSend(fn func(msg signalMessage) error) {
	s.signalMu.Lock()
	s.signalSend = fn
	s.signalMu.Unlock()
}

func (s *Session) sendSignal(msg signalMessage) {
	s.signalMu.Lock()
	fn := s.signalSend
	s.signalMu.Unlock()
	if fn != nil {
		if err := fn(msg); err != nil {
			s.log.Error("send signal", zap.String("type", msg.Type), zap.Error(err))
		}
	}
}

// handlePublisherOffer 处理客户端发来的 publisher SDP offer
func (s *Session) handlePublisherOffer(sdp string) {
	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}
	answer, err := s.pub.handleOffer(offer)
	if err != nil {
		s.log.Error("publisher handle offer", zap.Error(err))
		_ = s.handler.OnEvent(common.ProtocolEvent{
			Type:      common.EventError,
			Protocol:  common.ProtocolWebRTC,
			SessionID: s.id,
			Err:       err,
			Timestamp: time.Now(),
		})
		return
	}
	s.sendSignal(signalMessage{Type: "pub_answer", SDP: answer.SDP})
}

// handleSubscriberAnswer 处理客户端发来的 subscriber SDP answer
func (s *Session) handleSubscriberAnswer(sdp string) {
	answer := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}
	if err := s.sub.handleAnswer(answer); err != nil {
		s.log.Error("subscriber handle answer", zap.Error(err))
	}
}

// handleCandidate 处理 ICE candidate
func (s *Session) handleCandidate(target string, candidate webrtc.ICECandidateInit) {
	switch target {
	case "publisher":
		if err := s.pub.addICECandidate(candidate); err != nil {
			s.log.Error("publisher add ICE candidate", zap.Error(err))
		}
	case "subscriber":
		if err := s.sub.addICECandidate(candidate); err != nil {
			s.log.Error("subscriber add ICE candidate", zap.Error(err))
		}
	default:
		s.log.Warn("unknown ICE candidate target", zap.String("target", target))
	}
}

// --- DataChannel ---

func (s *Session) setupDataChannel(dc *webrtc.DataChannel) {
	label := dc.Label()
	s.log.Info("data channel opened", zap.String("label", label))

	s.mu.Lock()
	s.dataChannels[label] = dc
	s.mu.Unlock()

	dc.OnOpen(func() {
		_ = s.handler.OnEvent(common.ProtocolEvent{
			Type:      common.EventDataChannel,
			Protocol:  common.ProtocolWebRTC,
			SessionID: s.id,
			Timestamp: time.Now(),
		})
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		_ = s.handler.OnData(s.id, common.DataMessage{
			Channel:  label,
			Data:     msg.Data,
			IsString: msg.IsString,
		})
	})

	dc.OnClose(func() {
		s.mu.Lock()
		delete(s.dataChannels, label)
		s.mu.Unlock()
	})
}

// createDataChannels 服务端主动创建数据通道（在 publisher PC 上）
func (s *Session) createDataChannels() error {
	// 可靠通道（有序）
	reliable, err := s.pub.pc.CreateDataChannel(s.config.DataChannelReliableLabel, &webrtc.DataChannelInit{
		Ordered: boolPtr(true),
	})
	if err != nil {
		return fmt.Errorf("create reliable DC: %w", err)
	}
	s.setupDataChannel(reliable)

	// 不可靠通道（低延迟，允许丢包）
	lossy, err := s.pub.pc.CreateDataChannel(s.config.DataChannelLossyLabel, &webrtc.DataChannelInit{
		Ordered:        boolPtr(false),
		MaxRetransmits: uint16Ptr(0),
	})
	if err != nil {
		return fmt.Errorf("create lossy DC: %w", err)
	}
	s.setupDataChannel(lossy)

	return nil
}

// --- Close ---

func (s *Session) Close() error {
	if s.closed.Swap(true) {
		return nil
	}

	s.mu.Lock()
	for _, dc := range s.dataChannels {
		_ = dc.Close()
	}
	s.dataChannels = make(map[string]*webrtc.DataChannel)
	s.mu.Unlock()

	s.pub.close()
	s.sub.close()

	_ = s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventHangup,
		Protocol:  common.ProtocolWebRTC,
		SessionID: s.id,
		Timestamp: time.Now(),
	})
	return nil
}

// --- 辅助 ---

func boolPtr(b bool) *bool         { return &b }
func uint16Ptr(v uint16) *uint16   { return &v }
