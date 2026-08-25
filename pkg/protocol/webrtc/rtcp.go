package webrtc

import (
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// rtcpHandler 处理 RTCP 包
type rtcpHandler struct {
	mu           sync.Mutex
	lastPLI      time.Time
	pliInterval  time.Duration
	onStats      func(trackID common.TrackID, stats common.TrackStats)
}

func newRTCPHandler(pliInterval time.Duration) *rtcpHandler {
	return &rtcpHandler{
		pliInterval: pliInterval,
	}
}

// handlePublisherRTCP 处理发布者端收到的 RTCP（来自客户端的反馈）
func (h *rtcpHandler) handlePublisherRTCP(trackID common.TrackID, pkts []rtcp.Packet) {
	for _, pkt := range pkts {
		switch p := pkt.(type) {
		case *rtcp.PictureLossIndication:
			// PLI：客户端请求关键帧，转发给上层处理
			// 实际 SFU 会在这里触发编码器生成关键帧
			_ = p
		case *rtcp.FullIntraRequest:
			// FIR：同 PLI
			_ = p
		case *rtcp.TransportLayerNack:
			// NACK：客户端请求重传，pion NACK responder 拦截器会自动处理
			_ = p
		case *rtcp.ReceiverReport:
			// RR：客户端接收报告，更新统计
			for _, r := range p.Reports {
				h.mu.Lock()
				_ = r // 可提取丢包率、抖动、RTT
				h.mu.Unlock()
			}
		case *rtcp.ReceiverEstimatedMaximumBitrate:
			// REMB：带宽估计
			_ = p
		case *rtcp.TransportLayerCC:
			// TWCC：传输层拥塞控制反馈
			_ = p
		}
	}
}

// handleSubscriberRTCP 处理订阅者端收到的 RTCP（来自客户端的接收反馈）
func (h *rtcpHandler) handleSubscriberRTCP(trackID common.TrackID, pkts []rtcp.Packet) {
	for _, pkt := range pkts {
		switch p := pkt.(type) {
		case *rtcp.PictureLossIndication:
			// 客户端请求关键帧 — 需要向上游请求
			_ = p
		case *rtcp.FullIntraRequest:
			_ = p
		case *rtcp.TransportLayerNack:
			// 客户端请求重传 — NACK responder 会自动处理
			_ = p
		case *rtcp.ReceiverReport:
			// 客户端接收报告
			for _, r := range p.Reports {
				h.mu.Lock()
				// 更新 RTT 和丢包统计
				_ = r
				h.mu.Unlock()
			}
		}
	}
}

// shouldSendPLI 检查是否应该发送 PLI（限流）
func (h *rtcpHandler) shouldSendPLI() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if now.Sub(h.lastPLI) < h.pliInterval {
		return false
	}
	h.lastPLI = now
	return true
}

// sendPLI 通过 PeerConnection 发送 PLI
func sendPLI(pc *webrtc.PeerConnection, ssrc uint32) error {
	pkt := &rtcp.PictureLossIndication{
		SenderSSRC: 0,
		MediaSSRC:  ssrc,
	}
	return pc.WriteRTCP([]rtcp.Packet{pkt})
}

// sendFIR 通过 PeerConnection 发送 FIR
func sendFIR(pc *webrtc.PeerConnection, ssrc uint32) error {
	pkt := &rtcp.FullIntraRequest{
		SenderSSRC: 0,
		MediaSSRC:  ssrc,
		FIR: []rtcp.FIREntry{
			{SSRC: ssrc, SequenceNumber: 1},
		},
	}
	return pc.WriteRTCP([]rtcp.Packet{pkt})
}
