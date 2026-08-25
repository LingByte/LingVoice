package rustbridge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	mediav1 "github.com/LingByte/LingVoice/proto/media/v1"
)

// dummyAddr is an address that is very unlikely to have a Rust media node
// listening on it during tests.
const dummyAddr = "localhost:9999"

// newTestClient creates a Client connected to dummyAddr. It does not require a
// real gRPC server to be running (grpc.NewClient uses lazy connection).
func newTestClient(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(dummyAddr, nil)
	if err != nil {
		t.Fatalf("NewClient(%q) failed: %v", dummyAddr, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// --- NewClient / Close -------------------------------------------------------

// TestNewClientReturnsNonNil verifies that NewClient returns a non-nil Client
// even when the address is unreachable. grpc.NewClient uses lazy connection so
// it should not fail immediately.
func TestNewClientReturnsNonNil(t *testing.T) {
	c, err := NewClient(dummyAddr, nil)
	if err != nil {
		t.Fatalf("NewClient(%q) returned unexpected error: %v", dummyAddr, err)
	}
	if c == nil {
		t.Fatal("NewClient returned nil client")
	}
	if c.streams == nil {
		t.Fatal("NewClient did not initialize streams map")
	}
	if c.stub == nil {
		t.Fatal("NewClient did not initialize gRPC stub")
	}
	if c.conn == nil {
		t.Fatal("NewClient did not initialize gRPC conn")
	}
	_ = c.Close()
}

// TestNewClientWithNilLogger verifies that NewClient works with a nil logger
// (it should substitute a no-op logger).
func TestNewClientWithNilLogger(t *testing.T) {
	c, err := NewClient(dummyAddr, nil)
	if err != nil {
		t.Fatalf("NewClient with nil logger failed: %v", err)
	}
	if c == nil || c.log == nil {
		t.Fatal("NewClient with nil logger returned nil client or nil logger")
	}
	_ = c.Close()
}

// TestClientClose verifies that Close() does not panic on a freshly created
// client with no active streams.
func TestClientClose(t *testing.T) {
	c, err := NewClient(dummyAddr, nil)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

// TestClientCloseIdempotent verifies that calling Close() multiple times does
// not panic.
func TestClientCloseIdempotent(t *testing.T) {
	c, err := NewClient(dummyAddr, nil)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	_ = c.Close()
	// Second close: conn.Close() may error but should not panic.
	_ = c.Close()
}

// --- PushRtpPacket (local state) --------------------------------------------

// TestPushRtpPacketNoSession verifies that PushRtpPacket on a non-existent
// session returns an error mentioning "no streams for session".
func TestPushRtpPacketNoSession(t *testing.T) {
	c := newTestClient(t)
	err := c.PushRtpPacket("nonexistent-session", "track-1", common.MediaFrame{
		Payload: []byte("test"),
	})
	if err == nil {
		t.Fatal("PushRtpPacket on non-existent session should return error")
	}
	if !strings.Contains(err.Error(), "no streams for session") {
		t.Errorf("PushRtpPacket error should mention 'no streams for session', got: %v", err)
	}
}

// TestPushRtpPacketNoStream verifies that after getOrCreateStreams has created
// a sessionStreams entry (via StartPullRtp which calls getOrCreateStreams even
// if the gRPC call fails later), PushRtpPacket reports "no push rtp stream"
// rather than "no streams for session".
//
// Note: StartPullRtp calls getOrCreateStreams *before* the gRPC PullRtp call,
// so even when the gRPC call fails, the sessionStreams entry is created.
func TestPushRtpPacketNoStream(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// StartPullRtp will fail on the gRPC call, but getOrCreateStreams runs
	// first, creating the sessionStreams entry.
	_ = c.StartPullRtp(ctx, "sess-1", "track-a", common.TrackAudio, common.CodecOpus,
		func(common.MediaFrame) error { return nil })

	// Now the sessionStreams entry exists but has no push stream.
	err := c.PushRtpPacket("sess-1", "track-a", common.MediaFrame{
		Payload: []byte("test"),
	})
	if err == nil {
		t.Fatal("PushRtpPacket should return error when no push stream is open")
	}
	if !strings.Contains(err.Error(), "no push rtp stream") {
		t.Errorf("PushRtpPacket error should mention 'no push rtp stream', got: %v", err)
	}
}

// --- ClosePushRtp / ClosePushRtpTrack (local state, no-op) -------------------

// TestClosePushRtpNoSession verifies that ClosePushRtp on a non-existent
// session is a no-op returning nil.
func TestClosePushRtpNoSession(t *testing.T) {
	c := newTestClient(t)
	if err := c.ClosePushRtp("nonexistent-session"); err != nil {
		t.Errorf("ClosePushRtp on non-existent session should return nil, got: %v", err)
	}
}

// TestClosePushRtpTrackNoSession verifies that ClosePushRtpTrack on a
// non-existent session is a no-op returning nil.
func TestClosePushRtpTrackNoSession(t *testing.T) {
	c := newTestClient(t)
	if err := c.ClosePushRtpTrack("nonexistent-session", "track-1"); err != nil {
		t.Errorf("ClosePushRtpTrack on non-existent session should return nil, got: %v", err)
	}
}

// TestClosePushRtpTrackNoTrack verifies that ClosePushRtpTrack on a session
// that exists but has no matching track is a no-op returning nil.
func TestClosePushRtpTrackNoTrack(t *testing.T) {
	c := newTestClient(t)
	// Manually create a sessionStreams entry via the unexported helper.
	c.mu.Lock()
	c.getOrCreateStreams("sess-2")
	c.mu.Unlock()

	if err := c.ClosePushRtpTrack("sess-2", "missing-track"); err != nil {
		t.Errorf("ClosePushRtpTrack on missing track should return nil, got: %v", err)
	}
}

// --- gRPC-dependent methods (should fail gracefully without connection) ------

// TestStartPushRtpNoConnection verifies that StartPushRtp fails with a gRPC
// error when no server is reachable.
func TestStartPushRtpNoConnection(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.StartPushRtp(ctx, "sess-3", "track-1")
	if err == nil {
		t.Fatal("StartPushRtp with unreachable server should return error")
	}
	if !strings.Contains(err.Error(), "open push rtp stream") {
		t.Errorf("StartPushRtp error should mention 'open push rtp stream', got: %v", err)
	}
}

// TestStartPushRtpCreatesSessionStreams verifies that even when StartPushRtp
// fails on the gRPC call, the sessionStreams entry is created by
// getOrCreateStreams (which runs before the gRPC call). This tests the internal
// state management indirectly.
func TestStartPushRtpCreatesSessionStreams(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = c.StartPushRtp(ctx, "sess-4", "track-1")

	c.mu.Lock()
	ss, ok := c.streams["sess-4"]
	c.mu.Unlock()
	if !ok {
		t.Fatal("StartPushRtp should have created sessionStreams entry via getOrCreateStreams")
	}
	if ss == nil {
		t.Fatal("sessionStreams entry should not be nil")
	}
	if ss.pushRtp == nil {
		t.Fatal("pushRtp map should be initialized")
	}
	if len(ss.pushRtp) != 0 {
		t.Errorf("pushRtp map should be empty after failed StartPushRtp, got %d entries", len(ss.pushRtp))
	}
}

// TestRemoveTrackNoConnection verifies that RemoveTrack fails with a gRPC
// error when no server is reachable.
func TestRemoveTrackNoConnection(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.RemoveTrack(ctx, "sess-5", "track-1")
	if err == nil {
		t.Fatal("RemoveTrack with unreachable server should return error")
	}
}

// TestHealthCheckNoConnection verifies that HealthCheck fails with a gRPC
// error when no server is reachable.
func TestHealthCheckNoConnection(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := c.HealthCheck(ctx)
	if err == nil {
		t.Fatal("HealthCheck with unreachable server should return error")
	}
	if resp != nil {
		t.Errorf("HealthCheck response should be nil on error, got %v", resp)
	}
}

// TestCreateSessionNoConnection verifies that CreateSession fails with a gRPC
// error when no server is reachable.
func TestCreateSessionNoConnection(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.CreateSession(ctx, "sess-6", "room-1", "tenant-1")
	if err == nil {
		t.Fatal("CreateSession with unreachable server should return error")
	}
	if !strings.Contains(err.Error(), "create session") {
		t.Errorf("CreateSession error should mention 'create session', got: %v", err)
	}
}

// TestDestroySessionNoConnection verifies that DestroySession cleans up local
// state and then fails on the gRPC call.
func TestDestroySessionNoConnection(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Create a local sessionStreams entry first.
	c.mu.Lock()
	c.getOrCreateStreams("sess-7")
	c.mu.Unlock()

	err := c.DestroySession(ctx, "sess-7")
	if err == nil {
		t.Fatal("DestroySession with unreachable server should return error")
	}

	// Local state should be cleaned up even though gRPC failed.
	c.mu.Lock()
	_, ok := c.streams["sess-7"]
	c.mu.Unlock()
	if ok {
		t.Error("DestroySession should have removed local sessionStreams entry")
	}
}

// TestDestroySessionNoLocalStreams verifies that DestroySession on a session
// with no local streams still attempts the gRPC call (and fails).
func TestDestroySessionNoLocalStreams(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.DestroySession(ctx, "no-local-streams")
	if err == nil {
		t.Fatal("DestroySession with unreachable server should return error")
	}
}

// TestAddEndpointNoConnection verifies that AddEndpoint fails with a gRPC
// error when no server is reachable.
func TestAddEndpointNoConnection(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	codecs := []common.TrackInfo{
		{
			ID:         "track-1",
			Kind:       common.TrackAudio,
			Direction:  common.TrackSend,
			Codec:      common.CodecOpus,
			SampleRate: 48000,
			Channels:   2,
		},
	}
	err := c.AddEndpoint(ctx, "sess-8", "ep-1",
		mediav1.EndpointType_ENDPOINT_WEBRTC,
		mediav1.Direction_DIRECTION_SENDRECV,
		codecs)
	if err == nil {
		t.Fatal("AddEndpoint with unreachable server should return error")
	}
}

// TestRemoveEndpointNoConnection verifies that RemoveEndpoint fails with a
// gRPC error when no server is reachable.
func TestRemoveEndpointNoConnection(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.RemoveEndpoint(ctx, "sess-9", "ep-1")
	if err == nil {
		t.Fatal("RemoveEndpoint with unreachable server should return error")
	}
}

// TestAddTrackNoConnection verifies that AddTrack fails with a gRPC error when
// no server is reachable.
func TestAddTrackNoConnection(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.AddTrack(ctx, "sess-10", "ep-1", common.TrackInfo{
		ID:        "track-1",
		Kind:      common.TrackVideo,
		Direction: common.TrackSend,
		Codec:     common.CodecH264,
	})
	if err == nil {
		t.Fatal("AddTrack with unreachable server should return error")
	}
}

// TestStartPullRtpNoConnection verifies that StartPullRtp fails with a gRPC
// error when no server is reachable.
func TestStartPullRtpNoConnection(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.StartPullRtp(ctx, "sess-11", "track-1",
		common.TrackAudio, common.CodecOpus,
		func(common.MediaFrame) error { return nil })
	if err == nil {
		t.Fatal("StartPullRtp with unreachable server should return error")
	}
	if !strings.Contains(err.Error(), "open pull rtp stream") {
		t.Errorf("StartPullRtp error should mention 'open pull rtp stream', got: %v", err)
	}
}

// TestStartEventsNoConnection verifies that StartEvents fails with a gRPC
// error when no server is reachable.
func TestStartEventsNoConnection(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.StartEvents(ctx, "sess-12", func(*mediav1.MediaEvent) {})
	if err == nil {
		t.Fatal("StartEvents with unreachable server should return error")
	}
	if !strings.Contains(err.Error(), "open events stream") {
		t.Errorf("StartEvents error should mention 'open events stream', got: %v", err)
	}
}

// TestBridgeSessionsNoConnection verifies that BridgeSessions fails with a
// gRPC error when no server is reachable.
func TestBridgeSessionsNoConnection(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := c.BridgeSessions(ctx, "sess-a", "sess-b", false)
	if err == nil {
		t.Fatal("BridgeSessions with unreachable server should return error")
	}
	if resp != nil {
		t.Errorf("BridgeSessions response should be nil on error, got %v", resp)
	}
}

// TestStartRecordingNoConnection verifies that StartRecording fails with a
// gRPC error when no server is reachable.
func TestStartRecordingNoConnection(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	id, err := c.StartRecording(ctx, "sess-13", "wav", "/tmp/test.wav", 1)
	if err == nil {
		t.Fatal("StartRecording with unreachable server should return error")
	}
	if id != "" {
		t.Errorf("StartRecording ID should be empty on error, got %q", id)
	}
}

// TestStopRecordingNoConnection verifies that StopRecording fails with a gRPC
// error when no server is reachable.
func TestStopRecordingNoConnection(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := c.StopRecording(ctx, "sess-14", "rec-1")
	if err == nil {
		t.Fatal("StopRecording with unreachable server should return error")
	}
	if resp != nil {
		t.Errorf("StopRecording response should be nil on error, got %v", resp)
	}
}

// TestGetStatsNoConnection verifies that GetStats fails with a gRPC error when
// no server is reachable.
func TestGetStatsNoConnection(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := c.GetStats(ctx, "sess-15")
	if err == nil {
		t.Fatal("GetStats with unreachable server should return error")
	}
	if resp != nil {
		t.Errorf("GetStats response should be nil on error, got %v", resp)
	}
}

// TestWaitForReadyNoConnection verifies that WaitForReady fails when the
// server is unreachable within the timeout.
func TestWaitForReadyNoConnection(t *testing.T) {
	c := newTestClient(t)
	err := c.WaitForReady(500 * time.Millisecond)
	if err == nil {
		t.Fatal("WaitForReady with unreachable server should return error")
	}
}

// --- Internal helpers --------------------------------------------------------

// TestGetOrCreateStreams verifies the internal getOrCreateStreams helper
// creates a properly initialized sessionStreams entry.
func TestGetOrCreateStreams(t *testing.T) {
	c := newTestClient(t)

	c.mu.Lock()
	ss := c.getOrCreateStreams("sess-internal")
	c.mu.Unlock()

	if ss == nil {
		t.Fatal("getOrCreateStreams returned nil")
	}
	if ss.pushRtp == nil {
		t.Fatal("getOrCreateStreams did not initialize pushRtp map")
	}
	if ss.pullRtp == nil {
		t.Fatal("getOrCreateStreams did not initialize pullRtp map")
	}

	// Calling again should return the same entry.
	c.mu.Lock()
	ss2 := c.getOrCreateStreams("sess-internal")
	c.mu.Unlock()
	if ss != ss2 {
		t.Fatal("getOrCreateStreams should return the same entry for the same session")
	}

	// A different session should create a new entry.
	c.mu.Lock()
	ss3 := c.getOrCreateStreams("sess-other")
	c.mu.Unlock()
	if ss == ss3 {
		t.Fatal("getOrCreateStreams should create a new entry for a different session")
	}
}

// TestTrackToProto verifies the internal trackToProto helper converts a
// TrackInfo to the protobuf TrackInfo correctly.
func TestTrackToProto(t *testing.T) {
	track := common.TrackInfo{
		ID:         "track-1",
		Kind:       common.TrackAudio,
		Direction:  common.TrackSend,
		Codec:      common.CodecOpus,
		SampleRate: 48000,
		Channels:   2,
		SSRC:       12345,
		StreamID:   "stream-1",
	}
	pb := trackToProto(track)
	if pb == nil {
		t.Fatal("trackToProto returned nil")
	}
	if pb.TrackId != "track-1" {
		t.Errorf("TrackId = %q, want %q", pb.TrackId, "track-1")
	}
	if pb.Kind != "audio" {
		t.Errorf("Kind = %q, want %q", pb.Kind, "audio")
	}
	if pb.Direction != "send" {
		t.Errorf("Direction = %q, want %q", pb.Direction, "send")
	}
	if pb.Codec != "opus" {
		t.Errorf("Codec = %q, want %q", pb.Codec, "opus")
	}
	if pb.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want 48000", pb.SampleRate)
	}
	if pb.Channels != 2 {
		t.Errorf("Channels = %d, want 2", pb.Channels)
	}
	if pb.Ssrc != 12345 {
		t.Errorf("Ssrc = %d, want 12345", pb.Ssrc)
	}
	if pb.StreamId != "stream-1" {
		t.Errorf("StreamId = %q, want %q", pb.StreamId, "stream-1")
	}
}

// TestTrackToProtoWithSimulcast verifies that trackToProto picks up the RID
// from the first simulcast layer.
func TestTrackToProtoWithSimulcast(t *testing.T) {
	track := common.TrackInfo{
		ID:        "track-v",
		Kind:      common.TrackVideo,
		Direction: common.TrackSend,
		Codec:     common.CodecVP8,
		Layers: []common.SimulcastLayer{
			{RID: "q", Width: 320, Height: 180},
			{RID: "h", Width: 640, Height: 360},
		},
	}
	pb := trackToProto(track)
	if pb.Rid != "q" {
		t.Errorf("Rid = %q, want %q", pb.Rid, "q")
	}
}

// TestDirectionString verifies the internal directionString helper.
func TestDirectionString(t *testing.T) {
	tests := []struct {
		dir  common.TrackDirection
		want string
	}{
		{common.TrackRecv, "recv"},
		{common.TrackSend, "send"},
		{common.TrackDirection(99), "unknown"},
	}
	for _, tt := range tests {
		got := directionString(tt.dir)
		if got != tt.want {
			t.Errorf("directionString(%d) = %q, want %q", tt.dir, got, tt.want)
		}
	}
}
