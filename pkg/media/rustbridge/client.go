// Package rustbridge provides a gRPC client to the Rust media node.
//
// The Go protocol layer (signaling/connections) delegates media processing
// (decode/mix/relay/record) to the Rust media node via this client.
package rustbridge

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	mediav1 "github.com/LingByte/LingVoice/proto/media/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client is the gRPC client to the Rust media node.
type Client struct {
	conn   *grpc.ClientConn
	stub   mediav1.MediaNodeClient
	log    *zap.Logger
	mu     sync.Mutex
	streams map[string]*sessionStreams // sessionID → active streams
}

type sessionStreams struct {
	// per-track push streams（音频+视频各自一个 gRPC client stream）
	pushRtp map[string]mediav1.MediaNode_PushRtpClient // trackID → stream
	// per-track pull streams（音频+视频各自一个 gRPC server stream）
	pullRtp map[string]mediav1.MediaNode_PullRtpClient // trackID → stream
	events  mediav1.MediaNode_EventsClient
}

// NewClient creates a new Rust media node client.
func NewClient(addr string, log *zap.Logger) (*Client, error) {
	if log == nil {
		log = zap.NewNop()
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("connect rust media node %s: %w", addr, err)
	}
	return &Client{
		conn:    conn,
		stub:    mediav1.NewMediaNodeClient(conn),
		log:     log.With(zap.String("component", "rustbridge")),
		streams: make(map[string]*sessionStreams),
	}, nil
}

// Close closes the gRPC connection.
func (c *Client) Close() error {
	c.mu.Lock()
	for sid, ss := range c.streams {
		for _, stream := range ss.pushRtp {
			_ = stream.CloseSend()
		}
		for _, stream := range ss.pullRtp {
			_ = stream.CloseSend()
		}
		if ss.events != nil {
			_ = ss.events.CloseSend()
		}
		delete(c.streams, sid)
	}
	c.mu.Unlock()
	return c.conn.Close()
}

// HealthCheck checks if the Rust media node is alive.
func (c *Client) HealthCheck(ctx context.Context) (*mediav1.HealthCheckResponse, error) {
	return c.stub.HealthCheck(ctx, &mediav1.HealthCheckRequest{})
}

// CreateSession creates a media session on the Rust node.
func (c *Client) CreateSession(ctx context.Context, sessionID, roomID, tenantID string) error {
	_, err := c.stub.CreateSession(ctx, &mediav1.CreateSessionRequest{
		SessionId: sessionID,
		RoomId:    roomID,
		TenantId:  tenantID,
	})
	if err != nil {
		return fmt.Errorf("create session %s: %w", sessionID, err)
	}
	c.log.Info("session created on rust node", zap.String("session", sessionID))
	return nil
}

// DestroySession destroys a media session.
func (c *Client) DestroySession(ctx context.Context, sessionID string) error {
	c.mu.Lock()
	if ss, ok := c.streams[sessionID]; ok {
		for _, stream := range ss.pushRtp {
			_ = stream.CloseSend()
		}
		for _, stream := range ss.pullRtp {
			_ = stream.CloseSend()
		}
		if ss.events != nil {
			_ = ss.events.CloseSend()
		}
		delete(c.streams, sessionID)
	}
	c.mu.Unlock()

	_, err := c.stub.DestroySession(ctx, &mediav1.DestroySessionRequest{
		SessionId: sessionID,
	})
	return err
}

// AddEndpoint adds a media endpoint to a session.
func (c *Client) AddEndpoint(ctx context.Context, sessionID, endpointID string, epType mediav1.EndpointType, dir mediav1.Direction, codecs []common.TrackInfo) error {
	pbCodecs := make([]*mediav1.CodecInfo, 0, len(codecs))
	for _, t := range codecs {
		pbCodecs = append(pbCodecs, &mediav1.CodecInfo{
			Codec:     t.Codec.String(),
			ClockRate: t.SampleRate,
			Channels:  uint32(t.Channels),
		})
	}

	_, err := c.stub.AddEndpoint(ctx, &mediav1.AddEndpointRequest{
		SessionId:  sessionID,
		EndpointId: endpointID,
		Type:       epType,
		Direction:  dir,
		Codecs:     pbCodecs,
	})
	return err
}

// RemoveEndpoint removes a media endpoint.
func (c *Client) RemoveEndpoint(ctx context.Context, sessionID, endpointID string) error {
	_, err := c.stub.RemoveEndpoint(ctx, &mediav1.RemoveEndpointRequest{
		SessionId:  sessionID,
		EndpointId: endpointID,
	})
	return err
}

// AddTrack registers a track on the Rust node.
func (c *Client) AddTrack(ctx context.Context, sessionID, endpointID string, track common.TrackInfo) error {
	_, err := c.stub.AddTrack(ctx, &mediav1.AddTrackRequest{
		SessionId:  sessionID,
		EndpointId: endpointID,
		Track: trackToProto(track),
	})
	return err
}

// RemoveTrack removes a track from the Rust node.
func (c *Client) RemoveTrack(ctx context.Context, sessionID string, trackID common.TrackID) error {
	_, err := c.stub.RemoveTrack(ctx, &mediav1.RemoveTrackRequest{
		SessionId: sessionID,
		TrackId:   string(trackID),
	})
	return err
}

// StartPushRtp opens a client-streaming PushRtp RPC for the session+track.
// Call PushRtpPacket to send RTP packets; ClosePushRtp to finish.
// 支持 per-track push（音频和视频各自独立的 gRPC stream）。
func (c *Client) StartPushRtp(ctx context.Context, sessionID string, trackID common.TrackID) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	ss := c.getOrCreateStreams(sessionID)
	tid := string(trackID)
	if _, ok := ss.pushRtp[tid]; ok {
		return fmt.Errorf("push rtp stream already open for %s/%s", sessionID, trackID)
	}

	stream, err := c.stub.PushRtp(ctx)
	if err != nil {
		return fmt.Errorf("open push rtp stream: %w", err)
	}
	ss.pushRtp[tid] = stream

	c.log.Info("push rtp stream opened",
		zap.String("session", sessionID),
		zap.String("track", string(trackID)))
	return nil
}

// PushRtpPacket sends a single RTP packet to the Rust node.
func (c *Client) PushRtpPacket(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	c.mu.Lock()
	ss, ok := c.streams[sessionID]
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("no streams for session %s", sessionID)
	}

	tid := string(trackID)
	stream, ok := ss.pushRtp[tid]
	if !ok {
		return fmt.Errorf("no push rtp stream for session %s track %s", sessionID, trackID)
	}

	pkt := &mediav1.PushRtpRequest{
		SessionId: sessionID,
		TrackId:   string(trackID),
		Packet: &mediav1.RtpPacket{
			Ssrc:           frame.SSRC,
			SequenceNumber: uint32(frame.Sequence),
			Timestamp:      frame.Timestamp,
			Marker:         frame.Marker,
			Payload:        frame.Payload,
			Rid:            frame.RID,
			ClockRate:      frame.SampleRate,
		},
	}
	return stream.Send(pkt)
}

// ClosePushRtp closes all push streams for a session and gets the final response.
func (c *Client) ClosePushRtp(sessionID string) error {
	c.mu.Lock()
	ss, ok := c.streams[sessionID]
	if !ok || len(ss.pushRtp) == 0 {
		c.mu.Unlock()
		return nil
	}
	streams := ss.pushRtp
	ss.pushRtp = make(map[string]mediav1.MediaNode_PushRtpClient)
	c.mu.Unlock()

	var firstErr error
	for tid, stream := range streams {
		if _, err := stream.CloseAndRecv(); err != nil && firstErr == nil {
			firstErr = err
		}
		_ = tid
	}
	return firstErr
}

// ClosePushRtpTrack closes a specific track's push stream.
func (c *Client) ClosePushRtpTrack(sessionID string, trackID common.TrackID) error {
	c.mu.Lock()
	ss, ok := c.streams[sessionID]
	if !ok {
		c.mu.Unlock()
		return nil
	}
	tid := string(trackID)
	stream, ok := ss.pushRtp[tid]
	if !ok {
		c.mu.Unlock()
		return nil
	}
	delete(ss.pushRtp, tid)
	c.mu.Unlock()

	_, err := stream.CloseAndRecv()
	return err
}

// StartPullRtp opens a server-streaming PullRtp RPC.
// Received RTP packets are delivered to the onPacket callback.
// kind and codec are used to correctly label the MediaFrame.
func (c *Client) StartPullRtp(ctx context.Context, sessionID string, trackID common.TrackID, kind common.TrackKind, codec common.CodecType, onPacket func(common.MediaFrame) error) error {
	c.mu.Lock()
	ss := c.getOrCreateStreams(sessionID)
	c.mu.Unlock()

	stream, err := c.stub.PullRtp(ctx, &mediav1.PullRtpRequest{
		SessionId: sessionID,
		TrackId:   string(trackID),
	})
	if err != nil {
		return fmt.Errorf("open pull rtp stream: %w", err)
	}

	c.mu.Lock()
	ss.pullRtp[string(trackID)] = stream
	c.mu.Unlock()

	// Read loop
	go func() {
		for {
			pkt, err := stream.Recv()
			if err != nil {
				if err != io.EOF {
					c.log.Error("pull rtp stream error",
						zap.String("session", sessionID),
						zap.String("track", string(trackID)),
						zap.Error(err))
				}
				return
			}
			frame := common.MediaFrame{
				Type:       common.FrameTypeFromKind(kind),
				Codec:      codec,
				Payload:    pkt.Payload,
				Sequence:   uint16(pkt.SequenceNumber),
				Timestamp:  pkt.Timestamp,
				SSRC:       pkt.Ssrc,
				Marker:     pkt.Marker,
				RID:        pkt.Rid,
				SampleRate: pkt.ClockRate,
			}
			if err := onPacket(frame); err != nil {
				c.log.Error("onPacket callback error", zap.Error(err))
				return
			}
		}
	}()

	return nil
}

// StartEvents subscribes to media events from the Rust node.
func (c *Client) StartEvents(ctx context.Context, sessionID string, onEvent func(*mediav1.MediaEvent)) error {
	stream, err := c.stub.Events(ctx, &mediav1.EventsRequest{
		SessionId: sessionID,
	})
	if err != nil {
		return fmt.Errorf("open events stream: %w", err)
	}

	c.mu.Lock()
	ss := c.getOrCreateStreams(sessionID)
	ss.events = stream
	c.mu.Unlock()

	go func() {
		for {
			event, err := stream.Recv()
			if err != nil {
				if err != io.EOF {
					c.log.Error("events stream error", zap.Error(err))
				}
				return
			}
			onEvent(event)
		}
	}()

	return nil
}

// BridgeSessions bridges two sessions (relay or transcode).
func (c *Client) BridgeSessions(ctx context.Context, sessionA, sessionB string, forceTranscode bool) (*mediav1.BridgeSessionsResponse, error) {
	return c.stub.BridgeSessions(ctx, &mediav1.BridgeSessionsRequest{
		SessionAId:    sessionA,
		SessionBId:    sessionB,
		ForceTranscode: forceTranscode,
	})
}

// StartRecording starts recording a session.
func (c *Client) StartRecording(ctx context.Context, sessionID, format, path string, channels uint8) (string, error) {
	resp, err := c.stub.StartRecording(ctx, &mediav1.StartRecordingRequest{
		SessionId: sessionID,
		Format:    format,
		Path:      path,
		Channels:  uint32(channels),
	})
	if err != nil {
		return "", err
	}
	return resp.RecordingId, nil
}

// StopRecording stops recording.
func (c *Client) StopRecording(ctx context.Context, sessionID, recordingID string) (*mediav1.StopRecordingResponse, error) {
	return c.stub.StopRecording(ctx, &mediav1.StopRecordingRequest{
		SessionId:    sessionID,
		RecordingId:  recordingID,
	})
}

// GetStats retrieves statistics for a session.
func (c *Client) GetStats(ctx context.Context, sessionID string) (*mediav1.GetStatsResponse, error) {
	return c.stub.GetStats(ctx, &mediav1.GetStatsRequest{
		SessionId: sessionID,
	})
}

// --- helpers ---

func (c *Client) getOrCreateStreams(sessionID string) *sessionStreams {
	ss, ok := c.streams[sessionID]
	if !ok {
		ss = &sessionStreams{
			pushRtp: make(map[string]mediav1.MediaNode_PushRtpClient),
			pullRtp: make(map[string]mediav1.MediaNode_PullRtpClient),
		}
		c.streams[sessionID] = ss
	}
	return ss
}

func trackToProto(t common.TrackInfo) *mediav1.TrackInfo {
	pb := &mediav1.TrackInfo{
		TrackId:    string(t.ID),
		Kind:       t.Kind.String(),
		Direction:  directionString(t.Direction),
		Codec:      t.Codec.String(),
		SampleRate: t.SampleRate,
		Channels:   uint32(t.Channels),
		Ssrc:       t.SSRC,
		StreamId:   t.StreamID,
	}
	// 从 simulcast layers 取第一个 RID（如果有）
	if len(t.Layers) > 0 {
		pb.Rid = t.Layers[0].RID
	}
	return pb
}

func directionString(d common.TrackDirection) string {
	switch d {
	case common.TrackRecv:
		return "recv"
	case common.TrackSend:
		return "send"
	default:
		return "unknown"
	}
}

// WaitForReady waits for the Rust media node to be ready, up to timeout.
func (c *Client) WaitForReady(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, err := c.HealthCheck(ctx)
	return err
}
