package srt

// MPEG-TS / PES / PAT / PMT 解析。
//
// SRT 推流通常承载 MPEG-TS 流（188 字节定长 TS 包）。
// 本文件负责：
//   - 从 SRT data payload 中切分 188 字节 TS 包
//   - 解析 PAT（PID=0）获取 PMT PID
//   - 解析 PMT 获取 video/audio elementary stream PID 及 codec
//   - 按 PID 重组 PES，提取 H.264 NAL 单元 / AAC / G.711 音频帧
//   - 通过 handler.OnMediaFrame 向上层转发 common.MediaFrame

import (
	"encoding/binary"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
)

const (
	tsPacketSize = 188
	tsSyncByte   = 0x47

	pidPAT = 0x0000

	// adaptation_field_control
	afcReserved      = 0x00
	afcPayloadOnly   = 0x01
	afcAdaptationOnly = 0x02
	afcBoth          = 0x03

	// MPEG-TS stream_type
	streamTypeMPEG2Video   = 0x02
	streamTypeMPEG2Audio   = 0x04
	streamTypeH264         = 0x1B
	streamTypeH265         = 0x24
	streamTypeAACADTS      = 0x0F
	streamTypeG711U        = 0x90 // 非标准，部分实现使用
)

// tsStreamInfo 跟踪单个 PID 的 PES 重组状态。
type tsStreamInfo struct {
	pid       uint16
	streamType byte
	codec     common.CodecType
	kind      common.TrackKind
	pesBuffer []byte    // 累积的 PES payload（去掉 PES header 后的 elementary stream）
	pesTS     uint32    // PES PTS（简化：暂未使用）
	hasPayload bool
}

// tsParser 维护一个会话内的 TS 解复用状态。
type tsParser struct {
	pmtPID    uint16
	videoPID  uint16
	audioPID  uint16
	videoCodec common.CodecType
	audioCodec common.CodecType
	streams   map[uint16]*tsStreamInfo
}

func newTSParser() *tsParser {
	return &tsParser{streams: make(map[uint16]*tsStreamInfo)}
}

// codecFromStreamType 根据 MPEG-TS stream_type 推断 codec 与轨道类型。
func codecFromStreamType(st byte) (common.CodecType, common.TrackKind) {
	switch st {
	case streamTypeH264:
		return common.CodecH264, common.TrackVideo
	case streamTypeH265:
		return common.CodecH265, common.TrackVideo
	case streamTypeMPEG2Video:
		return common.CodecH264, common.TrackVideo // 简化：当作视频
	case streamTypeAACADTS:
		return common.CodecAAC, common.TrackAudio
	case streamTypeMPEG2Audio:
		return common.CodecAAC, common.TrackAudio
	case streamTypeG711U:
		return common.CodecG711U, common.TrackAudio
	default:
		return 0, 0
	}
}

// tsPacket 表示解析后的单个 TS 包字段。
type tsPacket struct {
	pid                   uint16
	payloadUnitStart      bool
	transportError        bool
	adaptationFieldControl byte
	continuityCounter     byte
	payload               []byte
}

// parseTSPacket 解析单个 188 字节 TS 包。
func parseTSPacket(buf []byte) (*tsPacket, bool) {
	if len(buf) < tsPacketSize || buf[0] != tsSyncByte {
		return nil, false
	}
	p := &tsPacket{
		transportError:        (buf[1] & 0x80) != 0,
		payloadUnitStart:      (buf[1] & 0x40) != 0,
		pid:                   uint16(buf[1]&0x1F)<<8 | uint16(buf[2]),
		adaptationFieldControl: (buf[3] >> 4) & 0x03,
		continuityCounter:     buf[3] & 0x0F,
	}
	off := 4
	// adaptation field 存在时跳过
	if p.adaptationFieldControl == afcAdaptationOnly || p.adaptationFieldControl == afcBoth {
		if off >= len(buf) {
			return p, true
		}
		afLen := int(buf[off])
		off += 1 + afLen
		if off > len(buf) {
			off = len(buf)
		}
	}
	// payload 存在时提取
	if p.adaptationFieldControl == afcPayloadOnly || p.adaptationFieldControl == afcBoth {
		p.payload = buf[off:tsPacketSize]
	}
	return p, true
}

// feedTSData 累积 SRT data payload，切分完整 TS 包并解复用媒体。
// 该方法在 Session 上调用，提取的媒体帧通过 handler.OnMediaFrame 转发上层。
func (s *Session) feedTSData(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tsBuffer = append(s.tsBuffer, data...)

	for len(s.tsBuffer) >= tsPacketSize {
		// 寻找同步字节
		idx := -1
		for i := 0; i+tsPacketSize <= len(s.tsBuffer); i++ {
			if s.tsBuffer[i] == tsSyncByte {
				idx = i
				break
			}
		}
		if idx < 0 {
			// 没有同步字节，保留最后一个字节（可能是下个同步字节的高位）
			if len(s.tsBuffer) > 1 {
				s.tsBuffer = s.tsBuffer[len(s.tsBuffer)-1:]
			}
			return
		}
		// 丢弃同步字节之前的残数据
		if idx > 0 {
			s.tsBuffer = s.tsBuffer[idx:]
		}
		if len(s.tsBuffer) < tsPacketSize {
			return
		}
		pkt, ok := parseTSPacket(s.tsBuffer[:tsPacketSize])
		s.tsBuffer = s.tsBuffer[tsPacketSize:]
		if !ok {
			continue
		}
		s.tsPackets++
		if pkt.transportError || len(pkt.payload) == 0 {
			continue
		}
		s.processTSPacket(pkt)
	}
}

// processTSPacket 按类型处理单个 TS 包（PAT/PMT/PES）。
func (s *Session) processTSPacket(pkt *tsPacket) {
	switch {
	case pkt.pid == pidPAT:
		s.parsePAT(pkt)
	case s.tsParser.pmtPID != 0 && pkt.pid == s.tsParser.pmtPID:
		s.parsePMT(pkt)
	default:
		s.processPES(pkt)
	}
}

// parsePAT 解析 PAT 表，提取 PMT PID。
func (s *Session) parsePAT(pkt *tsPacket) {
	if !pkt.payloadUnitStart || len(pkt.payload) < 1 {
		return
	}
	// pointer_field（1 字节）+ section 数据
	pointer := int(pkt.payload[0])
	off := 1 + pointer
	if off+8 > len(pkt.payload) {
		return
	}
	sec := pkt.payload[off:]
	// table_id(1) section_syntax_indicator+0+reserved+section_length(2)
	sectionLen := int(binary.BigEndian.Uint16(sec[1:3])) & 0x0FFF
	if 3+sectionLen > len(sec) {
		return
	}
	// transport_stream_id(2) reserved+version+current_next(1) section_number(1) last_section_number(1)
	// 之后是 4 字节一组的 program entries，末尾 4 字节 CRC
	body := sec[8 : 3+sectionLen-4]
	for i := 0; i+4 <= len(body); i += 4 {
		programNum := binary.BigEndian.Uint16(body[i : i+2])
		pmtPID := uint16(binary.BigEndian.Uint16(body[i+2:i+4])) & 0x1FFF
		if programNum != 0 && pmtPID != 0 {
			s.tsParser.pmtPID = pmtPID
		}
	}
}

// parsePMT 解析 PMT 表，提取 video/audio elementary stream PID。
func (s *Session) parsePMT(pkt *tsPacket) {
	if !pkt.payloadUnitStart || len(pkt.payload) < 1 {
		return
	}
	pointer := int(pkt.payload[0])
	off := 1 + pointer
	if off+12 > len(pkt.payload) {
		return
	}
	sec := pkt.payload[off:]
	sectionLen := int(binary.BigEndian.Uint16(sec[1:3])) & 0x0FFF
	if 3+sectionLen > len(sec) {
		return
	}
	// program_number(2) reserved+version+current_next(1) section_number(1) last_section_number(1)
	// reserved(3bits)+PCR_PID(13bits) reserved(4bits)+program_info_length(12bits)
	programInfoLen := int(binary.BigEndian.Uint16(sec[10:12])) & 0x0FFF
	esStart := 12 + programInfoLen
	body := sec[esStart : 3+sectionLen-4]
	for i := 0; i+5 <= len(body); {
		streamType := body[i]
		esPID := uint16(binary.BigEndian.Uint16(body[i+1:i+3])) & 0x1FFF
		esInfoLen := int(binary.BigEndian.Uint16(body[i+3:i+5])) & 0x0FFF
		i += 5 + esInfoLen

		codec, kind := codecFromStreamType(streamType)
		if codec == 0 {
			continue
		}
		// 注册 stream
		if _, ok := s.tsParser.streams[esPID]; !ok {
			s.tsParser.streams[esPID] = &tsStreamInfo{
				pid:        esPID,
				streamType: streamType,
				codec:      codec,
				kind:       kind,
			}
		}
		if kind == common.TrackVideo && s.tsParser.videoPID == 0 {
			s.tsParser.videoPID = esPID
			s.tsParser.videoCodec = codec
		} else if kind == common.TrackAudio && s.tsParser.audioPID == 0 {
			s.tsParser.audioPID = esPID
			s.tsParser.audioCodec = codec
		}
	}
}

// processPES 处理 PES payload（按 PID 重组，提取 elementary stream）。
func (s *Session) processPES(pkt *tsPacket) {
	info, ok := s.tsParser.streams[pkt.pid]
	if !ok {
		return
	}

	if pkt.payloadUnitStart {
		// 新 PES 开始：先 flush 旧的 PES
		if len(info.pesBuffer) > 0 {
			s.flushPES(info)
		}
		info.pesBuffer = info.pesBuffer[:0]
		info.hasPayload = false
		// 解析 PES header，提取 elementary stream payload
		es := parsePESHeader(pkt.payload)
		if es != nil {
			info.pesBuffer = append(info.pesBuffer, es...)
			info.hasPayload = true
		}
	} else {
		// 续接 PES payload
		info.pesBuffer = append(info.pesBuffer, pkt.payload...)
	}
}

// parsePESHeader 解析 PES header，返回去掉 header 后的 elementary stream payload。
// PES 结构：start_code_prefix(3) stream_id(1) PES_packet_length(2) flags(2) PES_header_data_length(1) [optional fields] payload
func parsePESHeader(payload []byte) []byte {
	if len(payload) < 9 {
		return nil
	}
	// start code prefix 00 00 01
	if payload[0] != 0x00 || payload[1] != 0x00 || payload[2] != 0x01 {
		return nil
	}
	// stream_id = payload[3]
	// PES_packet_length = payload[4:6]（可为 0，视频 PES 常为 0）
	headerDataLen := int(payload[8])
	off := 9 + headerDataLen
	if off > len(payload) {
		return nil
	}
	return payload[off:]
}

// flushPES 处理已重组完成的 PES，提取 NAL 单元 / 音频帧并转发上层。
func (s *Session) flushPES(info *tsStreamInfo) {
	if len(info.pesBuffer) == 0 || s.handler == nil {
		return
	}
	trackID := common.TrackID("video")
	if info.kind == common.TrackAudio {
		trackID = "audio"
	}

	switch info.codec {
	case common.CodecH264, common.CodecH265:
		nals := splitNALUnits(info.pesBuffer)
		for _, nal := range nals {
			if len(nal) == 0 {
				continue
			}
			s.handler.OnMediaFrame(s.id, trackID, common.MediaFrame{
				Type:    common.FrameVideo,
				Codec:   info.codec,
				Payload: nal,
				Marker:  isKeyFrameNAL(info.codec, nal),
			})
		}
	case common.CodecAAC:
		// AAC ADTS 帧直接转发（上层负责解 ADTS）
		s.handler.OnMediaFrame(s.id, trackID, common.MediaFrame{
			Type:    common.FrameAudio,
			Codec:   common.CodecAAC,
			Payload: info.pesBuffer,
		})
	case common.CodecG711U, common.CodecG711A, common.CodecPCMU, common.CodecPCMA:
		s.handler.OnMediaFrame(s.id, trackID, common.MediaFrame{
			Type:    common.FrameAudio,
			Codec:   info.codec,
			Payload: info.pesBuffer,
		})
	default:
		s.handler.OnMediaFrame(s.id, trackID, common.MediaFrame{
			Type:    common.FrameTypeFromKind(info.kind),
			Codec:   info.codec,
			Payload: info.pesBuffer,
		})
	}
}

// splitNALUnits 按 Annex B start code (00 00 00 01 / 00 00 01) 切分 NAL 单元。
func splitNALUnits(data []byte) [][]byte {
	var nals [][]byte
	start := -1
	i := 0
	for i < len(data) {
		// 3 字节 start code: 00 00 01
		if i+2 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 0x01 {
			if start >= 0 {
				nals = append(nals, data[start:i])
			}
			start = i + 3
			i += 3
			continue
		}
		// 4 字节 start code: 00 00 00 01
		if i+3 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 0x01 {
			if start >= 0 {
				nals = append(nals, data[start:i])
			}
			start = i + 4
			i += 4
			continue
		}
		i++
	}
	if start >= 0 && start < len(data) {
		nals = append(nals, data[start:])
	}
	return nals
}

// isKeyFrameNAL 判断 NAL 单元是否为关键帧（IDR / SPS / VPS）。
func isKeyFrameNAL(codec common.CodecType, nal []byte) bool {
	if len(nal) == 0 {
		return false
	}
	switch codec {
	case common.CodecH264:
		nalType := nal[0] & 0x1F
		// 5 = IDR slice
		return nalType == 5
	case common.CodecH265:
		nalType := (nal[0] >> 1) & 0x3F
		// 16..21 = BLA/IDR/CRA
		return nalType >= 16 && nalType <= 21
	}
	return false
}
