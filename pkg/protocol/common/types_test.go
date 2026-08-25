package common

import (
	"testing"
	"time"
)

// ─── TrackKind ────────────────────────────────────────────────────────────────

func TestTrackKindString(t *testing.T) {
	tests := []struct {
		kind TrackKind
		want string
	}{
		{TrackAudio, "audio"},
		{TrackVideo, "video"},
		{TrackKind(0xFF), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("TrackKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestFrameTypeFromKind(t *testing.T) {
	if got := FrameTypeFromKind(TrackAudio); got != FrameAudio {
		t.Errorf("FrameTypeFromKind(TrackAudio) = %v, want %v", got, FrameAudio)
	}
	if got := FrameTypeFromKind(TrackVideo); got != FrameVideo {
		t.Errorf("FrameTypeFromKind(TrackVideo) = %v, want %v", got, FrameVideo)
	}
	// default: any non-video kind should map to FrameAudio
	if got := FrameTypeFromKind(TrackKind(0)); got != FrameAudio {
		t.Errorf("FrameTypeFromKind(0) = %v, want %v", got, FrameAudio)
	}
}

// ─── CodecType ────────────────────────────────────────────────────────────────

func TestCodecTypeString(t *testing.T) {
	tests := []struct {
		codec CodecType
		want  string
	}{
		{CodecOpus, "opus"},
		{CodecPCMU, "pcmu"},
		{CodecPCMA, "pcma"},
		{CodecPCM16, "pcm16"},
		{CodecH264, "h264"},
		{CodecVP8, "vp8"},
		{CodecVP9, "vp9"},
		{CodecAV1, "av1"},
		{CodecRTX, "rtx"},
		{CodecType(0xFF), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.codec.String(); got != tt.want {
			t.Errorf("CodecType(%d).String() = %q, want %q", tt.codec, got, tt.want)
		}
	}
}

func TestCodecFromString(t *testing.T) {
	tests := []struct {
		in      string
		want    CodecType
		wantErr bool
	}{
		{"opus", CodecOpus, false},
		{"pcmu", CodecPCMU, false},
		{"pcma", CodecPCMA, false},
		{"pcm16", CodecPCM16, false},
		{"pcm", CodecPCM16, false},
		{"h264", CodecH264, false},
		{"vp8", CodecVP8, false},
		{"vp9", CodecVP9, false},
		{"av1", CodecAV1, false},
		{"rtx", CodecRTX, false},
		{"bogus", 0, true},
	}
	for _, tt := range tests {
		got, err := CodecFromString(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("CodecFromString(%q) expected error, got nil", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("CodecFromString(%q) unexpected error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("CodecFromString(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// ─── ProtocolType ─────────────────────────────────────────────────────────────
// ProtocolType is a string-typed alias; verify its underlying string values.

func TestProtocolTypeString(t *testing.T) {
	tests := []struct {
		pt   ProtocolType
		want string
	}{
		{ProtocolSIP, "sip"},
		{ProtocolWebRTC, "webrtc"},
		{ProtocolWS, "ws"},
		{ProtocolRTMP, "rtmp"},
		{ProtocolWHIP, "whip"},
		{ProtocolWHEP, "whep"},
		{ProtocolMQTT, "mqtt"},
	}
	for _, tt := range tests {
		if got := string(tt.pt); got != tt.want {
			t.Errorf("ProtocolType(%v) = %q, want %q", tt.pt, got, tt.want)
		}
	}
}

// ─── TrackDirection ───────────────────────────────────────────────────────────
// TrackDirection is a uint8 type without a String() method; verify constants.

func TestTrackDirectionValues(t *testing.T) {
	if TrackRecv != 0x01 {
		t.Errorf("TrackRecv = %d, want 1", TrackRecv)
	}
	if TrackSend != 0x02 {
		t.Errorf("TrackSend = %d, want 2", TrackSend)
	}
	if TrackRecv == TrackSend {
		t.Error("TrackRecv and TrackSend must differ")
	}
}

// ─── MediaFrame ───────────────────────────────────────────────────────────────

func TestMediaFrameFields(t *testing.T) {
	f := MediaFrame{
		Type:       FrameAudio,
		Codec:      CodecOpus,
		Payload:    []byte{0x01, 0x02, 0x03},
		Timestamp:  12345,
		Sequence:   6789,
		SampleRate: 48000,
		Channels:   2,
		SSRC:       0xABCDEF12,
		Marker:     true,
		RID:        "q",
	}
	if f.Type != FrameAudio {
		t.Errorf("MediaFrame.Type = %v, want %v", f.Type, FrameAudio)
	}
	if f.Codec != CodecOpus {
		t.Errorf("MediaFrame.Codec = %v, want %v", f.Codec, CodecOpus)
	}
	if len(f.Payload) != 3 || f.Payload[0] != 0x01 {
		t.Errorf("MediaFrame.Payload = %v, want [1 2 3]", f.Payload)
	}
	if f.Timestamp != 12345 {
		t.Errorf("MediaFrame.Timestamp = %d, want 12345", f.Timestamp)
	}
	if f.Sequence != 6789 {
		t.Errorf("MediaFrame.Sequence = %d, want 6789", f.Sequence)
	}
	if f.SampleRate != 48000 {
		t.Errorf("MediaFrame.SampleRate = %d, want 48000", f.SampleRate)
	}
	if f.Channels != 2 {
		t.Errorf("MediaFrame.Channels = %d, want 2", f.Channels)
	}
	if f.SSRC != 0xABCDEF12 {
		t.Errorf("MediaFrame.SSRC = %d, want %d", f.SSRC, 0xABCDEF12)
	}
	if !f.Marker {
		t.Error("MediaFrame.Marker = false, want true")
	}
	if f.RID != "q" {
		t.Errorf("MediaFrame.RID = %q, want %q", f.RID, "q")
	}
}

// ─── TrackInfo / TrackConfig ──────────────────────────────────────────────────

func TestTrackInfoConstruction(t *testing.T) {
	layers := []SimulcastLayer{
		{RID: "q", Width: 320, Height: 180, FPS: 15},
		{RID: "h", Width: 640, Height: 360, FPS: 30},
		{RID: "f", Width: 1280, Height: 720, FPS: 30},
	}
	ti := TrackInfo{
		ID:         TrackID("track-1"),
		Kind:       TrackVideo,
		Direction:  TrackSend,
		Codec:      CodecVP8,
		SampleRate: 90000,
		Channels:   0,
		SSRC:       1234,
		Layers:     layers,
		StreamID:   "stream-1",
	}
	if ti.ID != TrackID("track-1") {
		t.Errorf("TrackInfo.ID = %q, want %q", ti.ID, "track-1")
	}
	if ti.Kind != TrackVideo {
		t.Errorf("TrackInfo.Kind = %v, want %v", ti.Kind, TrackVideo)
	}
	if ti.Direction != TrackSend {
		t.Errorf("TrackInfo.Direction = %v, want %v", ti.Direction, TrackSend)
	}
	if ti.Codec != CodecVP8 {
		t.Errorf("TrackInfo.Codec = %v, want %v", ti.Codec, CodecVP8)
	}
	if ti.SampleRate != 90000 {
		t.Errorf("TrackInfo.SampleRate = %d, want 90000", ti.SampleRate)
	}
	if ti.SSRC != 1234 {
		t.Errorf("TrackInfo.SSRC = %d, want 1234", ti.SSRC)
	}
	if len(ti.Layers) != 3 {
		t.Fatalf("TrackInfo.Layers len = %d, want 3", len(ti.Layers))
	}
	if ti.Layers[1].Width != 640 || ti.Layers[1].Height != 360 {
		t.Errorf("TrackInfo.Layers[1] = %+v, want Width=640 Height=360", ti.Layers[1])
	}
	if ti.StreamID != "stream-1" {
		t.Errorf("TrackInfo.StreamID = %q, want %q", ti.StreamID, "stream-1")
	}
}

func TestTrackConfigConstruction(t *testing.T) {
	tc := TrackConfig{
		Kind:       TrackAudio,
		Codec:      CodecOpus,
		SampleRate: 48000,
		Channels:   2,
		StreamID:   "stream-a",
		Label:      "mic",
	}
	if tc.Kind != TrackAudio {
		t.Errorf("TrackConfig.Kind = %v, want %v", tc.Kind, TrackAudio)
	}
	if tc.Codec != CodecOpus {
		t.Errorf("TrackConfig.Codec = %v, want %v", tc.Codec, CodecOpus)
	}
	if tc.SampleRate != 48000 {
		t.Errorf("TrackConfig.SampleRate = %d, want 48000", tc.SampleRate)
	}
	if tc.Channels != 2 {
		t.Errorf("TrackConfig.Channels = %d, want 2", tc.Channels)
	}
	if tc.StreamID != "stream-a" {
		t.Errorf("TrackConfig.StreamID = %q, want %q", tc.StreamID, "stream-a")
	}
	if tc.Label != "mic" {
		t.Errorf("TrackConfig.Label = %q, want %q", tc.Label, "mic")
	}
}

// ─── ProtocolEvent / ProtocolCommand ──────────────────────────────────────────

func TestProtocolEventConstruction(t *testing.T) {
	ts := time.Now()
	track := &TrackInfo{
		ID:        TrackID("t-1"),
		Kind:      TrackAudio,
		Direction: TrackRecv,
		Codec:     CodecOpus,
	}
	ev := ProtocolEvent{
		Type:      EventIncomingCall,
		Protocol:  ProtocolSIP,
		SessionID: "call-123",
		From:      "alice@example.com",
		To:        "bob@example.com",
		Track:     track,
		Err:       nil,
		Timestamp: ts,
	}
	if ev.Type != EventIncomingCall {
		t.Errorf("ProtocolEvent.Type = %v, want %v", ev.Type, EventIncomingCall)
	}
	if ev.Protocol != ProtocolSIP {
		t.Errorf("ProtocolEvent.Protocol = %v, want %v", ev.Protocol, ProtocolSIP)
	}
	if ev.SessionID != "call-123" {
		t.Errorf("ProtocolEvent.SessionID = %q, want %q", ev.SessionID, "call-123")
	}
	if ev.From != "alice@example.com" {
		t.Errorf("ProtocolEvent.From = %q, want %q", ev.From, "alice@example.com")
	}
	if ev.To != "bob@example.com" {
		t.Errorf("ProtocolEvent.To = %q, want %q", ev.To, "bob@example.com")
	}
	if ev.Track == nil || ev.Track.ID != TrackID("t-1") {
		t.Errorf("ProtocolEvent.Track = %+v, want ID=t-1", ev.Track)
	}
	if !ev.Timestamp.Equal(ts) {
		t.Errorf("ProtocolEvent.Timestamp = %v, want %v", ev.Timestamp, ts)
	}
}

func TestProtocolCommandConstruction(t *testing.T) {
	cmd := ProtocolCommand{
		Type:      CmdHangup,
		SessionID: "call-456",
		Reason:    "user busy",
		Target:    "",
	}
	if cmd.Type != CmdHangup {
		t.Errorf("ProtocolCommand.Type = %v, want %v", cmd.Type, CmdHangup)
	}
	if cmd.SessionID != "call-456" {
		t.Errorf("ProtocolCommand.SessionID = %q, want %q", cmd.SessionID, "call-456")
	}
	if cmd.Reason != "user busy" {
		t.Errorf("ProtocolCommand.Reason = %q, want %q", cmd.Reason, "user busy")
	}
}

// ─── SimulcastLayer ───────────────────────────────────────────────────────────

func TestSimulcastLayer(t *testing.T) {
	l := SimulcastLayer{
		RID:    "f",
		Width:  1920,
		Height: 1080,
		FPS:    60,
	}
	if l.RID != "f" {
		t.Errorf("SimulcastLayer.RID = %q, want %q", l.RID, "f")
	}
	if l.Width != 1920 {
		t.Errorf("SimulcastLayer.Width = %d, want 1920", l.Width)
	}
	if l.Height != 1080 {
		t.Errorf("SimulcastLayer.Height = %d, want 1080", l.Height)
	}
	if l.FPS != 60 {
		t.Errorf("SimulcastLayer.FPS = %d, want 60", l.FPS)
	}
}
