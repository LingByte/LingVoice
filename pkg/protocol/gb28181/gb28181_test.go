package gb28181

import (
	"strconv"
	"strings"
	"testing"
)

// ─── SIP 消息解析测试 ────────────────────────────────────────────────────────

const sampleRegister = `REGISTER sip:34020000002000000001@3402000000 SIP/2.0
Via: SIP/2.0/UDP 192.168.1.100:5060;rport;branch=z9hG4bK123456
From: <sip:34020000001320000001@3402000000>;tag=123456
To: <sip:34020000001320000001@3402000000>
Call-ID: abc123@192.168.1.100
CSeq: 1 REGISTER
Contact: <sip:34020000001320000001@192.168.1.100:5060>
Max-Forwards: 70
User-Agent: IPC
Content-Length: 0

`

func TestParseSipRequest_Register(t *testing.T) {
	req, err := ParseSipRequest([]byte(sampleRegister))
	if err != nil {
		t.Fatalf("parse register: %v", err)
	}
	if req.Method != MethodRegister {
		t.Errorf("method = %q, want %q", req.Method, MethodRegister)
	}
	if req.URI != "sip:34020000002000000001@3402000000" {
		t.Errorf("uri = %q", req.URI)
	}
	if req.Proto != "SIP/2.0" {
		t.Errorf("proto = %q", req.Proto)
	}
	if req.CallID != "abc123@192.168.1.100" {
		t.Errorf("call-id = %q", req.CallID)
	}
	if req.CSeq != "1 REGISTER" {
		t.Errorf("cseq = %q", req.CSeq)
	}
	if req.CSeqNum != 1 || req.CSeqMethod != MethodRegister {
		t.Errorf("cseq parsed = (%d, %q)", req.CSeqNum, req.CSeqMethod)
	}
	if req.FromTag != "123456" {
		t.Errorf("from tag = %q", req.FromTag)
	}
	if req.Branch != "z9hG4bK123456" {
		t.Errorf("branch = %q", req.Branch)
	}
	if req.Contact != "<sip:34020000001320000001@192.168.1.100:5060>" {
		t.Errorf("contact = %q", req.Contact)
	}
	if req.MaxForwards != 70 {
		t.Errorf("max-forwards = %d", req.MaxForwards)
	}
	if req.UserAgent != "IPC" {
		t.Errorf("user-agent = %q", req.UserAgent)
	}
	if len(req.Body) != 0 {
		t.Errorf("body should be empty, got %d bytes", len(req.Body))
	}
}

func TestParseSipRequest_ExtractUser(t *testing.T) {
	req, err := ParseSipRequest([]byte(sampleRegister))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	uid := extractUser(req.From)
	if uid != "34020000001320000001" {
		t.Errorf("extractUser(from) = %q", uid)
	}
}

const inviteSDP = `v=0
o=34020000001320000001 0 0 IN IP4 192.168.1.100
s=Play
c=IN IP4 192.168.1.100
t=0 0
m=video 6000 RTP/AVP 96 98
a=rtpmap:96 PS/90000
a=rtpmap:98 H264/90000
a=recvonly
y=0100000001
`

var sampleInvite = "INVITE sip:34020000001320000001@3402000000 SIP/2.0\r\n" +
	"Via: SIP/2.0/UDP 192.168.1.100:5060;rport;branch=z9hG4bK777\r\n" +
	"From: <sip:34020000002000000001@3402000000>;tag=abc\r\n" +
	"To: <sip:34020000001320000001@3402000000>\r\n" +
	"Call-ID: call-001@192.168.1.100\r\n" +
	"CSeq: 2 INVITE\r\n" +
	"Content-Type: application/sdp\r\n" +
	"Content-Length: " + strconv.Itoa(len(inviteSDP)) + "\r\n" +
	"\r\n" +
	inviteSDP

func TestParseSipRequest_InviteWithSDP(t *testing.T) {
	req, err := ParseSipRequest([]byte(sampleInvite))
	if err != nil {
		t.Fatalf("parse invite: %v", err)
	}
	if req.Method != MethodInvite {
		t.Errorf("method = %q", req.Method)
	}
	if req.CSeqNum != 2 {
		t.Errorf("cseq num = %d", req.CSeqNum)
	}
	if len(req.Body) == 0 {
		t.Fatal("invite body should contain SDP")
	}

	sdp, err := ParseSDP(req.Body)
	if err != nil {
		t.Fatalf("parse sdp: %v", err)
	}
	if sdp.MediaType != "video" {
		t.Errorf("sdp media = %q", sdp.MediaType)
	}
	if sdp.Port != 6000 {
		t.Errorf("sdp port = %d", sdp.Port)
	}
	if sdp.Transport != "RTP/AVP" {
		t.Errorf("sdp transport = %q", sdp.Transport)
	}
	if len(sdp.PayloadTypes) != 2 || sdp.PayloadTypes[0] != 96 || sdp.PayloadTypes[1] != 98 {
		t.Errorf("payload types = %v", sdp.PayloadTypes)
	}
	if sdp.Attributes["rtpmap:96"] != "PS/90000" {
		t.Errorf("rtpmap:96 = %q", sdp.Attributes["rtpmap:96"])
	}
	if sdp.Attributes["rtpmap:98"] != "H264/90000" {
		t.Errorf("rtpmap:98 = %q", sdp.Attributes["rtpmap:98"])
	}
	if sdp.SSRC != "0100000001" {
		t.Errorf("ssrc = %q", sdp.SSRC)
	}
	if sdp.ConnectionAddress != "192.168.1.100" {
		t.Errorf("connection addr = %q", sdp.ConnectionAddress)
	}
}

func TestParseSipRequest_InvalidRequestLine(t *testing.T) {
	_, err := ParseSipRequest([]byte("garbage\r\n\r\n"))
	if err == nil {
		t.Fatal("expected error for garbage")
	}
}

func TestParseSipRequest_ResponseDetection(t *testing.T) {
	resp := "SIP/2.0 200 OK\r\n\r\n"
	if !IsSipResponse([]byte(resp)) {
		t.Error("IsSipResponse should be true")
	}
	if IsSipResponse([]byte(sampleRegister)) {
		t.Error("IsSipResponse should be false for request")
	}
}

// ─── SIP 响应生成测试 ────────────────────────────────────────────────────────

func TestSipResponse_WriteTo(t *testing.T) {
	resp := newSipResponse(StatusOK)
	resp.Via = "SIP/2.0/UDP 192.168.1.100:5060;rport;branch=z9hG4bK123456"
	resp.From = "<sip:34020000001320000001@3402000000>;tag=123456"
	resp.To = "<sip:34020000001320000001@3402000000>"
	resp.ToTag = "server-tag-1"
	resp.CallID = "abc123@192.168.1.100"
	resp.CSeq = "1 REGISTER"
	resp.SetBody([]byte("hello"), "text/plain")

	var sb stringBuilder
	if err := resp.Render(&sb); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := string(sb.Bytes())
	if !strings.HasPrefix(out, "SIP/2.0 200 OK\r\n") {
		t.Errorf("status line missing: %q", out[:20])
	}
	if !strings.Contains(out, "Via: SIP/2.0/UDP 192.168.1.100:5060") {
		t.Error("via header missing")
	}
	if !strings.Contains(out, "tag=server-tag-1") {
		t.Error("to tag missing")
	}
	if !strings.Contains(out, "Content-Length: 5") {
		t.Error("content-length missing")
	}
	if !strings.HasSuffix(out, "\r\nhello") {
		t.Errorf("body not appended correctly: suffix=%q", out[len(out)-10:])
	}
}

func TestBuildSdpAnswer(t *testing.T) {
	sdp := BuildSdpAnswer(SdpAnswerConfig{
		LocalIP:   "192.168.1.10",
		LocalPort: 50000,
		SSRC:      "0100000001",
		DeviceID:  "34020000002000000001",
	})
	if !strings.Contains(sdp, "v=0") {
		t.Error("v= missing")
	}
	if !strings.Contains(sdp, "o=34020000002000000001 0 0 IN IP4 192.168.1.10") {
		t.Error("o= line wrong")
	}
	if !strings.Contains(sdp, "m=video 50000 RTP/AVP 96") {
		t.Error("m= line wrong")
	}
	if !strings.Contains(sdp, "a=rtpmap:96 PS/90000") {
		t.Error("rtpmap missing")
	}
	if !strings.Contains(sdp, "y=0100000001") {
		t.Error("y= line missing")
	}
}

// ─── PS header 解析测试 ──────────────────────────────────────────────────────

// 构造一个最小 PS pack header (0x000001BA) + stuffing
func makePackHeader() []byte {
	// 14 字节 pack header：起始码 + SCR(6) + mux rate(3) + stuffing length(3 bits)=0
	h := make([]byte, 14)
	h[0], h[1], h[2], h[3] = 0x00, 0x00, 0x01, 0xBA
	// SCR 字段填充 0x44（marker bits）
	h[4] = 0x44
	h[5] = 0x00
	h[6] = 0x04
	h[7] = 0x00
	h[8] = 0x04
	h[9] = 0x01
	// mux rate
	h[10] = 0x00
	h[11] = 0x00
	h[12] = 0x03
	// 第 13 字节：reserved(5) + stuffing(3)
	h[13] = 0xF8 // stuffing length = 0
	return h
}

func TestSkipPackHeader(t *testing.T) {
	h := makePackHeader()
	end, err := skipPackHeader(h, 0)
	if err != nil {
		t.Fatalf("skipPackHeader: %v", err)
	}
	if end != 14 {
		t.Errorf("end = %d, want 14", end)
	}
}

func TestSkipPackHeader_Stuffing(t *testing.T) {
	h := makePackHeader()
	// 设置 stuffing length = 2，并追加 2 字节
	h[13] = 0xFA // stuffing length = 2
	h = append(h, 0xFF, 0xFF)
	end, err := skipPackHeader(h, 0)
	if err != nil {
		t.Fatalf("skipPackHeader: %v", err)
	}
	if end != 16 {
		t.Errorf("end = %d, want 16", end)
	}
}

func TestSkipSection_SystemHeader(t *testing.T) {
	// 构造 system header: 0x000001BB + length(2) + data
	h := []byte{0x00, 0x00, 0x01, 0xBB, 0x00, 0x03, 0xAA, 0xBB, 0xCC}
	end, err := skipSection(h, 0)
	if err != nil {
		t.Fatalf("skipSection: %v", err)
	}
	if end != 9 {
		t.Errorf("end = %d, want 9", end)
	}
}

// ─── PES 解析测试 ────────────────────────────────────────────────────────────

// 构造一个视频 PES packet
func makeVideoPES(payload []byte) []byte {
	// 起始码 0x000001E0 + length(2) + flags(2) + header_data_len(1) + payload
	pesLen := len(payload) + 3 // flags(2) + header_data_len(1)
	h := []byte{0x00, 0x00, 0x01, 0xE0}
	h = append(h, byte(pesLen>>8), byte(pesLen&0xFF))
	// flags1 = 0x80, flags2 = 0x80 (PTS only), header_data_len = 0
	h = append(h, 0x80, 0x00, 0x00)
	h = append(h, payload...)
	return h
}

func TestParsePES_Video(t *testing.T) {
	payload := []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0A} // SPS NALU
	pes := makeVideoPES(payload)
	p, end, err := parsePES(pes, 0)
	if err != nil {
		t.Fatalf("parsePES: %v", err)
	}
	if p.StreamID != 0xE0 {
		t.Errorf("stream id = %#x", p.StreamID)
	}
	if end != len(pes) {
		t.Errorf("end = %d, want %d", end, len(pes))
	}
	if len(p.Payload) != len(payload) {
		t.Errorf("payload len = %d, want %d", len(p.Payload), len(payload))
	}
}

// ─── Annex-B 拆分测试 ────────────────────────────────────────────────────────

func TestSplitAnnexB(t *testing.T) {
	// 两个 NALU：SPS + IDR
	data := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0A, // SPS
		0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40, // IDR
	}
	nalus := splitAnnexB(data)
	if len(nalus) != 2 {
		t.Fatalf("nalus count = %d, want 2", len(nalus))
	}
	if NaluType(nalus[0]) != 7 {
		t.Errorf("nal0 type = %d, want 7 (SPS)", NaluType(nalus[0]))
	}
	if NaluTypeName(nalus[0]) != "SPS" {
		t.Errorf("nal0 name = %q", NaluTypeName(nalus[0]))
	}
	if NaluType(nalus[1]) != 5 {
		t.Errorf("nal1 type = %d, want 5 (IDR)", NaluType(nalus[1]))
	}
	if NaluTypeName(nalus[1]) != "IDR" {
		t.Errorf("nal1 name = %q", NaluTypeName(nalus[1]))
	}
}

func TestSplitAnnexB_3ByteStartCode(t *testing.T) {
	data := []byte{
		0x00, 0x00, 0x01, 0x67, 0x42, // SPS with 3-byte start code
		0x00, 0x00, 0x01, 0x65, 0x88, // IDR with 3-byte start code
	}
	nalus := splitAnnexB(data)
	if len(nalus) != 2 {
		t.Fatalf("nalus count = %d, want 2", len(nalus))
	}
}

func TestParsePTS(t *testing.T) {
	// 构造 PTS = 0 的 5 字节
	b := []byte{0x10, 0x00, 0x01, 0x00, 0x01}
	pts := parsePTS(b)
	// marker bits 应被屏蔽，PTS=0
	if pts != 0 {
		t.Errorf("pts = %d, want 0", pts)
	}
}

// ─── findStartCode 测试 ──────────────────────────────────────────────────────

func TestFindStartCode(t *testing.T) {
	data := []byte{0xAA, 0xBB, 0x00, 0x00, 0x01, 0xBA, 0xCC}
	pos, code, ok := findStartCode(data, 0)
	if !ok {
		t.Fatal("start code not found")
	}
	if pos != 2 {
		t.Errorf("pos = %d, want 2", pos)
	}
	if code != 0xBA {
		t.Errorf("code = %#x", code)
	}
}
