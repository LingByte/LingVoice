package webrtc

import (
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/pion/interceptor/pkg/stats"
)

// SessionStats 会话级 QoS 指标汇总
type SessionStats struct {
	// 连接状态
	PublisherState  string
	SubscriberState string
	ICEState        string

	// 采集时间
	Timestamp time.Time

	// 轨道级统计
	Tracks map[common.TrackID]TrackStats

	// 会话时长
	ConnectedAt time.Time
	Duration    time.Duration
}

// QoSReporter 可观测性指标采集器
type QoSReporter struct {
	mu           sync.Mutex
	statsGetter  stats.Getter
	connectedAt  time.Time
	publisher    *publisher
	subscriber   *subscriber
}

func newQoSReporter(pub *publisher, sub *subscriber) *QoSReporter {
	return &QoSReporter{
		publisher:  pub,
		subscriber: sub,
	}
}

// Collect 采集当前会话的完整 QoS 指标
func (q *QoSReporter) Collect() SessionStats {
	q.mu.Lock()
	defer q.mu.Unlock()

	result := SessionStats{
		Timestamp: time.Now(),
		Tracks:    make(map[common.TrackID]TrackStats),
	}

	if q.publisher != nil {
		for id, s := range q.publisher.trackStats() {
			result.Tracks[id] = enrichTrackStats(s, q.publisher.statsGetter, id)
		}
	}

	if q.subscriber != nil {
		for id, s := range q.subscriber.trackStats() {
			result.Tracks[id] = enrichTrackStats(s, q.subscriber.statsGetter, id)
		}
	}

	if !q.connectedAt.IsZero() {
		result.ConnectedAt = q.connectedAt
		result.Duration = time.Since(q.connectedAt)
	}

	return result
}

// SetConnected 标记连接建立时间
func (q *QoSReporter) SetConnected() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.connectedAt.IsZero() {
		q.connectedAt = time.Now()
	}
}

// enrichTrackStats 用 pion stats interceptor 补充 RTT、抖动等指标
func enrichTrackStats(base common.TrackStats, getter stats.Getter, trackID common.TrackID) TrackStats {
	result := TrackStats{
		PacketsSent:     base.PacketsSent,
		PacketsReceived: base.PacketsReceived,
		PacketsLost:     base.PacketsLost,
		BytesSent:       base.BytesSent,
		BytesReceived:   base.BytesReceived,
	}

	if getter == nil {
		return result
	}

	// pion stats 按 SSRC 查询，这里遍历所有 SSRC
	// 实际使用时需要 trackID → SSRC 的映射
	// 简化：尝试用 base 里的 SSRC（如果有）
	// TODO: 建立 trackID → SSRC 映射后精确查询

	return result
}

// TrackStats 扩展的轨道统计（包含 RTT、抖动、比特率）
type TrackStats struct {
	PacketsSent     uint64
	PacketsReceived uint64
	PacketsLost     uint64
	BytesSent       uint64
	BytesReceived   uint64
	RTT             time.Duration
	Jitter          time.Duration
	Bitrate         uint64 // bps
	FIRCount        uint32
	PLICount        uint32
	NACKCount       uint32
}

// LossRate 计算丢包率（0-1）
func (s TrackStats) LossRate() float64 {
	total := s.PacketsSent + s.PacketsReceived
	if total == 0 {
		return 0
	}
	return float64(s.PacketsLost) / float64(total)
}

// CollectFromPionStats 从 pion stats.Stats 提取指标
func CollectFromPionStats(s *stats.Stats) TrackStats {
	if s == nil {
		return TrackStats{}
	}
	return TrackStats{
		PacketsReceived: s.InboundRTPStreamStats.PacketsReceived,
		PacketsLost:     uint64(s.InboundRTPStreamStats.PacketsLost),
		BytesReceived:   s.InboundRTPStreamStats.BytesReceived,
		PacketsSent:     s.OutboundRTPStreamStats.PacketsSent,
		BytesSent:       s.OutboundRTPStreamStats.BytesSent,
		RTT:             s.RemoteInboundRTPStreamStats.RoundTripTime,
		Jitter:          time.Duration(s.InboundRTPStreamStats.Jitter * float64(time.Second)),
		FIRCount:        s.InboundRTPStreamStats.FIRCount,
		PLICount:        s.InboundRTPStreamStats.PLICount,
		NACKCount:       s.InboundRTPStreamStats.NACKCount,
	}
}
