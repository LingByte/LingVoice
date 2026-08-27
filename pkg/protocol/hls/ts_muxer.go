package hls

import (
	"github.com/LingByte/LingVoice/pkg/protocol/common"
)

// TS 包常量
const (
	tsPacketSize  = 188
	tsSyncByte    = 0x47
	tsPATPID      = 0x0000
	tsVideoPID    = 0x0100
	tsAudioPID    = 0x0101
	tsPMTPID      = 0x1000
	tsStreamIDH264 = 0xE0 // video stream
	tsStreamIDAAC  = 0xC0 // audio stream
)

// stream_type values for PMT
const (
	streamTypeH264 = 0x1B
	streamTypeAAC  = 0x0F
)

// TSMuxer 将 H.264 NAL 单元和 AAC 帧封装为 MPEG-TS 流。
//
// 每个 TS 包固定 188 字节:
//
//	4 字节 header (sync_byte + PID + flags + continuity_counter)
//	可选 adaptation_field (padding / PCR)
//	payload (PES 或 PSI 表)
type TSMuxer struct {
	patSent  bool
	pmtPID   uint16
	videoPID uint16
	audioPID uint16
	pcrPID   uint16
	cc       map[uint16]byte // continuity counters per PID
}

// NewTSMuxer 创建一个 TS muxer, 使用默认 PID 分配。
func NewTSMuxer() *TSMuxer {
	return &TSMuxer{
		pmtPID:   tsPMTPID,
		videoPID: tsVideoPID,
		audioPID: tsAudioPID,
		pcrPID:   tsVideoPID,
		cc:       make(map[uint16]byte),
	}
}

// MuxFrame 将一帧媒体数据封装为 TS 包。
// 关键帧 (Marker=true 的视频帧) 前会插入 PAT+PMT。
func (m *TSMuxer) MuxFrame(frame common.MediaFrame) []byte {
	var out []byte

	// 关键帧前插入 PAT+PMT
	if frame.Type == common.FrameVideo && frame.Marker {
		out = append(out, m.MuxPAT()...)
		out = append(out, m.MuxPMT()...)
		m.patSent = true
	}

	// 将 RTP timestamp 转换为 90kHz PTS (MPEG-TS 时钟)
	var pts uint64
	if frame.Type == common.FrameVideo {
		// H.264 RTP clock = 90kHz, timestamp 直接使用
		pts = uint64(frame.Timestamp)
	} else {
		// AAC RTP clock = sample rate, 需要转换到 90kHz
		if frame.SampleRate > 0 {
			pts = uint64(frame.Timestamp) * 90000 / uint64(frame.SampleRate)
		} else {
			pts = uint64(frame.Timestamp)
		}
	}

	pid := m.videoPID
	streamID := byte(tsStreamIDH264)
	if frame.Type == common.FrameAudio {
		pid = m.audioPID
		streamID = tsStreamIDAAC
	}

	out = append(out, m.MuxPES(pid, streamID, frame.Payload, pts, pts, frame.Marker)...)
	return out
}

// MuxPAT 生成 PAT (Program Association Table) TS 包。
// PID=0, 包含一个 program, 指向 PMT PID。
func (m *TSMuxer) MuxPAT() []byte {
	section := m.buildPATSection()
	// PSI 表前加 pointer_field (0x00 = 表紧随其后)
	payload := append([]byte{0x00}, section...)
	return m.stuffTS(tsPATPID, true, payload, false, 0)
}

// MuxPMT 生成 PMT (Program Map Table) TS 包。
// 包含 video PID 和 audio PID 的 elementary stream 描述。
func (m *TSMuxer) MuxPMT() []byte {
	section := m.buildPMTSection()
	payload := append([]byte{0x00}, section...)
	return m.stuffTS(m.pmtPID, true, payload, false, 0)
}

// MuxPES 将 elementary stream data 封装到 PES 包中, 再拆分为 TS 包。
// pts/dts 为 90kHz 时钟值。isKeyframe=true 时在第一个 TS 包中插入 PCR。
func (m *TSMuxer) MuxPES(pid uint16, streamID byte, payload []byte, pts uint64, dts uint64, isKeyframe bool) []byte {
	pesData := m.buildPESHeader(streamID, payload, pts, dts)
	pesData = append(pesData, payload...)

	var pcr uint64
	withPCR := false
	if isKeyframe {
		pcr = pts * 300 // PCR = PTS * 300 (90kHz -> 27MHz)
		withPCR = true
	}

	return m.stuffTS(pid, true, pesData, withPCR, pcr)
}

// ─── 内部方法 ─────────────────────────────────────────────────────────────

// nextCC 获取并递增指定 PID 的 continuity counter。
func (m *TSMuxer) nextCC(pid uint16) byte {
	cc := m.cc[pid]
	m.cc[pid] = (cc + 1) & 0x0F
	return cc
}

// stuffTS 将 payload 封装为若干 188 字节 TS 包。
// payloadStart=true 时第一个包设置 payload_unit_start_indicator。
// withPCR=true 时在第一个包的 adaptation field 中插入 PCR。
func (m *TSMuxer) stuffTS(pid uint16, payloadStart bool, payload []byte, withPCR bool, pcr uint64) []byte {
	var out []byte
	offset := 0

	for offset < len(payload) || (offset == 0 && len(payload) == 0) {
		packet := make([]byte, tsPacketSize)
		packet[0] = tsSyncByte

		// PID (13 bits) + payload_unit_start_indicator
		pidByte1 := byte((pid>>8)&0x1F) | 0x00 // transport_error=0, priority=0
		if payloadStart && offset == 0 {
			pidByte1 |= 0x40 // payload_unit_start_indicator
		}
		packet[1] = pidByte1
		packet[2] = byte(pid & 0xFF)

		remaining := len(payload) - offset

		// 构建 adaptation field 内容 (不含 length 字节)
		var afContent []byte
		if withPCR && offset == 0 {
			afContent = append(afContent, 0x10) // PCR flag
			afContent = append(afContent, pcrBytes(pcr)...)
		}

		afc := byte(0x01) // payload only (adaptation_field_control = 01)
		if remaining < 184 {
			// 需要用 adaptation field 填充
			if len(afContent) == 0 {
				afContent = []byte{0x00} // flags = 0, 纯 stuffing
			}
			stuffing := 183 - len(afContent) - remaining
			for i := 0; i < stuffing; i++ {
				afContent = append(afContent, 0xFF)
			}
			if remaining > 0 {
				afc = 0x03 // adaptation + payload
			} else {
				afc = 0x02 // adaptation only
			}
		} else if len(afContent) > 0 {
			// 有 PCR 但 payload >= 184, 仍需 adaptation field
			afc = 0x03
		}

		cc := m.nextCC(pid)
		packet[3] = (afc << 4) | (cc & 0x0F)

		pos := 4
		if len(afContent) > 0 {
			packet[pos] = byte(len(afContent)) // adaptation_field_length
			pos++
			copy(packet[pos:], afContent)
			pos += len(afContent)
		}

		// 填充 payload
		payloadLen := tsPacketSize - pos
		if payloadLen > remaining {
			payloadLen = remaining
		}
		if payloadLen > 0 {
			copy(packet[pos:], payload[offset:offset+payloadLen])
		}
		offset += payloadLen

		out = append(out, packet...)
	}

	return out
}

// buildPATSection 构建 PAT section (不含 TS 包头和 pointer_field)。
func (m *TSMuxer) buildPATSection() []byte {
	var s []byte
	s = append(s, 0x00)       // table_id = 0x00 (PAT)
	s = append(s, 0x00, 0x00) // section_length placeholder

	// transport_stream_id
	s = append(s, 0x00, 0x01)
	// reserved(2) + version(5) + current_next(1) = 0xC1
	s = append(s, 0xC1)
	// section_number, last_section_number
	s = append(s, 0x00, 0x00)

	// program_number = 1
	s = append(s, 0x00, 0x01)
	// reserved(3) + PMT_PID(13)
	s = append(s, byte(0xE0|((m.pmtPID>>8)&0x1F)), byte(m.pmtPID&0xFF))

	// section_length = (len(s) - 3) + 4(CRC)
	sectionLen := len(s) - 3 + 4
	s[1] = 0xB0 | byte((sectionLen>>8)&0x0F) // section_syntax_indicator=1, '0', reserved=11
	s[2] = byte(sectionLen & 0xFF)

	// CRC32
	crc := crc32MPEG2(s)
	s = append(s, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
	return s
}

// buildPMTSection 构建 PMT section。
func (m *TSMuxer) buildPMTSection() []byte {
	var s []byte
	s = append(s, 0x02)       // table_id = 0x02 (PMT)
	s = append(s, 0x00, 0x00) // section_length placeholder

	// program_number = 1
	s = append(s, 0x00, 0x01)
	// reserved(2) + version(5) + current_next(1)
	s = append(s, 0xC1)
	// section_number, last_section_number
	s = append(s, 0x00, 0x00)

	// reserved(3) + PCR_PID(13)
	s = append(s, byte(0xE0|((m.pcrPID>>8)&0x1F)), byte(m.pcrPID&0xFF))
	// reserved(4) + program_info_length(12) = 0
	s = append(s, 0xF0, 0x00)

	// elementary stream: video (H.264)
	s = append(s, streamTypeH264)
	s = append(s, byte(0xE0|((m.videoPID>>8)&0x1F)), byte(m.videoPID&0xFF))
	s = append(s, 0xF0, 0x00) // ES_info_length = 0

	// elementary stream: audio (AAC)
	s = append(s, streamTypeAAC)
	s = append(s, byte(0xE0|((m.audioPID>>8)&0x1F)), byte(m.audioPID&0xFF))
	s = append(s, 0xF0, 0x00) // ES_info_length = 0

	// section_length = (len(s) - 3) + 4(CRC)
	sectionLen := len(s) - 3 + 4
	s[1] = 0xB0 | byte((sectionLen>>8)&0x0F)
	s[2] = byte(sectionLen & 0xFF)

	// CRC32
	crc := crc32MPEG2(s)
	s = append(s, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
	return s
}

// buildPESHeader 构建 PES 包头 (不含 elementary stream data)。
func (m *TSMuxer) buildPESHeader(streamID byte, esData []byte, pts uint64, dts uint64) []byte {
	var pes []byte

	// start code prefix
	pes = append(pes, 0x00, 0x00, 0x01)
	// stream_id
	pes = append(pes, streamID)

	// PES_header_data (从 flags 到 ES data 前)
	var headerData []byte

	// PTS/DTS flags
	if pts == dts {
		// PTS only: PTS_DTS_flags = 10
		headerData = append(headerData, 0x80) // flags1: 10 + zeros
		headerData = append(headerData, 0x80) // flags2: PTS_DTS_flags=10 + zeros
		// PES_header_data_length = 5 (PTS only)
		headerData = append(headerData, 0x05)
		headerData = append(headerData, ptsBytes(pts, 0x21)...) // 0010 prefix + marker
	} else {
		// PTS + DTS: PTS_DTS_flags = 11
		headerData = append(headerData, 0x80) // flags1
		headerData = append(headerData, 0xC0) // flags2: PTS_DTS_flags=11
		// PES_header_data_length = 10 (PTS + DTS)
		headerData = append(headerData, 0x0A)
		headerData = append(headerData, ptsBytes(pts, 0x31)...)  // 0011 prefix
		headerData = append(headerData, ptsBytes(dts, 0x11)...) // 0001 prefix
	}

	// PES_packet_length: total PES data after this field
	// = PES_header_data_length(1 byte already in headerData) + ... actually:
	// PES_packet_length = len(headerData) + len(esData)
	// If > 65535, set to 0 (unbounded, allowed for video)
	pesPacketLen := len(headerData) + len(esData)
	if pesPacketLen > 0xFFFF {
		pesPacketLen = 0
	}
	pes = append(pes, byte(pesPacketLen>>8), byte(pesPacketLen&0xFF))

	// flags + PES_header_data
	pes = append(pes, headerData...)

	return pes
}

// ptsBytes 将 PTS 编码为 5 字节, prefix 为高 3 位 (如 0010=PTS only, 0011=PTS+DTS)。
func ptsBytes(pts uint64, prefix byte) []byte {
	return []byte{
		prefix | byte((pts>>29)&0x0E) | 0x01,
		byte((pts >> 22) & 0xFF),
		byte((pts>>14)&0xFF) | 0x01,
		byte((pts >> 7) & 0xFF),
		byte((pts<<1)&0xFE) | 0x01,
	}
}

// pcrBytes 将 PCR (27MHz 时钟) 编码为 6 字节。
func pcrBytes(pcr uint64) []byte {
	base := pcr / 300
	ext := pcr % 300
	return []byte{
		byte(base >> 25),
		byte(base >> 17),
		byte(base >> 9),
		byte(base >> 1),
		byte((base&1)<<7) | 0x7E | byte((ext>>8)&0x01),
		byte(ext & 0xFF),
	}
}

// crc32MPEG2 计算 CRC-32/MPEG-2 (poly=0x04C11DB7, init=0xFFFFFFFF, refin=false, refout=false, xorout=0)。
func crc32MPEG2(data []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	for _, b := range data {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
