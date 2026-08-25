package webrtc

import (
	"fmt"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/pion/interceptor/pkg/stats"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// subscriber 管理订阅者 PeerConnection（向客户端发送媒体）
type subscriber struct {
	pc          *webrtc.PeerConnection
	statsGetter stats.Getter
	rtcp        *rtcpHandler
	log         *zap.Logger

	mu           sync.RWMutex
	localTracks  map[common.TrackID]*trackLocal
	trackCounter uint64

	// 重协商
	negotiationPending bool
	remoteAnswerPending bool

	// 回调
	onOfferNeeded  func(offer webrtc.SessionDescription)
	onError        func(err error)
	onStateChange  func(state webrtc.PeerConnectionState)
}

func newSubscriber(cfg Config, log *zap.Logger) (*subscriber, error) {
	me, err := createMediaEngine(cfg.Subscriber)
	if err != nil {
		return nil, fmt.Errorf("subscriber media engine: %w", err)
	}

	ir, statsInterceptor, err := createInterceptorRegistry(cfg, me, false)
	if err != nil {
		return nil, fmt.Errorf("subscriber interceptor: %w", err)
	}

	se := createSettingEngine(cfg)

	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(me),
		webrtc.WithSettingEngine(se),
		webrtc.WithInterceptorRegistry(ir),
	)

	pc, err := api.NewPeerConnection(webrtc.Configuration{
		ICEServers: cfg.ICEServers,
	})
	if err != nil {
		return nil, fmt.Errorf("subscriber PC: %w", err)
	}

	s := &subscriber{
		pc:          pc,
		statsGetter: statsInterceptor,
		rtcp:        newRTCPHandler(cfg.PLIInterval),
		log:         log.With(zap.String("pc", "subscriber")),
		localTracks: make(map[common.TrackID]*trackLocal),
	}

	s.setupCallbacks()
	return s, nil
}

func (s *subscriber) setupCallbacks() {
	// 需要重协商时创建 offer
	s.pc.OnNegotiationNeeded(func() {
		s.mu.Lock()
		if s.remoteAnswerPending {
			s.negotiationPending = true
			s.mu.Unlock()
			return
		}
		s.remoteAnswerPending = true
		s.mu.Unlock()

		offer, err := s.pc.CreateOffer(nil)
		if err != nil {
			s.log.Error("subscriber create offer", zap.Error(err))
			return
		}

		if err := s.pc.SetLocalDescription(offer); err != nil {
			s.log.Error("subscriber set local description", zap.Error(err))
			return
		}

		if s.onOfferNeeded != nil {
			s.onOfferNeeded(offer)
		}
	})

	// RTCP 处理由 interceptor 负责（NACK generator、SR/RR、PLI、TWCC）
	// pion v4 不提供 OnRTCPReceived 回调，统计通过 stats interceptor 获取

	// 连接状态
	s.pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		s.log.Info("subscriber connection state", zap.String("state", state.String()))
		if s.onStateChange != nil {
			s.onStateChange(state)
		}
	})

	// ICE 连接状态
	s.pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		s.log.Info("subscriber ICE state", zap.String("state", state.String()))
	})
}

// addTrack 添加发送轨道
func (s *subscriber) addTrack(cfg common.TrackConfig) (common.TrackID, error) {
	s.mu.Lock()
	s.trackCounter++
	id := common.TrackID(fmt.Sprintf("sub-%d", s.trackCounter))
	s.mu.Unlock()

	track, err := newTrackLocal(id, cfg)
	if err != nil {
		return "", err
	}

	sender, err := s.pc.AddTrack(track.track)
	if err != nil {
		return "", fmt.Errorf("subscriber add track: %w", err)
	}
	track.sender = sender

	s.mu.Lock()
	s.localTracks[id] = track
	s.mu.Unlock()

	return id, nil
}

// removeTrack 移除发送轨道
func (s *subscriber) removeTrack(id common.TrackID) error {
	s.mu.Lock()
	track, ok := s.localTracks[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("track not found: %s", id)
	}
	delete(s.localTracks, id)
	s.mu.Unlock()

	if track.sender != nil {
		if err := s.pc.RemoveTrack(track.sender); err != nil {
			return fmt.Errorf("remove track: %w", err)
		}
	}
	return nil
}

// sendMediaFrame 发送媒体帧
func (s *subscriber) sendMediaFrame(trackID common.TrackID, frame common.MediaFrame) error {
	s.mu.RLock()
	track, ok := s.localTracks[trackID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("track not found: %s", trackID)
	}

	// 更新统计
	track.mu.Lock()
	track.stats.PacketsSent++
	track.stats.BytesSent += uint64(len(frame.Payload))
	track.mu.Unlock()

	return track.writeRTP(frame)
}

// handleAnswer 处理客户端的 SDP answer
func (s *subscriber) handleAnswer(answer webrtc.SessionDescription) error {
	if err := s.pc.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("subscriber set remote description: %w", err)
	}

	s.mu.Lock()
	s.remoteAnswerPending = false
	if s.negotiationPending {
		s.negotiationPending = false
		s.mu.Unlock()
		// 触发 OnNegotiationNeeded
		// pion 会在状态变化时自动触发
		return nil
	}
	s.mu.Unlock()
	return nil
}

// addICECandidate 添加 ICE 候选
func (s *subscriber) addICECandidate(candidate webrtc.ICECandidateInit) error {
	return s.pc.AddICECandidate(candidate)
}

// tracks 返回所有本地轨道信息
func (s *subscriber) tracks() []common.TrackInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]common.TrackInfo, 0, len(s.localTracks))
	for _, tr := range s.localTracks {
		result = append(result, common.TrackInfo{
			ID:        tr.id,
			Kind:      tr.kind,
			Direction: common.TrackSend,
			Codec:     tr.codec,
			StreamID:  tr.streamID,
		})
	}
	return result
}

// trackStats 获取轨道统计
func (s *subscriber) trackStats() map[common.TrackID]common.TrackStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[common.TrackID]common.TrackStats, len(s.localTracks))
	for id, tr := range s.localTracks {
		result[id] = tr.getStats()
	}
	return result
}

// close 关闭
func (s *subscriber) close() {
	_ = s.pc.Close()
}
