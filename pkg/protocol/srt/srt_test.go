package srt

import (
	"bytes"
	"net"
	"testing"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"go.uber.org/zap"
)

// --- SRT 包解析测试 ---

func TestParsePacket_DataPacket(t *testing.T) {
	payload := []byte{0x01, 0x02, 0x03, 0x04}
	data := BuildDataPacket(12345, 67890, 1000000, 999, payload)

	pkt, err := ParsePacket(data)
	if err != nil {
		t.Fatalf("ParsePacket: %v", err)
	}
	if pkt.IsControl {
		t.Error("data packet should not be control")
	}
	if pkt.SeqNum != 12345 {
		t.Errorf("SeqNum = %d, want 12345", pkt.SeqNum)
	}
	if pkt.MsgNo != 67890 {
		t.Errorf("MsgNo = %d, want 67890", pkt.MsgNo)
	}
	if pkt.Timestamp != 1000000 {
		t.Errorf("Timestamp = %d, want 1000000", pkt.Timestamp)
	}
	if pkt.SocketID != 999 {
		t.Errorf("SocketID = %d, want 999", pkt.SocketID)
	}
	if !bytes.Equal(pkt.Payload, payload) {
		t.Errorf("Payload = %v, want %v", pkt.Payload, payload)
	}
}

func TestParsePacket_ControlPacket(t *testing.T) {
	payload := []byte{0xAA, 0xBB}
	data := BuildControlPacket(ctrlKeepalive, 0, 12345, payload)

	pkt, err := ParsePacket(data)
	if err != nil {
		t.Fatalf("ParsePacket: %v", err)
	}
	if !pkt.IsControl {
		t.Error("control packet should be control")
	}
	if pkt.CtrlType != ctrlKeepalive {
		t.Errorf("CtrlType = %d, want %d", pkt.CtrlType, ctrlKeepalive)
	}
	if pkt.SocketID != 12345 {
		t.Errorf("SocketID = %d, want 12345", pkt.SocketID)
	}
	if !bytes.Equal(pkt.Payload, payload) {
		t.Errorf("Payload = %v, want %v", pkt.Payload, payload)
	}
}

func TestParsePacket_TooShort(t *testing.T) {
	_, err := ParsePacket([]byte{0x01, 0x02, 0x03})
	if err != ErrPacketTooShort {
		t.Errorf("expected ErrPacketTooShort, got %v", err)
	}
}

func TestParsePacket_Empty(t *testing.T) {
	_, err := ParsePacket(nil)
	if err != ErrPacketTooShort {
		t.Errorf("expected ErrPacketTooShort for nil, got %v", err)
	}
}

func TestParsePacket_ExactHeaderLen(t *testing.T) {
	// 刚好 16 字节，无 payload
	data := BuildDataPacket(1, 2, 3, 4, nil)
	pkt, err := ParsePacket(data)
	if err != nil {
		t.Fatalf("ParsePacket: %v", err)
	}
	if len(pkt.Payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(pkt.Payload))
	}
}

// --- SRT 包序列化 round-trip ---

func TestPacket_DataRoundTrip(t *testing.T) {
	payload := make([]byte, 100)
	for i := range payload {
		payload[i] = byte(i)
	}

	orig := &Packet{
		IsControl: false,
		SeqNum:    0x12345,
		MsgNo:     0x6789A,
		Timestamp: 0xABCDEF,
		SocketID:  0x11223344,
	}

	data := BuildDataPacket(orig.SeqNum, orig.MsgNo, orig.Timestamp, orig.SocketID, payload)
	parsed, err := ParsePacket(data)
	if err != nil {
		t.Fatalf("ParsePacket: %v", err)
	}

	if parsed.IsControl != orig.IsControl {
		t.Errorf("IsControl = %v, want %v", parsed.IsControl, orig.IsControl)
	}
	if parsed.SeqNum != orig.SeqNum {
		t.Errorf("SeqNum = %d, want %d", parsed.SeqNum, orig.SeqNum)
	}
	if parsed.MsgNo != orig.MsgNo {
		t.Errorf("MsgNo = %d, want %d", parsed.MsgNo, orig.MsgNo)
	}
	if parsed.Timestamp != orig.Timestamp {
		t.Errorf("Timestamp = %d, want %d", parsed.Timestamp, orig.Timestamp)
	}
	if parsed.SocketID != orig.SocketID {
		t.Errorf("SocketID = %d, want %d", parsed.SocketID, orig.SocketID)
	}
	if !bytes.Equal(parsed.Payload, payload) {
		t.Errorf("Payload mismatch")
	}
}

func TestPacket_ControlRoundTrip(t *testing.T) {
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}

	tests := []struct {
		name     string
		ctrlType uint16
		subtype  uint16
		socket   uint32
	}{
		{"handshake", ctrlHandshake, 0, 0},
		{"keepalive", ctrlKeepalive, 0, 12345},
		{"ack", ctrlAck, 0, 999},
		{"nak", ctrlNak, 0, 888},
		{"shutdown", ctrlShutdown, 0, 777},
		{"ack2", ctrlAck2, 0, 666},
		{"with-subtype", ctrlHandshake, 0x1234, 555},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := BuildControlPacket(tt.ctrlType, tt.subtype, tt.socket, payload)
			pkt, err := ParsePacket(data)
			if err != nil {
				t.Fatalf("ParsePacket: %v", err)
			}
			if !pkt.IsControl {
				t.Error("should be control packet")
			}
			if pkt.CtrlType != tt.ctrlType {
				t.Errorf("CtrlType = %d, want %d", pkt.CtrlType, tt.ctrlType)
			}
			if pkt.Subtype != tt.subtype {
				t.Errorf("Subtype = %d, want %d", pkt.Subtype, tt.subtype)
			}
			if pkt.SocketID != tt.socket {
				t.Errorf("SocketID = %d, want %d", pkt.SocketID, tt.socket)
			}
			if !bytes.Equal(pkt.Payload, payload) {
				t.Errorf("Payload mismatch")
			}
		})
	}
}

// --- IsControlPacket 测试 ---

func TestIsControlPacket(t *testing.T) {
	// control packet: 最高 bit = 1
	ctrlData := BuildControlPacket(ctrlKeepalive, 0, 1, nil)
	if !IsControlPacket(ctrlData) {
		t.Error("control packet should be detected as control")
	}

	// data packet: 最高 bit = 0
	dataPkt := BuildDataPacket(1, 2, 3, 4, nil)
	if IsControlPacket(dataPkt) {
		t.Error("data packet should not be detected as control")
	}
}

func TestIsControlPacket_TooShort(t *testing.T) {
	// 空数据或太短的数据不应被识别为 control packet
	if IsControlPacket([]byte{}) {
		t.Error("empty data should not be control")
	}
	if IsControlPacket([]byte{0x00}) {
		t.Error("single byte 0x00 should not be control")
	}
	if IsControlPacket([]byte{0x80}) {
		t.Error("single byte 0x80 should not be control (too short, need >= 4 bytes)")
	}
	// 4 字节但 control bit=0
	if IsControlPacket([]byte{0x00, 0x00, 0x00, 0x00}) {
		t.Error("4 bytes with control bit=0 should not be control")
	}
	// 4 字节 control bit=1
	if !IsControlPacket([]byte{0x80, 0x00, 0x00, 0x00}) {
		t.Error("4 bytes with control bit=1 should be control")
	}
}

// --- Handshake 包识别 ---

func TestHandshake_ControlType(t *testing.T) {
	// handshake 的 control type 应为 0x0000
	hs := &HandshakePacket{
		Version:       hsVersionV5,
		HandshakeType: HsInduction,
		SocketID:      12345,
	}
	resp := BuildHandshakeResponse(hs, 0)
	pkt, err := ParsePacket(resp)
	if err != nil {
		t.Fatalf("ParsePacket: %v", err)
	}
	if !pkt.IsControl {
		t.Error("handshake response should be control packet")
	}
	if pkt.CtrlType != ctrlHandshake {
		t.Errorf("CtrlType = %d, want %d (ctrlHandshake)", pkt.CtrlType, ctrlHandshake)
	}
}

// --- Handshake 包解析/序列化 ---

func TestHandshake_RoundTrip(t *testing.T) {
	orig := &HandshakePacket{
		Version:       hsVersionV5,
		Encryption:    0,
		Extension:     0x1234,
		InitSeqNum:    1000,
		MTU:           hsMtuDefault,
		MaxFlowWindow: hsFlowDefault,
		HandshakeType: HsConclusion,
		SocketID:      54321,
		SynCookie:     0xCAFEBABE,
		PeerIP:        [4]byte{192, 168, 1, 100},
	}

	data := BuildHandshake(orig)
	parsed, err := ParseHandshake(data)
	if err != nil {
		t.Fatalf("ParseHandshake: %v", err)
	}

	if parsed.Version != orig.Version {
		t.Errorf("Version = %d, want %d", parsed.Version, orig.Version)
	}
	if parsed.Encryption != orig.Encryption {
		t.Errorf("Encryption = %d, want %d", parsed.Encryption, orig.Encryption)
	}
	if parsed.Extension != orig.Extension {
		t.Errorf("Extension = %d, want %d", parsed.Extension, orig.Extension)
	}
	if parsed.InitSeqNum != orig.InitSeqNum {
		t.Errorf("InitSeqNum = %d, want %d", parsed.InitSeqNum, orig.InitSeqNum)
	}
	if parsed.MTU != orig.MTU {
		t.Errorf("MTU = %d, want %d", parsed.MTU, orig.MTU)
	}
	if parsed.MaxFlowWindow != orig.MaxFlowWindow {
		t.Errorf("MaxFlowWindow = %d, want %d", parsed.MaxFlowWindow, orig.MaxFlowWindow)
	}
	if parsed.HandshakeType != orig.HandshakeType {
		t.Errorf("HandshakeType = %d, want %d", parsed.HandshakeType, orig.HandshakeType)
	}
	if parsed.SocketID != orig.SocketID {
		t.Errorf("SocketID = %d, want %d", parsed.SocketID, orig.SocketID)
	}
	if parsed.SynCookie != orig.SynCookie {
		t.Errorf("SynCookie = %d, want %d", parsed.SynCookie, orig.SynCookie)
	}
	if !bytes.Equal(parsed.PeerIP[:], orig.PeerIP[:]) {
		t.Errorf("PeerIP = %v, want %v", parsed.PeerIP, orig.PeerIP)
	}
}

func TestHandshake_WithStreamID(t *testing.T) {
	orig := &HandshakePacket{
		Version:       hsVersionV5,
		HandshakeType: HsConclusion,
		SocketID:      12345,
		StreamID:      "live/test-stream",
	}

	data := BuildHandshake(orig)
	parsed, err := ParseHandshake(data)
	if err != nil {
		t.Fatalf("ParseHandshake: %v", err)
	}
	if parsed.StreamID != orig.StreamID {
		t.Errorf("StreamID = %q, want %q", parsed.StreamID, orig.StreamID)
	}
}

func TestHandshake_WithStreamID_Padding(t *testing.T) {
	// 测试非 4 字节对齐的 streamID
	orig := &HandshakePacket{
		Version:       hsVersionV5,
		HandshakeType: HsInduction,
		StreamID:      "abc", // 3 bytes, 需要 padding 到 4
	}
	data := BuildHandshake(orig)
	parsed, err := ParseHandshake(data)
	if err != nil {
		t.Fatalf("ParseHandshake: %v", err)
	}
	if parsed.StreamID != "abc" {
		t.Errorf("StreamID = %q, want %q", parsed.StreamID, "abc")
	}
}

func TestHandshake_TooShort(t *testing.T) {
	_, err := ParseHandshake([]byte{0x01, 0x02, 0x03})
	if err != ErrHandshakeTooShort {
		t.Errorf("expected ErrHandshakeTooShort, got %v", err)
	}
}

func TestHandshake_Empty(t *testing.T) {
	_, err := ParseHandshake(nil)
	if err != ErrHandshakeTooShort {
		t.Errorf("expected ErrHandshakeTooShort for nil, got %v", err)
	}
}

func TestHandshake_ExactHeaderLen(t *testing.T) {
	// 刚好 48 字节
	hs := &HandshakePacket{
		Version:       hsVersionV5,
		HandshakeType: HsInduction,
	}
	data := BuildHandshake(hs)
	if len(data) != hsHeaderLen {
		t.Errorf("header-only handshake should be %d bytes, got %d", hsHeaderLen, len(data))
	}
	parsed, err := ParseHandshake(data)
	if err != nil {
		t.Fatalf("ParseHandshake: %v", err)
	}
	if parsed.StreamID != "" {
		t.Errorf("StreamID should be empty, got %q", parsed.StreamID)
	}
}

// --- Handshake 阶段测试 ---

func TestHandshake_InductionPhase(t *testing.T) {
	// induction 阶段：version 可能为 0 或 5
	hs := &HandshakePacket{
		Version:       0, // induction 初始 version 可能为 0
		HandshakeType: HsInduction,
		SocketID:      0, // induction 阶段 socket ID = 0
		MTU:           hsMtuDefault,
		MaxFlowWindow: hsFlowDefault,
	}
	data := BuildHandshake(hs)
	parsed, err := ParseHandshake(data)
	if err != nil {
		t.Fatalf("ParseHandshake: %v", err)
	}
	if parsed.HandshakeType != HsInduction {
		t.Errorf("HandshakeType = %d, want HsInduction (%d)", parsed.HandshakeType, HsInduction)
	}
	if err := ValidateHandshake(parsed); err != nil {
		t.Errorf("ValidateHandshake for induction (version=0): %v", err)
	}
}

func TestHandshake_ConclusionPhase(t *testing.T) {
	hs := &HandshakePacket{
		Version:       hsVersionV5,
		HandshakeType: HsConclusion,
		SocketID:      54321,
		MTU:           hsMtuDefault,
		MaxFlowWindow: hsFlowDefault,
	}
	data := BuildHandshake(hs)
	parsed, err := ParseHandshake(data)
	if err != nil {
		t.Fatalf("ParseHandshake: %v", err)
	}
	if parsed.HandshakeType != HsConclusion {
		t.Errorf("HandshakeType = %d, want HsConclusion (%d)", parsed.HandshakeType, HsConclusion)
	}
	if err := ValidateHandshake(parsed); err != nil {
		t.Errorf("ValidateHandshake for conclusion: %v", err)
	}
}

func TestValidateHandshake_UnsupportedVersion(t *testing.T) {
	hs := &HandshakePacket{
		Version: 3, // 不支持的版本
	}
	err := ValidateHandshake(hs)
	if err == nil {
		t.Error("expected error for unsupported version")
	}
}

func TestValidateHandshake_Version5(t *testing.T) {
	hs := &HandshakePacket{Version: hsVersionV5}
	if err := ValidateHandshake(hs); err != nil {
		t.Errorf("version 5 should be valid: %v", err)
	}
}

func TestValidateHandshake_Version0(t *testing.T) {
	hs := &HandshakePacket{Version: 0}
	if err := ValidateHandshake(hs); err != nil {
		t.Errorf("version 0 (induction) should be valid: %v", err)
	}
}

// --- HandshakeResponse 测试 ---

func TestBuildHandshakeResponse(t *testing.T) {
	hs := &HandshakePacket{
		Version:       hsVersionV5,
		HandshakeType: HsInduction,
		SocketID:      12345,
	}
	resp := BuildHandshakeResponse(hs, 999)

	// 解析为 SRT packet
	pkt, err := ParsePacket(resp)
	if err != nil {
		t.Fatalf("ParsePacket: %v", err)
	}
	if !pkt.IsControl {
		t.Error("response should be control packet")
	}
	if pkt.CtrlType != ctrlHandshake {
		t.Errorf("CtrlType = %d, want %d", pkt.CtrlType, ctrlHandshake)
	}
	if pkt.SocketID != 999 {
		t.Errorf("SocketID = %d, want 999 (dest socket)", pkt.SocketID)
	}
	// subtype 应为 handshake type
	if pkt.Subtype != uint16(HsInduction) {
		t.Errorf("Subtype = %d, want %d", pkt.Subtype, uint16(HsInduction))
	}

	// 解析 handshake payload
	hsParsed, err := ParseHandshake(pkt.Payload)
	if err != nil {
		t.Fatalf("ParseHandshake: %v", err)
	}
	if hsParsed.Version != hsVersionV5 {
		t.Errorf("Version = %d, want %d", hsParsed.Version, hsVersionV5)
	}
}

// --- Session 创建/查找测试 ---

func TestSession_Creation(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(192, 168, 1, 1), Port: 12345}
	s := newSession("test-srt-session", addr, 1001, "live/stream1", nil, nil)

	if s.ID() != "test-srt-session" {
		t.Errorf("ID = %q, want test-srt-session", s.ID())
	}
	if s.SocketID() != 1001 {
		t.Errorf("SocketID = %d, want 1001", s.SocketID())
	}
	if s.StreamID() != "live/stream1" {
		t.Errorf("StreamID = %q, want live/stream1", s.StreamID())
	}
	if s.Protocol() != common.ProtocolSRT {
		t.Errorf("Protocol = %v, want SRT", s.Protocol())
	}
	if s.PeerAddr().String() != addr.String() {
		t.Errorf("PeerAddr = %v, want %v", s.PeerAddr(), addr)
	}
}

func TestSession_PeerSocketID(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 80}
	s := newSession("s1", addr, 100, "stream", nil, nil)

	if s.PeerSocketID() != 0 {
		t.Errorf("initial PeerSocketID = %d, want 0", s.PeerSocketID())
	}
	s.SetPeerSocketID(999)
	if s.PeerSocketID() != 999 {
		t.Errorf("PeerSocketID = %d, want 999", s.PeerSocketID())
	}
}

func TestSession_Tracks(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 80}
	s := newSession("s1", addr, 100, "stream", nil, nil)

	tracks := s.Tracks()
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(tracks))
	}
	if tracks[0].Kind != common.TrackVideo {
		t.Errorf("track kind = %v, want video", tracks[0].Kind)
	}
	if tracks[0].Codec != common.CodecH264 {
		t.Errorf("track codec = %v, want h264", tracks[0].Codec)
	}
	if tracks[0].StreamID != "stream" {
		t.Errorf("track streamID = %q, want stream", tracks[0].StreamID)
	}
	if tracks[0].Direction != common.TrackRecv {
		t.Errorf("track direction = %v, want TrackRecv", tracks[0].Direction)
	}
}

func TestSession_SendCommand_Hangup(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 80}
	s := newSession("s1", addr, 100, "stream", nil, nil)

	// CmdHangup 应该触发 Close
	err := s.SendCommand(common.ProtocolCommand{Type: common.CmdHangup})
	if err != nil {
		t.Errorf("SendCommand hangup: %v", err)
	}
}

func TestSession_SendCommand_Unknown(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 80}
	s := newSession("s1", addr, 100, "stream", nil, nil)

	err := s.SendCommand(common.ProtocolCommand{Type: common.CmdAnswer})
	if err != nil {
		t.Errorf("SendCommand unknown should return nil, got: %v", err)
	}
}

func TestSession_MediaStats(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 80}
	s := newSession("s1", addr, 100, "stream", nil, nil)

	stats := s.MediaStats()
	if stats != nil {
		t.Errorf("MediaStats should return nil, got %v", stats)
	}
}

func TestSession_LastActive(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 80}
	s := newSession("s1", addr, 100, "stream", nil, nil)

	la := s.LastActive()
	if la.IsZero() {
		t.Error("LastActive should not be zero after creation")
	}
}

// --- BuildShutdown / BuildKeepalive 测试 ---

func TestBuildShutdown(t *testing.T) {
	data := BuildShutdown(12345)
	pkt, err := ParsePacket(data)
	if err != nil {
		t.Fatalf("ParsePacket: %v", err)
	}
	if !pkt.IsControl {
		t.Error("shutdown should be control packet")
	}
	if pkt.CtrlType != ctrlShutdown {
		t.Errorf("CtrlType = %d, want %d", pkt.CtrlType, ctrlShutdown)
	}
	if pkt.SocketID != 12345 {
		t.Errorf("SocketID = %d, want 12345", pkt.SocketID)
	}
}

func TestBuildKeepalive(t *testing.T) {
	data := BuildKeepalive(67890)
	pkt, err := ParsePacket(data)
	if err != nil {
		t.Fatalf("ParsePacket: %v", err)
	}
	if !pkt.IsControl {
		t.Error("keepalive should be control packet")
	}
	if pkt.CtrlType != ctrlKeepalive {
		t.Errorf("CtrlType = %d, want %d", pkt.CtrlType, ctrlKeepalive)
	}
	if pkt.SocketID != 67890 {
		t.Errorf("SocketID = %d, want 67890", pkt.SocketID)
	}
}

// --- Packet.String() 测试 ---

func TestPacket_String(t *testing.T) {
	dataPkt := &Packet{
		IsControl: false,
		SeqNum:    100,
		MsgNo:     200,
		Timestamp: 300,
		SocketID:  400,
		Payload:   []byte{1, 2, 3},
	}
	s := dataPkt.String()
	if s == "" {
		t.Error("String() should not be empty")
	}

	ctrlPkt := &Packet{
		IsControl: true,
		CtrlType:  ctrlKeepalive,
		Subtype:   0,
		SocketID:  500,
		Payload:   []byte{1, 2},
	}
	s2 := ctrlPkt.String()
	if s2 == "" {
		t.Error("String() should not be empty for control packet")
	}
}

// --- Server Session 查找测试 ---

func TestServer_GetSession_NotExist(t *testing.T) {
	srv := NewServer(DefaultConfig(), nil, zap.NewNop())
	_, ok := srv.GetSession("nonexistent")
	if ok {
		t.Error("GetSession should return false for nonexistent session")
	}
}
