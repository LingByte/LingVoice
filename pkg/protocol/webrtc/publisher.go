package webrtc

import (
	"fmt"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/pion/interceptor/pkg/stats"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// publisher 管理发布者 PeerConnection（接收客户端媒体）
type publisher struct {
	pc          *webrtc.PeerConnection
	statsGetter stats.Getter
	rtcp        *rtcpHandler
	log         *zap.Logger

	mu          sync.RWMutex
	remoteTracks map[common.TrackID]*trackRemote

	// 回调
	onTrack     func(track common.TrackInfo)
	onFrame     func(trackID common.TrackID, frame common.MediaFrame)
	onError     func(err error)
	onStateChange func(state webrtc.PeerConnectionState)
}

func newPublisher(cfg Config, log *zap.Logger) (*publisher, error) {
	me, err := createMediaEngine(cfg.Publisher)
	if err != nil {
		return nil, fmt.Errorf("publisher media engine: %w", err)
	}

	ir, statsInterceptor, err := createInterceptorRegistry(cfg, me, true)
	if err != nil {
		return nil, fmt.Errorf("publisher interceptor: %w", err)
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
		return nil, fmt.Errorf("publisher PC: %w", err)
	}

	p := &publisher{
		pc:          pc,
		statsGetter: statsInterceptor,
		rtcp:        newRTCPHandler(cfg.PLIInterval),
		log:         log.With(zap.String("pc", "publisher")),
		remoteTracks: make(map[common.TrackID]*trackRemote),
	}

	p.setupCallbacks()
	return p, nil
}

func (p *publisher) setupCallbacks() {
	// 收到远端 Track
	p.pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		p.log.Info("publisher track received",
			zap.String("trackID", track.ID()),
			zap.String("kind", track.Kind().String()),
			zap.String("codec", track.Codec().MimeType),
			zap.String("rid", track.RID()),
			zap.Uint32("ssrc", uint32(track.SSRC())),
		)

		// 跳过 RTX 重传轨道（由拦截器自动处理）
		if track.Codec().MimeType == webrtc.MimeTypeRTX {
			return
		}

		tr := newTrackRemote(track, receiver, p.onFrame)
		trackID := tr.id

		p.mu.Lock()
		// 如果同 ID 的 track 已存在（simulcast 不同层），更新 layers
		if existing, ok := p.remoteTracks[trackID]; ok {
			// simulcast：同 trackID 不同 RID
			if tr.rid != "" {
				existing.mu.Lock()
				// 保持原 track，新层由 pion buffer 处理
				existing.mu.Unlock()
			}
			p.mu.Unlock()
			// 仍然读取 RTP
			go tr.readLoop()
			return
		}
		p.remoteTracks[trackID] = tr
		p.mu.Unlock()

		// 通知上层轨道就绪
		if p.onTrack != nil {
			p.onTrack(tr.info())
		}

		// 读取 RTP 包
		tr.readLoop()

		// track 结束，通知上层
		p.mu.Lock()
		delete(p.remoteTracks, trackID)
		p.mu.Unlock()
	})

	// RTCP 处理由 interceptor 负责（NACK responder、SR/RR、TWCC）
	// pion v4 不提供 OnRTCPReceived 回调，统计通过 stats interceptor 获取

	// 连接状态
	p.pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		p.log.Info("publisher connection state", zap.String("state", state.String()))
		if p.onStateChange != nil {
			p.onStateChange(state)
		}
	})

	// ICE 连接状态
	p.pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		p.log.Info("publisher ICE state", zap.String("state", state.String()))
	})
}

// handleOffer 处理客户端的 SDP offer
func (p *publisher) handleOffer(offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("publisher set remote description: %w", err)
	}

	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("publisher create answer: %w", err)
	}

	if err := p.pc.SetLocalDescription(answer); err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("publisher set local description: %w", err)
	}

	return answer, nil
}

// addICECandidate 添加 ICE 候选
func (p *publisher) addICECandidate(candidate webrtc.ICECandidateInit) error {
	return p.pc.AddICECandidate(candidate)
}

// tracks 返回所有远端轨道信息
func (p *publisher) tracks() []common.TrackInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]common.TrackInfo, 0, len(p.remoteTracks))
	for _, tr := range p.remoteTracks {
		result = append(result, tr.info())
	}
	return result
}

// trackStats 获取轨道统计
func (p *publisher) trackStats() map[common.TrackID]common.TrackStats {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make(map[common.TrackID]common.TrackStats, len(p.remoteTracks))
	for id, tr := range p.remoteTracks {
		result[id] = tr.getStats()
	}
	return result
}

// close 关闭
func (p *publisher) close() {
	_ = p.pc.Close()
}
