package rtsp

import (
	"bufio"
	"bytes"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
)

// --- mock helpers ---

// mockConn 用 bytes.Buffer 模拟 net.Conn，方便测试 Session
type mockConn struct {
	r *bytes.Buffer
	w *bytes.Buffer
}

func (m *mockConn) Read(b []byte) (int, error)         { return m.r.Read(b) }
func (m *mockConn) Write(b []byte) (int, error)        { return m.w.Write(b) }
func (m *mockConn) Close() error                       { return nil }
func (m *mockConn) LocalAddr() net.Addr                { return nil }
func (m *mockConn) RemoteAddr() net.Addr               { return nil }
func (m *mockConn) SetDeadline(t time.Time) error    { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

// newTestSession 构造一个用于测试的 Session（不依赖 Server）
func newTestSession() *Session {
	conn := &mockConn{r: &bytes.Buffer{}, w: &bytes.Buffer{}}
	s := &Session{
		id:        "test-session-id",
		conn:      conn,
		transport: NewTransport(conn),
		tracks:    make(map[common.TrackID]*SessionTrack),
	}
	s.touch()
	return s
}

// --- SDP 生成测试 ---

func TestBuildSDP_H264Opus(t *testing.T) {
	s := newTestSession()
	s.AddTrack(&SessionTrack{
		TrackID:   "video",
		Kind:      common.TrackVideo,
		Codec:     common.CodecH264,
		ClockRate: 90000,
		Channel:   0,
	})
	s.AddTrack(&SessionTrack{
		TrackID:   "audio",
		Kind:      common.TrackAudio,
		Codec:     common.CodecOpus,
		ClockRate: 48000,
		Channels:  2,
		Channel:   2,
	})

	sdp := BuildSDP(s)

	// 基本结构检查
	if !strings.HasPrefix(sdp, "v=0\r\n") {
		t.Errorf("SDP should start with v=0, got: %q", sdp[:10])
	}
	if !strings.Contains(sdp, "s=LingVoice RTSP Stream") {
		t.Errorf("SDP should contain session name")
	}
	// video track（PT 可能是 96 或 97，取决于 map 遍历顺序）
	if !strings.Contains(sdp, "m=video 0 RTP/AVP") {
		t.Errorf("SDP should contain video m= line")
	}
	if !strings.Contains(sdp, "h264/90000") {
		t.Errorf("SDP should contain H.264 rtpmap")
	}
	if !strings.Contains(sdp, "packetization-mode=1") {
		t.Errorf("SDP should contain H.264 fmtp")
	}
	if !strings.Contains(sdp, "a=control:trackID=video") {
		t.Errorf("SDP should contain video control trackID")
	}
	// audio track
	if !strings.Contains(sdp, "m=audio 0 RTP/AVP") {
		t.Errorf("SDP should contain audio m= line")
	}
	if !strings.Contains(sdp, "opus/48000/2") {
		t.Errorf("SDP should contain Opus rtpmap with channels=2, got: %s", sdp)
	}
	if !strings.Contains(sdp, "a=control:trackID=audio") {
		t.Errorf("SDP should contain audio control trackID")
	}
	// recvonly
	if !strings.Contains(sdp, "a=recvonly") {
		t.Errorf("SDP should contain a=recvonly")
	}
}

func TestBuildSDP_EmptySession(t *testing.T) {
	s := newTestSession()
	sdp := BuildSDP(s)
	if !strings.Contains(sdp, "v=0") {
		t.Errorf("empty SDP should still have v=0")
	}
	if strings.Contains(sdp, "m=audio") || strings.Contains(sdp, "m=video") {
		t.Errorf("empty session should not have media lines")
	}
}

func TestBuildSDP_MonoAudio(t *testing.T) {
	s := newTestSession()
	s.AddTrack(&SessionTrack{
		TrackID:   "audio",
		Kind:      common.TrackAudio,
		Codec:     common.CodecOpus,
		ClockRate: 48000,
		Channels:  1,
		Channel:   0,
	})
	sdp := BuildSDP(s)
	// mono 不应带 /channels
	if strings.Contains(sdp, "opus/48000/1") {
		t.Errorf("mono audio should not include /1 channels suffix")
	}
	if !strings.Contains(sdp, "a=rtpmap:96 opus/48000") {
		t.Errorf("should contain opus rtpmap")
	}
}

// --- SDP 解析测试 ---

func TestParseSDP_Basic(t *testing.T) {
	raw := "v=0\r\n" +
		"o=- 0 0 IN IP4 0.0.0.0\r\n" +
		"s=Test Stream\r\n" +
		"m=video 0 RTP/AVP 96\r\n" +
		"a=rtpmap:96 H264/90000\r\n" +
		"a=fmtp:96 packetization-mode=1\r\n" +
		"a=control:trackID=video\r\n" +
		"m=audio 0 RTP/AVP 97\r\n" +
		"a=rtpmap:97 opus/48000/2\r\n" +
		"a=control:trackID=audio\r\n"

	sdp, err := ParseSDP([]byte(raw))
	if err != nil {
		t.Fatalf("ParseSDP error: %v", err)
	}
	if sdp.SessionName != "Test Stream" {
		t.Errorf("SessionName = %q, want %q", sdp.SessionName, "Test Stream")
	}
	if len(sdp.Media) != 2 {
		t.Fatalf("expected 2 media sections, got %d", len(sdp.Media))
	}

	// video
	v := sdp.Media[0]
	if v.Type != "video" {
		t.Errorf("media[0] type = %q, want video", v.Type)
	}
	if v.Proto != "RTP/AVP" {
		t.Errorf("media[0] proto = %q, want RTP/AVP", v.Proto)
	}
	if v.PT != 96 {
		t.Errorf("media[0] PT = %d, want 96", v.PT)
	}
	if v.RTPMap != "96 H264/90000" {
		t.Errorf("media[0] RTPMap = %q, want %q", v.RTPMap, "96 H264/90000")
	}
	if v.Fmtp != "96 packetization-mode=1" {
		t.Errorf("media[0] Fmtp = %q", v.Fmtp)
	}
	if v.Control != "trackID=video" {
		t.Errorf("media[0] Control = %q", v.Control)
	}

	// audio
	a := sdp.Media[1]
	if a.Type != "audio" {
		t.Errorf("media[1] type = %q, want audio", a.Type)
	}
	if a.RTPMap != "97 opus/48000/2" {
		t.Errorf("media[1] RTPMap = %q", a.RTPMap)
	}
	if a.Control != "trackID=audio" {
		t.Errorf("media[1] Control = %q", a.Control)
	}
}

func TestParseSDP_Empty(t *testing.T) {
	sdp, err := ParseSDP([]byte(""))
	if err != nil {
		t.Fatalf("ParseSDP empty error: %v", err)
	}
	if len(sdp.Media) != 0 {
		t.Errorf("empty SDP should have 0 media, got %d", len(sdp.Media))
	}
}

func TestParseSDP_InvalidLines(t *testing.T) {
	// 非法行应被跳过
	raw := "garbage line\r\n" +
		"xinvalid\r\n" +
		"v=0\r\n" +
		"m=audio\r\n" + // parts < 4, 应跳过
		"m=audio 0 RTP/AVP 96\r\n" +
		"a=rtpmap:96 opus/48000\r\n"

	sdp, err := ParseSDP([]byte(raw))
	if err != nil {
		t.Fatalf("ParseSDP error: %v", err)
	}
	if len(sdp.Media) != 1 {
		t.Errorf("expected 1 valid media, got %d", len(sdp.Media))
	}
}

func TestParseSDP_GlobalAttributes(t *testing.T) {
	raw := "v=0\r\n" +
		"a=ice-ufrag:abcd\r\n" +
		"m=audio 0 RTP/AVP 96\r\n" +
		"a=rtpmap:96 opus/48000\r\n"

	sdp, err := ParseSDP([]byte(raw))
	if err != nil {
		t.Fatalf("ParseSDP error: %v", err)
	}
	if len(sdp.Attributes) != 1 {
		t.Errorf("expected 1 global attribute, got %d", len(sdp.Attributes))
	}
	if sdp.Attributes[0] != "ice-ufrag:abcd" {
		t.Errorf("global attr = %q", sdp.Attributes[0])
	}
}

// --- Session 测试 ---

func TestSession_AddTrackAndFind(t *testing.T) {
	s := newTestSession()
	st := &SessionTrack{
		TrackID:   "audio",
		Kind:      common.TrackAudio,
		Codec:     common.CodecOpus,
		ClockRate: 48000,
		Channel:   0,
	}
	s.AddTrack(st)

	found, ok := s.findTrack("audio")
	if !ok {
		t.Fatal("findTrack should find audio track")
	}
	if found.Codec != common.CodecOpus {
		t.Errorf("found codec = %v, want opus", found.Codec)
	}
	if found.ClockRate != 48000 {
		t.Errorf("found clockRate = %d, want 48000", found.ClockRate)
	}
}

func TestSession_FindTrack_NotExist(t *testing.T) {
	s := newTestSession()
	_, ok := s.findTrack("nonexistent")
	if ok {
		t.Error("findTrack should return false for nonexistent track")
	}
}

func TestSession_FirstTrack(t *testing.T) {
	s := newTestSession()
	if s.firstTrack() != nil {
		t.Error("firstTrack should be nil for empty session")
	}
	s.AddTrack(&SessionTrack{TrackID: "video", Kind: common.TrackVideo, Codec: common.CodecH264})
	first := s.firstTrack()
	if first == nil {
		t.Fatal("firstTrack should not be nil after AddTrack")
	}
	if first.TrackID != "video" {
		t.Errorf("firstTrack ID = %q, want video", first.TrackID)
	}
}

func TestSession_TrackByChannel(t *testing.T) {
	s := newTestSession()
	s.AddTrack(&SessionTrack{TrackID: "video", Kind: common.TrackVideo, Codec: common.CodecH264, Channel: 0})
	s.AddTrack(&SessionTrack{TrackID: "audio", Kind: common.TrackAudio, Codec: common.CodecOpus, Channel: 2})

	if st, ok := s.TrackByChannel(0); !ok || st.TrackID != "video" {
		t.Errorf("TrackByChannel(0) = %v %v, want video", st, ok)
	}
	if st, ok := s.TrackByChannel(2); !ok || st.TrackID != "audio" {
		t.Errorf("TrackByChannel(2) = %v %v, want audio", st, ok)
	}
	if _, ok := s.TrackByChannel(99); ok {
		t.Error("TrackByChannel(99) should return false")
	}
}

func TestSession_RTSPSessionID(t *testing.T) {
	s := newTestSession()
	if s.RTSPSessionID() != "" {
		t.Error("initial RTSPSessionID should be empty")
	}
	s.SetRTSPSessionID("abc-123")
	if s.RTSPSessionID() != "abc-123" {
		t.Errorf("RTSPSessionID = %q, want abc-123", s.RTSPSessionID())
	}
}

func TestSession_PlayingState(t *testing.T) {
	s := newTestSession()
	if s.IsPlaying() {
		t.Error("session should not be playing initially")
	}
	s.SetPlaying(true)
	if !s.IsPlaying() {
		t.Error("session should be playing after SetPlaying(true)")
	}
	s.SetPlaying(false)
	if s.IsPlaying() {
		t.Error("session should not be playing after SetPlaying(false)")
	}
}

func TestSession_Tracks(t *testing.T) {
	s := newTestSession()
	s.AddTrack(&SessionTrack{TrackID: "video", Kind: common.TrackVideo, Codec: common.CodecH264, ClockRate: 90000})
	s.AddTrack(&SessionTrack{TrackID: "audio", Kind: common.TrackAudio, Codec: common.CodecOpus, ClockRate: 48000})

	tracks := s.Tracks()
	if len(tracks) != 2 {
		t.Fatalf("Tracks() returned %d, want 2", len(tracks))
	}
	for _, tr := range tracks {
		if tr.Direction != common.TrackRecv {
			t.Errorf("track %s direction = %v, want TrackRecv", tr.ID, tr.Direction)
		}
	}
}

func TestSession_SendMediaFrame_NotSupported(t *testing.T) {
	s := newTestSession()
	err := s.SendMediaFrame("video", common.MediaFrame{})
	if err == nil {
		t.Error("SendMediaFrame should return error in ingest mode")
	}
}

// --- Interleaved RTP frame 编码/解码 ---

func TestInterleaved_WriteAndRead(t *testing.T) {
	buf := &bytes.Buffer{}
	tr := NewTransport(buf)

	payload := []byte{0x80, 0x60, 0x00, 0x01, // RTP header
		0x00, 0x00, 0x00, 0x01,
		0x12, 0x34, 0x56, 0x78,
		0xDE, 0xAD, 0xBE, 0xEF, // payload
	}
	if err := tr.WriteInterleaved(0, payload); err != nil {
		t.Fatalf("WriteInterleaved: %v", err)
	}

	// 用同一个 buffer 读回
	tr2 := NewTransport(buf)
	ch, data, err := tr2.ReadInterleaved()
	if err != nil {
		t.Fatalf("ReadInterleaved: %v", err)
	}
	if ch != 0 {
		t.Errorf("channel = %d, want 0", ch)
	}
	if !bytes.Equal(data, payload) {
		t.Errorf("payload mismatch: got %v, want %v", data, payload)
	}
}

func TestInterleaved_MultipleFrames(t *testing.T) {
	buf := &bytes.Buffer{}
	tr := NewTransport(buf)

	frames := [][]byte{
		[]byte("frame1"),
		[]byte("frame2-data"),
		[]byte("f3"),
	}
	channels := []byte{0, 2, 4}

	for i, f := range frames {
		if err := tr.WriteInterleaved(channels[i], f); err != nil {
			t.Fatalf("WriteInterleaved %d: %v", i, err)
		}
	}

	tr2 := NewTransport(buf)
	for i, f := range frames {
		ch, data, err := tr2.ReadInterleaved()
		if err != nil {
			t.Fatalf("ReadInterleaved %d: %v", i, err)
		}
		if ch != channels[i] {
			t.Errorf("frame %d channel = %d, want %d", i, ch, channels[i])
		}
		if !bytes.Equal(data, f) {
			t.Errorf("frame %d payload mismatch", i)
		}
	}
}

func TestInterleaved_EmptyPayload(t *testing.T) {
	buf := &bytes.Buffer{}
	tr := NewTransport(buf)
	if err := tr.WriteInterleaved(0, nil); err != nil {
		t.Fatalf("WriteInterleaved empty: %v", err)
	}
	tr2 := NewTransport(buf)
	ch, data, err := tr2.ReadInterleaved()
	if err != nil {
		t.Fatalf("ReadInterleaved: %v", err)
	}
	if ch != 0 {
		t.Errorf("channel = %d, want 0", ch)
	}
	if data != nil {
		t.Errorf("expected nil payload for empty frame, got %v", data)
	}
}

func TestInterleaved_NotInterleavedFrame(t *testing.T) {
	// 写入非 '$' 开头的数据
	buf := bytes.NewBufferString("RTSP/1.0 200 OK\r\n")
	tr := NewTransport(buf)
	_, _, err := tr.ReadInterleaved()
	if err != ErrNotInterleaved {
		t.Errorf("expected ErrNotInterleaved, got %v", err)
	}
}

func TestInterleaved_TooLarge(t *testing.T) {
	buf := &bytes.Buffer{}
	tr := NewTransport(buf)
	large := make([]byte, maxInterleavedSize+1)
	err := tr.WriteInterleaved(0, large)
	if err != ErrInterleavedTooLarge {
		t.Errorf("expected ErrInterleavedTooLarge, got %v", err)
	}
}

func TestInterleaved_HeaderFormat(t *testing.T) {
	// 验证帧头格式：$<channel><len big-endian><data>
	buf := &bytes.Buffer{}
	tr := NewTransport(buf)
	payload := []byte{0x01, 0x02, 0x03}
	if err := tr.WriteInterleaved(2, payload); err != nil {
		t.Fatalf("WriteInterleaved: %v", err)
	}
	out := buf.Bytes()
	if len(out) < 4 {
		t.Fatalf("output too short: %d", len(out))
	}
	if out[0] != interleavedMagic {
		t.Errorf("magic = 0x%02x, want 0x%02x", out[0], interleavedMagic)
	}
	if out[1] != 2 {
		t.Errorf("channel = %d, want 2", out[1])
	}
	// big-endian length
	length := uint16(out[2])<<8 | uint16(out[3])
	if int(length) != len(payload) {
		t.Errorf("length = %d, want %d", length, len(payload))
	}
	if !bytes.Equal(out[4:], payload) {
		t.Errorf("payload mismatch")
	}
}

// --- Transport channel 注册 ---

func TestTransport_RegisterChannel(t *testing.T) {
	buf := &bytes.Buffer{}
	tr := NewTransport(buf)
	tr.RegisterChannel(0, "video", common.TrackVideo, common.CodecH264)
	tr.RegisterChannel(2, "audio", common.TrackAudio, common.CodecOpus)

	trackID, kind, codec, ok := tr.ChannelInfo(0)
	if !ok {
		t.Fatal("ChannelInfo(0) should find channel")
	}
	if trackID != "video" || kind != common.TrackVideo || codec != common.CodecH264 {
		t.Errorf("ChannelInfo(0) = %s %v %v, want video h264", trackID, kind, codec)
	}

	_, _, _, ok = tr.ChannelInfo(99)
	if ok {
		t.Error("ChannelInfo(99) should return false")
	}
}

// --- RTSP 请求行解析 ---

func TestParseRequest_Basic(t *testing.T) {
	raw := "DESCRIBE rtsp://localhost/test RTSP/1.0\r\n" +
		"CSeq: 1\r\n" +
		"User-Agent: test\r\n" +
		"\r\n"
	r := bufio.NewReader(strings.NewReader(raw))
	req, err := parseRequest(r)
	if err != nil {
		t.Fatalf("parseRequest: %v", err)
	}
	if req.Method != MethodDescribe {
		t.Errorf("Method = %q, want DESCRIBE", req.Method)
	}
	if req.URI != "rtsp://localhost/test" {
		t.Errorf("URI = %q", req.URI)
	}
	if req.Proto != "RTSP/1.0" {
		t.Errorf("Proto = %q, want RTSP/1.0", req.Proto)
	}
	if req.CSeq != "1" {
		t.Errorf("CSeq = %q, want 1", req.CSeq)
	}
	if req.Headers["user-agent"] != "test" {
		t.Errorf("User-Agent header = %q", req.Headers["user-agent"])
	}
}

func TestParseRequest_WithBody(t *testing.T) {
	body := "v=0\r\nm=audio 0 RTP/AVP 96\r\n"
	raw := "ANNOUNCE rtsp://localhost/test RTSP/1.0\r\n" +
		"CSeq: 2\r\n" +
		"Content-Type: application/sdp\r\n" +
		"Content-Length: " + itoa(len(body)) + "\r\n" +
		"\r\n" + body
	r := bufio.NewReader(strings.NewReader(raw))
	req, err := parseRequest(r)
	if err != nil {
		t.Fatalf("parseRequest: %v", err)
	}
	if req.Method != MethodAnnounce {
		t.Errorf("Method = %q, want ANNOUNCE", req.Method)
	}
	if req.ContentLen != len(body) {
		t.Errorf("ContentLen = %d, want %d", req.ContentLen, len(body))
	}
	if string(req.Body) != body {
		t.Errorf("Body = %q, want %q", string(req.Body), body)
	}
	if req.Headers["content-type"] != "application/sdp" {
		t.Errorf("Content-Type = %q", req.Headers["content-type"])
	}
}

func TestParseRequest_WithSession(t *testing.T) {
	raw := "PLAY rtsp://localhost/test RTSP/1.0\r\n" +
		"CSeq: 3\r\n" +
		"Session: abc-123\r\n" +
		"\r\n"
	r := bufio.NewReader(strings.NewReader(raw))
	req, err := parseRequest(r)
	if err != nil {
		t.Fatalf("parseRequest: %v", err)
	}
	if req.Session != "abc-123" {
		t.Errorf("Session = %q, want abc-123", req.Session)
	}
}

func TestParseRequest_InvalidRequestLine(t *testing.T) {
	raw := "INVALID\r\n\r\n"
	r := bufio.NewReader(strings.NewReader(raw))
	_, err := parseRequest(r)
	if err == nil {
		t.Error("expected error for invalid request line")
	}
}

func TestParseRequest_Empty(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	_, err := parseRequest(r)
	if err == nil {
		t.Error("expected error for empty input")
	}
}

// --- RTSP 响应生成 ---

func TestResponse_WriteTo(t *testing.T) {
	resp := newResponse("1", StatusOK)
	resp.Headers["Public"] = "OPTIONS, DESCRIBE"
	buf := &bytes.Buffer{}
	if err := resp.WriteTo(buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "RTSP/1.0 200 OK\r\n") {
		t.Errorf("response should start with status line, got: %q", out[:20])
	}
	if !strings.Contains(out, "CSeq: 1\r\n") {
		t.Errorf("response should contain CSeq header")
	}
	if !strings.Contains(out, "Public: OPTIONS, DESCRIBE\r\n") {
		t.Errorf("response should contain Public header")
	}
	if !strings.HasSuffix(out, "\r\n") {
		t.Errorf("response should end with \\r\\n")
	}
}

func TestResponse_WithBody(t *testing.T) {
	resp := newResponse("2", StatusOK)
	body := []byte("v=0\r\nm=audio\r\n")
	resp.SetBody(body, "application/sdp")
	buf := &bytes.Buffer{}
	if err := resp.WriteTo(buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Content-Type: application/sdp") {
		t.Errorf("should contain Content-Type header")
	}
	if !strings.Contains(out, "Content-Length: "+itoa(len(body))) {
		t.Errorf("should contain Content-Length header")
	}
	if !strings.HasSuffix(out, string(body)) {
		t.Errorf("response should end with body")
	}
}

func TestResponse_WithSession(t *testing.T) {
	resp := newResponse("3", StatusOK)
	resp.Session = "session-xyz"
	buf := &bytes.Buffer{}
	_ = resp.WriteTo(buf)
	out := buf.String()
	if !strings.Contains(out, "Session: session-xyz\r\n") {
		t.Errorf("response should contain Session header")
	}
}

func TestResponse_StatusCodes(t *testing.T) {
	tests := []struct {
		code    int
		reason  string
	}{
		{StatusOK, "OK"},
		{StatusBadRequest, "Bad Request"},
		{StatusNotFound, "Not Found"},
		{StatusNotImplemented, "Not Implemented"},
		{StatusInternalError, "Internal Server Error"},
	}
	for _, tt := range tests {
		resp := newResponse("1", tt.code)
		if resp.Reason != tt.reason {
			t.Errorf("code %d reason = %q, want %q", tt.code, resp.Reason, tt.reason)
		}
	}
}

// --- 辅助函数测试 ---

func TestParseInterleaved(t *testing.T) {
	tests := []struct {
		transport string
		channel   int
		isTCP     bool
	}{
		{"RTP/AVP/TCP;interleaved=0-1", 0, true},
		{"RTP/AVP/TCP;interleaved=2-3", 2, true},
		{"RTP/AVP/TCP", 0, true}, // TCP 但未指定，默认 0
		{"RTP/AVP/UDP;unicast", -1, false},
		{"RTP/AVP", -1, false},
	}
	for _, tt := range tests {
		ch, isTCP := parseInterleaved(tt.transport)
		if ch != tt.channel || isTCP != tt.isTCP {
			t.Errorf("parseInterleaved(%q) = (%d, %v), want (%d, %v)",
				tt.transport, ch, isTCP, tt.channel, tt.isTCP)
		}
	}
}

func TestTrackIDFromURI(t *testing.T) {
	tests := []struct {
		uri     string
		want    string
	}{
		{"rtsp://host/path/trackID=audio", "audio"},
		{"rtsp://host/path/trackID=video", "video"},
		{"rtsp://host/path/audio", "audio"},
		{"rtsp://host/path/", ""},
	}
	for _, tt := range tests {
		got := trackIDFromURI(tt.uri)
		if string(got) != tt.want {
			t.Errorf("trackIDFromURI(%q) = %q, want %q", tt.uri, got, tt.want)
		}
	}
}

func TestParseRTPMap(t *testing.T) {
	tests := []struct {
		rtpmap   string
		media    string
		codec    common.CodecType
		clock    uint32
		channels uint16
	}{
		{"96 H264/90000", "video", common.CodecH264, 90000, 0},
		{"97 opus/48000/2", "audio", common.CodecOpus, 48000, 2},
		{"96 opus/48000", "audio", common.CodecOpus, 48000, 0},
		{"96 pcmu/8000", "audio", common.CodecPCMU, 8000, 0},
	}
	for _, tt := range tests {
		codec, cr, ch := parseRTPMap(tt.rtpmap, tt.media)
		if codec != tt.codec {
			t.Errorf("parseRTPMap(%q) codec = %v, want %v", tt.rtpmap, codec, tt.codec)
		}
		if cr != tt.clock {
			t.Errorf("parseRTPMap(%q) clock = %d, want %d", tt.rtpmap, cr, tt.clock)
		}
		if ch != tt.channels {
			t.Errorf("parseRTPMap(%q) channels = %d, want %d", tt.rtpmap, ch, tt.channels)
		}
	}
}

func TestParseRTPMap_InvalidFormat(t *testing.T) {
	// 缺少 codec/clockRate 部分（只有 PT 号，没有描述）
	codec, cr, ch := parseRTPMap("96", "video")
	// 没有 desc 时返回 0, 0, 0
	if codec != 0 || cr != 0 || ch != 0 {
		t.Errorf("parseRTPMap with no desc should return zeros, got codec=%v cr=%d ch=%d", codec, cr, ch)
	}
}

// --- ParseRtpPacket 测试 ---

func TestParseRtpPacket_Valid(t *testing.T) {
	// 构造一个最小 RTP 包
	rtpData := []byte{
		0x80,       // V=2, P=0, X=0, CC=0
		0x08,       // M=0, PT=8 (PCMA)
		0x00, 0x01, // sequence
		0x00, 0x00, 0x00, 0x64, // timestamp = 100
		0x12, 0x34, 0x56, 0x78, // SSRC
		0xAA, 0xBB, // payload
	}
	pkt, err := ParseRtpPacket(rtpData)
	if err != nil {
		t.Fatalf("ParseRtpPacket: %v", err)
	}
	if pkt.SequenceNumber != 1 {
		t.Errorf("sequence = %d, want 1", pkt.SequenceNumber)
	}
	if pkt.Timestamp != 100 {
		t.Errorf("timestamp = %d, want 100", pkt.Timestamp)
	}
	if pkt.SSRC != 0x12345678 {
		t.Errorf("SSRC = 0x%08X, want 0x12345678", pkt.SSRC)
	}
	if !bytes.Equal(pkt.Payload, []byte{0xAA, 0xBB}) {
		t.Errorf("payload = %v", pkt.Payload)
	}
}

func TestParseRtpPacket_Invalid(t *testing.T) {
	_, err := ParseRtpPacket([]byte{0x00})
	if err == nil {
		t.Error("expected error for invalid RTP packet")
	}
}

func TestRtpToFrame(t *testing.T) {
	// 构造 RTP 包数据
	rtpData := []byte{
		0x80, 0xE0, // V=2, M=1, PT=96
		0x00, 0x0A, // sequence = 10
		0x00, 0x00, 0x01, 0x00, // timestamp = 256
		0xDE, 0xAD, 0xBE, 0xEF, // SSRC
		0x01, 0x02, 0x03, // payload
	}
	pkt, err := ParseRtpPacket(rtpData)
	if err != nil {
		t.Fatalf("ParseRtpPacket: %v", err)
	}
	frame := RtpToFrame(pkt, common.TrackAudio, common.CodecOpus)
	if frame.Type != common.FrameAudio {
		t.Errorf("frame type = %v, want FrameAudio", frame.Type)
	}
	if frame.Codec != common.CodecOpus {
		t.Errorf("frame codec = %v, want opus", frame.Codec)
	}
	if frame.Sequence != 10 {
		t.Errorf("frame sequence = %d, want 10", frame.Sequence)
	}
	if frame.Timestamp != 256 {
		t.Errorf("frame timestamp = %d, want 256", frame.Timestamp)
	}
	if frame.SSRC != 0xDEADBEEF {
		t.Errorf("frame SSRC = 0x%08X", frame.SSRC)
	}
	if !frame.Marker {
		t.Error("frame marker should be true")
	}
	if !bytes.Equal(frame.Payload, []byte{0x01, 0x02, 0x03}) {
		t.Errorf("frame payload = %v", frame.Payload)
	}
}

func TestClockRateForCodec(t *testing.T) {
	tests := []struct {
		codec common.CodecType
		want  uint32
	}{
		{common.CodecOpus, 48000},
		{common.CodecPCMU, 8000},
		{common.CodecPCMA, 8000},
		{common.CodecH264, 90000},
		{common.CodecVP8, 90000},
		{common.CodecAV1, 90000},
	}
	for _, tt := range tests {
		got := clockRateForCodec(tt.codec)
		if got != tt.want {
			t.Errorf("clockRateForCodec(%v) = %d, want %d", tt.codec, got, tt.want)
		}
	}
}

// itoa 简单实现 strconv.Itoa 避免额外 import
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
