package webrtc

import (
	"context"
	"errors"
	"fmt"

	"github.com/LingByte/LingVoice/proto/protocol/v1"
	pionwebrtc "github.com/pion/webrtc/v4"
)

// Signaling 通过 gRPC 协议层指令服务与 Rust 媒体面交互，
// 负责 WebRTC 的 SDP offer/answer 协商与 ICE candidate 下发。
//
// Go 侧不做 PeerConnection 的 SRTP/DTLS/ICE 实时处理，只负责信令协调；
// Rust 侧持有 PeerConnection，接收 SetRemoteDescription / AddIceCandidate / Close 指令。
type Signaling struct {
	client protocolv1.ProtocolCommandClient
}

// NewSignaling 创建 WebRTC 信令客户端
func NewSignaling(client protocolv1.ProtocolCommandClient) *Signaling {
	return &Signaling{client: client}
}

// CreatePeerConnection 请求 Rust 为指定会话创建 PeerConnection
func (s *Signaling) CreatePeerConnection(ctx context.Context, sessionID string, iceServers []string, enableSimulcast bool) (string, error) {
	req := &protocolv1.CreatePeerConnectionRequest{
		SessionId:       sessionID,
		IceServers:      iceServers,
		EnableSimulcast: enableSimulcast,
	}
	resp, err := s.client.CreatePeerConnection(ctx, req)
	if err != nil {
		return "", fmt.Errorf("create peer connection: %w", err)
	}
	return resp.GetPcId(), nil
}

// SetRemoteDescription 向 Rust 下发远端 SDP（offer 或 answer）
func (s *Signaling) SetRemoteDescription(ctx context.Context, pcID, sdpType, sdp string) error {
	req := &protocolv1.SetRemoteDescriptionRequest{
		PcId: pcID,
		Sdp: &protocolv1.SdpDescription{
			Type: sdpType,
			Sdp:  sdp,
		},
	}
	ack, err := s.client.SetRemoteDescription(ctx, req)
	if err != nil {
		return fmt.Errorf("set remote description: %w", err)
	}
	if !ack.GetOk() {
		return errors.New(ack.GetError())
	}
	return nil
}

// AddIceCandidate 向 Rust 下发本地 ICE candidate
func (s *Signaling) AddIceCandidate(ctx context.Context, pcID string, c pionwebrtc.ICECandidateInit) error {
	cand := &protocolv1.IceCandidate{
		Candidate: c.Candidate,
	}
	if c.SDPMid != nil {
		cand.SdpMid = *c.SDPMid
	}
	if c.SDPMLineIndex != nil {
		cand.SdpMLineIndex = int32(*c.SDPMLineIndex)
	}
	if c.UsernameFragment != nil {
		cand.UsernameFragment = *c.UsernameFragment
	}

	req := &protocolv1.AddIceCandidateRequest{
		PcId:      pcID,
		Candidate: cand,
	}
	ack, err := s.client.AddIceCandidate(ctx, req)
	if err != nil {
		return fmt.Errorf("add ice candidate: %w", err)
	}
	if !ack.GetOk() {
		return errors.New(ack.GetError())
	}
	return nil
}

// ClosePeerConnection 关闭 Rust 侧的 PeerConnection
func (s *Signaling) ClosePeerConnection(ctx context.Context, pcID string) error {
	req := &protocolv1.ClosePeerConnectionRequest{PcId: pcID}
	ack, err := s.client.ClosePeerConnection(ctx, req)
	if err != nil {
		return fmt.Errorf("close peer connection: %w", err)
	}
	if !ack.GetOk() {
		return errors.New(ack.GetError())
	}
	return nil
}
