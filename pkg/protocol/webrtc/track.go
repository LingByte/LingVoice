package webrtc

import (
	"fmt"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// trackLocal 封装本地发送轨道
type trackLocal struct {
	id        common.TrackID
	track     *webrtc.TrackLocalStaticRTP
	sender    *webrtc.RTPSender
	kind      common.TrackKind
	codec     common.CodecType
	streamID  string
	mu        sync.Mutex
	stats     common.TrackStats
}

func newTrackLocal(id common.TrackID, cfg common.TrackConfig) (*trackLocal, error) {
	mimeType := mimeTypeFromCodecType(cfg.Codec)
	if mimeType == "" {
		return nil, fmt.Errorf("unsupported codec: %s", cfg.Codec)
	}

	capability := webrtc.RTPCodecCapability{
		MimeType:    mimeType,
		ClockRate:   cfg.SampleRate,
		Channels:    cfg.Channels,
	}
	if cfg.Kind == common.TrackVideo {
		// 所有标准 WebRTC 视频编解码器（VP8/VP9/H264/AV1）均使用 90kHz。
		// 上游 caller 应已在 TrackConfig.SampleRate 设为 90000，
		// 此处作为防御性兜底，避免遗漏导致 SDP 协商失败。
		if capability.ClockRate == 0 {
			capability.ClockRate = 90000
		}
		capability.RTCPFeedback = videoRTCPFeedback
	} else {
		capability.RTCPFeedback = audioRTCPFeedback
		// Opus 需要 SDPFmtpLine 与 MediaEngine 注册的 codec 匹配，
		// 否则 SetRemoteDescription(answer) 会报 "codec is not supported by remote"
		if mimeType == webrtc.MimeTypeOpus {
			capability.SDPFmtpLine = "minptime=10;useinbandfec=1"
		}
	}

	track, err := webrtc.NewTrackLocalStaticRTP(capability, cfg.Label, cfg.StreamID)
	if err != nil {
		return nil, fmt.Errorf("create track local: %w", err)
	}

	return &trackLocal{
		id:       id,
		track:    track,
		kind:     cfg.Kind,
		codec:    cfg.Codec,
		streamID: cfg.StreamID,
	}, nil
}

// writeRTP 向轨道写入 RTP 包
func (t *trackLocal) writeRTP(frame common.MediaFrame) error {
	pkt := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    0, // pion 会自动设置
			SequenceNumber: frame.Sequence,
			Timestamp:      frame.Timestamp,
			SSRC:           frame.SSRC,
			Marker:         frame.Marker,
		},
		Payload: frame.Payload,
	}
	return t.track.WriteRTP(pkt)
}

// trackRemote 封装远端接收轨道
type trackRemote struct {
	id        common.TrackID
	track     *webrtc.TrackRemote
	receiver  *webrtc.RTPReceiver
	kind      common.TrackKind
	codec     common.CodecType
	streamID  string
	rid       string // simulcast RID
	ssrc      uint32
	mu        sync.Mutex
	stats     common.TrackStats
	onFrame   func(common.TrackID, common.MediaFrame)
}

func newTrackRemote(
	track *webrtc.TrackRemote,
	receiver *webrtc.RTPReceiver,
	onFrame func(common.TrackID, common.MediaFrame),
) *trackRemote {
	codec := codecTypeFromMimeType(track.Codec().MimeType)
	kind := common.TrackAudio
	if track.Kind() == webrtc.RTPCodecTypeVideo {
		kind = common.TrackVideo
	}

	return &trackRemote{
		id:       common.TrackID(track.ID()),
		track:    track,
		receiver: receiver,
		kind:     kind,
		codec:    codec,
		streamID: track.StreamID(),
		rid:      track.RID(),
		ssrc:     uint32(track.SSRC()),
		onFrame:  onFrame,
	}
}

// readLoop 读取 RTP 包循环
func (t *trackRemote) readLoop() {
	for {
		pkt, _, err := t.track.ReadRTP()
		if err != nil {
			return
		}

		frame := common.MediaFrame{
			Type:       frameTypeFromKind(t.kind),
			Codec:      t.codec,
			Payload:    pkt.Payload,
			Timestamp:  pkt.Timestamp,
			Sequence:   pkt.SequenceNumber,
			SampleRate: t.track.Codec().ClockRate,
			Channels:   uint16(t.track.Codec().Channels),
			SSRC:       pkt.SSRC,
			Marker:     pkt.Marker,
			RID:        t.rid,
		}

		// 更新统计
		t.mu.Lock()
		t.stats.PacketsReceived++
		t.stats.BytesReceived += uint64(len(pkt.Payload))
		t.mu.Unlock()

		if t.onFrame != nil {
			t.onFrame(t.id, frame)
		}
	}
}

// info 返回轨道信息
func (t *trackRemote) info() common.TrackInfo {
	return common.TrackInfo{
		ID:         t.id,
		Kind:       t.kind,
		Direction:  common.TrackRecv,
		Codec:      t.codec,
		SampleRate: t.track.Codec().ClockRate,
		Channels:   uint16(t.track.Codec().Channels),
		SSRC:       t.ssrc,
		StreamID:   t.streamID,
	}
}

// getStats 获取统计
func (t *trackRemote) getStats() common.TrackStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}

// getStats 获取本地轨道统计
func (t *trackLocal) getStats() common.TrackStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}

// frameTypeFromKind
func frameTypeFromKind(kind common.TrackKind) common.FrameType {
	if kind == common.TrackVideo {
		return common.FrameVideo
	}
	return common.FrameAudio
}
