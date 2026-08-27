package hls

import (
	"testing"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
)

// TestTSMuxerPAT 验证 PAT 生成: sync byte, PID=0, 包大小 188 字节。
func TestTSMuxerPAT(t *testing.T) {
	m := NewTSMuxer()
	pkt := m.MuxPAT()

	if len(pkt) != tsPacketSize {
		t.Fatalf("PAT packet size = %d, want %d", len(pkt), tsPacketSize)
	}
	if pkt[0] != tsSyncByte {
		t.Errorf("PAT sync byte = 0x%02X, want 0x%02X", pkt[0], tsSyncByte)
	}
	// PID = 0, payload_unit_start_indicator = 1
	if pkt[1]&0x40 == 0 {
		t.Error("PAT should have payload_unit_start_indicator set")
	}
	pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
	if pid != tsPATPID {
		t.Errorf("PAT PID = %d, want %d", pid, tsPATPID)
	}

	// 跳过 adaptation field 找到 payload 起点
	payloadStart := tsPayloadOffset(pkt)
	// pointer_field should be 0 (section starts immediately after)
	if pkt[payloadStart] != 0x00 {
		t.Errorf("PAT pointer_field = 0x%02X, want 0x00", pkt[payloadStart])
	}
	// table_id should be 0x00
	if pkt[payloadStart+1] != 0x00 {
		t.Errorf("PAT table_id = 0x%02X, want 0x00", pkt[payloadStart+1])
	}
}

// TestTSMuxerPMT 验证 PMT 生成: 包含 video/audio PID。
func TestTSMuxerPMT(t *testing.T) {
	m := NewTSMuxer()
	pkt := m.MuxPMT()

	if len(pkt) != tsPacketSize {
		t.Fatalf("PMT packet size = %d, want %d", len(pkt), tsPacketSize)
	}
	if pkt[0] != tsSyncByte {
		t.Errorf("PMT sync byte = 0x%02X, want 0x%02X", pkt[0], tsSyncByte)
	}
	pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
	if pid != tsPMTPID {
		t.Errorf("PMT PID = %d, want %d", pid, tsPMTPID)
	}

	// 跳过 adaptation field 找到 payload 起点
	payloadStart := tsPayloadOffset(pkt)
	// pointer_field = 0
	if pkt[payloadStart] != 0x00 {
		t.Errorf("PMT pointer_field = 0x%02X, want 0x00", pkt[payloadStart])
	}
	// table_id = 0x02
	if pkt[payloadStart+1] != 0x02 {
		t.Errorf("PMT table_id = 0x%02X, want 0x02", pkt[payloadStart+1])
	}

	// 验证 section 中包含 video PID 和 audio PID
	foundVideo := false
	foundAudio := false
	for i := payloadStart; i < len(pkt)-1; i++ {
		if pkt[i] == byte(0xE0|((tsVideoPID>>8)&0x1F)) && pkt[i+1] == byte(tsVideoPID&0xFF) {
			foundVideo = true
		}
		if pkt[i] == byte(0xE0|((tsAudioPID>>8)&0x1F)) && pkt[i+1] == byte(tsAudioPID&0xFF) {
			foundAudio = true
		}
	}
	if !foundVideo {
		t.Error("PMT should contain video PID")
	}
	if !foundAudio {
		t.Error("PMT should contain audio PID")
	}
}

// TestTSMuxerPES 验证 PES 封装: start code, stream_id, PTS。
func TestTSMuxerPES(t *testing.T) {
	m := NewTSMuxer()
	payload := make([]byte, 100) // 小 payload, 一个 TS 包即可
	for i := range payload {
		payload[i] = byte(i)
	}

	raw := m.MuxPES(tsVideoPID, tsStreamIDH264, payload, 90000, 90000, true)
	pkts := splitTSPackets(raw)

	// 所有包必须是 188 字节
	for i, p := range pkts {
		if len(p) != tsPacketSize {
			t.Fatalf("PES TS packet %d size = %d, want %d", i, len(p), tsPacketSize)
		}
		if p[0] != tsSyncByte {
			t.Errorf("PES TS packet %d sync byte = 0x%02X, want 0x%02X", i, p[0], tsSyncByte)
		}
	}

	// 第一个包应有 payload_unit_start_indicator
	if pkts[0][1]&0x40 == 0 {
		t.Error("first PES packet should have payload_unit_start_indicator set")
	}

	// PID 应为 videoPID
	pid := uint16(pkts[0][1]&0x1F)<<8 | uint16(pkts[0][2])
	if pid != tsVideoPID {
		t.Errorf("PES PID = %d, want %d", pid, tsVideoPID)
	}

	// 查找 PES start code (0x00 0x00 0x01) 在 payload 中
	// 第一个包: 4 header + 可能的 adaptation field, 然后是 PES
	pesStart := findPESStart(pkts[0])
	if pesStart < 0 {
		t.Fatal("PES start code not found in first packet")
	}
	if pkts[0][pesStart] != 0x00 || pkts[0][pesStart+1] != 0x00 || pkts[0][pesStart+2] != 0x01 {
		t.Fatal("PES start code prefix mismatch")
	}
	// stream_id = 0xE0 for video
	if pkts[0][pesStart+3] != tsStreamIDH264 {
		t.Errorf("PES stream_id = 0x%02X, want 0x%02X", pkts[0][pesStart+3], tsStreamIDH264)
	}

	// 验证 PTS: PTS_DTS_flags = 10 (PTS only), PES_header_data_length = 5
	// flags2 at pesStart+7, PES_header_data_length at pesStart+8
	flags2 := pkts[0][pesStart+7]
	if flags2&0xC0 != 0x80 {
		t.Errorf("PES PTS_DTS_flags = 0x%02X, want 0x80 (PTS only)", flags2&0xC0)
	}
	hdrLen := pkts[0][pesStart+8]
	if hdrLen != 5 {
		t.Errorf("PES header data length = %d, want 5", hdrLen)
	}

	// 解码 PTS 并验证
	ptsBytes := pkts[0][pesStart+9 : pesStart+14]
	decodedPTS := decodePTS(ptsBytes)
	if decodedPTS != 90000 {
		t.Errorf("decoded PTS = %d, want 90000", decodedPTS)
	}
}

// TestTSMuxerPacketSize 验证所有 TS 包严格 188 字节, 包括大 payload 分包。
func TestTSMuxerPacketSize(t *testing.T) {
	m := NewTSMuxer()
	// 大 payload 需要多个 TS 包
	payload := make([]byte, 5000)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	raw := m.MuxPES(tsVideoPID, tsStreamIDH264, payload, 0, 0, false)
	pkts := splitTSPackets(raw)
	if len(pkts) == 0 {
		t.Fatal("expected at least one TS packet")
	}
	for i, p := range pkts {
		if len(p) != tsPacketSize {
			t.Errorf("TS packet %d size = %d, want %d", i, len(p), tsPacketSize)
		}
	}
}

// TestTSMuxerContinuityCounter 验证连续帧的 continuity_counter 递增。
func TestTSMuxerContinuityCounter(t *testing.T) {
	m := NewTSMuxer()

	// 发送多个视频帧
	for i := 0; i < 5; i++ {
		frame := common.MediaFrame{
			Type:      common.FrameVideo,
			Codec:     common.CodecH264,
			Payload:   []byte{0, 0, 0, 1, byte(i)},
			Timestamp: uint32(i * 3000),
			Marker:    true,
		}
		_ = m.MuxFrame(frame)
	}

	// continuity counter 应该递增
	cc := m.cc[tsVideoPID]
	if cc == 0 {
		t.Error("continuity counter should have incremented after multiple frames")
	}
}

// TestTSMuxerMuxFrameKeyframePAT 验证关键帧前插入 PAT+PMT。
func TestTSMuxerMuxFrameKeyframePAT(t *testing.T) {
	m := NewTSMuxer()

	frame := common.MediaFrame{
		Type:      common.FrameVideo,
		Codec:     common.CodecH264,
		Payload:   []byte{0, 0, 0, 1, 0x67, 0x42, 0x00, 0x0a},
		Timestamp: 0,
		Marker:    true,
	}

	raw := m.MuxFrame(frame)
	pkts := splitTSPackets(raw)
	if len(pkts) < 3 {
		t.Fatalf("keyframe should produce at least 3 TS packets (PAT+PMT+PES), got %d", len(pkts))
	}

	// 第一个包应为 PAT (PID=0)
	pid0 := uint16(pkts[0][1]&0x1F)<<8 | uint16(pkts[0][2])
	if pid0 != tsPATPID {
		t.Errorf("first packet PID = %d, want PAT (0)", pid0)
	}
	// 第二个包应为 PMT
	pid1 := uint16(pkts[1][1]&0x1F)<<8 | uint16(pkts[1][2])
	if pid1 != tsPMTPID {
		t.Errorf("second packet PID = %d, want PMT (%d)", pid1, tsPMTPID)
	}
	// 第三个包应为 video PES
	pid2 := uint16(pkts[2][1]&0x1F)<<8 | uint16(pkts[2][2])
	if pid2 != tsVideoPID {
		t.Errorf("third packet PID = %d, want video (%d)", pid2, tsVideoPID)
	}
}

// ─── 辅助函数 ─────────────────────────────────────────────────────────────

// splitTSPackets 将扁平的 TS 字节流拆分为 188 字节的包切片。
func splitTSPackets(data []byte) [][]byte {
	var pkts [][]byte
	for i := 0; i+tsPacketSize <= len(data); i += tsPacketSize {
		pkt := make([]byte, tsPacketSize)
		copy(pkt, data[i:i+tsPacketSize])
		pkts = append(pkts, pkt)
	}
	return pkts
}

// tsPayloadOffset 返回 TS 包中 payload 的起始偏移 (跳过 header 和 adaptation field)。
func tsPayloadOffset(pkt []byte) int {
	pos := 4
	afc := (pkt[3] >> 4) & 0x03
	if afc == 0x02 || afc == 0x03 {
		afLen := int(pkt[4])
		pos += 1 + afLen
	}
	return pos
}

// findPESStart 在 TS 包中查找 PES start code (0x00 0x00 0x01) 的起始位置。
func findPESStart(pkt []byte) int {
	// 跳过 4 字节 header
	pos := 4
	// 检查 adaptation field
	afc := (pkt[3] >> 4) & 0x03
	if afc == 0x02 || afc == 0x03 {
		afLen := int(pkt[4])
		pos += 1 + afLen
	}
	// 查找 0x00 0x00 0x01
	for i := pos; i < len(pkt)-2; i++ {
		if pkt[i] == 0x00 && pkt[i+1] == 0x00 && pkt[i+2] == 0x01 {
			return i
		}
	}
	return -1
}

// decodePTS 从 5 字节 PTS 数据解码 PTS 值。
func decodePTS(data []byte) uint64 {
	pts := uint64(data[0]&0x0E) << 29
	pts |= uint64(data[1]) << 22
	pts |= uint64(data[2]&0xFE) << 14
	pts |= uint64(data[3]) << 7
	pts |= uint64(data[4]&0xFE) >> 1
	return pts
}
