package gb28181

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/pion/rtp"
	"go.uber.org/zap"
)

// ─── PS / PES 常量 ───────────────────────────────────────────────────────────

// PS 流起始码
var (
	psPackStartCode   = []byte{0x00, 0x00, 0x01, 0xBA} // PS pack header
	psSystemStartCode = []byte{0x00, 0x00, 0x01, 0xBB} // PS system header
	psMapStartCode    = []byte{0x00, 0x00, 0x01, 0xBC} // PSM
	pesVideoStartCode = []byte{0x00, 0x00, 0x01, 0xE0} // 视频流
	pesAudioStartCode = []byte{0x00, 0x00, 0x01, 0xC0} // 音频流
)

// PES stream id 范围
const (
	pesStreamIDVideoMin = 0xE0 // 0xE0-0xEF video
	pesStreamIDVideoMax = 0xEF
	pesStreamIDAudioMin = 0xC0 // 0xC0-0xDF audio
	pesStreamIDAudioMax = 0xDF
)

// H.264/H.265 Annex-B 起始码
var (
	annexBStartCode3 = []byte{0x00, 0x00, 0x01}
	annexBStartCode4 = []byte{0x00, 0x00, 0x00, 0x01}
)

// ─── PSReceiver ──────────────────────────────────────────────────────────────

// PSReceiver 在指定 UDP 端口接收设备发送的 PS 流，解析出 H.264 NALU /
// G.711 音频，封装为 RTP 包后通过 EventHandler 上报。
type PSReceiver struct {
	port      int
	conn      *net.UDPConn
	handler   common.EventHandler
	log       *zap.Logger
	sessionID string
	deviceID  string
	ssrc      uint32

	closed atomic.Bool

	// 媒体状态
	mu            sync.Mutex
	videoReported bool
	audioReported bool
	videoSeq      uint16
	audioSeq      uint16
	rtpSSRC       uint32

	// PES 重组缓冲（按 stream id 聚合跨包的 PES）
	pesBufMu sync.Mutex
	pesBuf   map[byte]*pesAssembler

	// 统计
	packetsRecv atomic.Uint64
	bytesRecv   atomic.Uint64
}

// pesAssembler 聚合一个 stream id 的 PES 数据。
type pesAssembler struct {
	streamID byte
	buf      []byte
}

// NewPSReceiver 创建 PS 接收器。port=0 表示由系统动态分配端口。
func NewPSReceiver(port int, sessionID, deviceID string, handler common.EventHandler, log *zap.Logger) *PSReceiver {
	if log == nil {
		log = zap.NewNop()
	}
	return &PSReceiver{
		port:      port,
		handler:   handler,
		log:       log.With(zap.String("component", "gb28181-ps-receiver")),
		sessionID: sessionID,
		deviceID:  deviceID,
		rtpSSRC:   uint32(time.Now().UnixNano() & 0xFFFFFFFF),
		pesBuf:    make(map[byte]*pesAssembler),
	}
}

// LocalPort 返回实际监听端口（Start 之后有效）。
func (r *PSReceiver) LocalPort() int {
	return r.port
}

// Start 启动 UDP 接收循环。
func (r *PSReceiver) Start() error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", r.port))
	if err != nil {
		return fmt.Errorf("gb28181 ps resolve addr: %w", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("gb28181 ps listen: %w", err)
	}
	r.conn = conn
	// 获取实际端口
	laddr := conn.LocalAddr().(*net.UDPAddr)
	r.port = laddr.Port
	r.log.Info("gb28181 ps receiver listening", zap.Int("port", r.port))
	go r.readLoop()
	return nil
}

// Close 关闭接收器。
func (r *PSReceiver) Close() error {
	r.closed.Store(true)
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

// readLoop 读取 UDP 数据包并解析 PS 流。
func (r *PSReceiver) readLoop() {
	buf := make([]byte, 65536)
	for {
		if r.closed.Load() {
			return
		}
		n, _, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			if r.closed.Load() {
				return
			}
			r.log.Error("gb28181 ps read", zap.Error(err))
			continue
		}
		r.packetsRecv.Add(1)
		r.bytesRecv.Add(uint64(n))
		r.processPacket(buf[:n])
	}
}

// processPacket 处理一个 UDP 包（可能包含 RTP 头 + PS 负载，或纯 PS）。
// GB28181 设备通常发送 RTP 封装的 PS 流（RTP payload type 96）。
func (r *PSReceiver) processPacket(data []byte) {
	// 尝试解析 RTP：RTP version=2
	var psPayload []byte
	var rtpTS uint32
	var rtpSeq uint16
	var rtpMarker bool

	pkt := &rtp.Packet{}
	if err := pkt.Unmarshal(data); err == nil && pkt.Version == 2 {
		psPayload = pkt.Payload
		rtpTS = pkt.Timestamp
		rtpSeq = pkt.SequenceNumber
		rtpMarker = pkt.Marker
	} else {
		// 非 RTP，当作裸 PS 流
		psPayload = data
	}

	if len(psPayload) == 0 {
		return
	}

	r.parsePSStream(psPayload, rtpTS, rtpSeq, rtpMarker)
}

// parsePSStream 解析 PS 流，提取 PES packet 并分发到视频/音频处理。
func (r *PSReceiver) parsePSStream(data []byte, rtpTS uint32, rtpSeq uint16, rtpMarker bool) {
	pos := 0
	for pos < len(data) {
		// 查找起始码 0x000001xx
		sc, _, ok := findStartCode(data, pos)
		if !ok {
			break
		}
		start := sc
		streamCode := data[start+3]

		switch {
		case streamCode == 0xBA: // PS pack header
			end, err := skipPackHeader(data, start)
			if err != nil {
				return
			}
			pos = end
		case streamCode == 0xBB: // system header
			end, err := skipSection(data, start)
			if err != nil {
				return
			}
			pos = end
		case streamCode == 0xBC: // PSM
			end, err := skipSection(data, start)
			if err != nil {
				return
			}
			pos = end
		case streamCode == 0xE0 || (streamCode >= pesStreamIDVideoMin && streamCode <= pesStreamIDVideoMax):
			// 视频 PES
			pes, end, err := parsePES(data, start)
			if err != nil {
				return
			}
			pos = end
			r.handleVideoPES(pes, rtpTS, rtpSeq, rtpMarker)
		case streamCode == 0xC0 || (streamCode >= pesStreamIDAudioMin && streamCode <= pesStreamIDAudioMax):
			// 音频 PES
			pes, end, err := parsePES(data, start)
			if err != nil {
				return
			}
			pos = end
			r.handleAudioPES(pes, rtpTS, rtpSeq, rtpMarker)
		case streamCode == 0xBE: // padding stream
			end, err := skipSection(data, start)
			if err != nil {
				return
			}
			pos = end
		default:
			// 未知起始码：尝试跳过该 section（按 PES 长度）
			end, err := skipSection(data, start)
			if err != nil {
				return
			}
			pos = end
		}
	}
}

// findStartCode 从 pos 开始查找 0x000001 起始码，返回起始位置与起始码字节。
func findStartCode(data []byte, pos int) (int, byte, bool) {
	for i := pos; i < len(data)-3; i++ {
		if data[i] == 0x00 && data[i+1] == 0x00 && data[i+2] == 0x01 {
			return i, data[i+3], true
		}
	}
	return 0, 0, false
}

// skipPackHeader 跳过 PS pack header (0x000001BA)。
// pack header 结构：起始码(4) + SCR/速率等(10) + stuffing length(3 bits)。
func skipPackHeader(data []byte, start int) (int, error) {
	// 至少 14 字节
	if start+14 > len(data) {
		return 0, fmt.Errorf("gb28181 ps: pack header too short")
	}
	// stuffing length 在第 13 字节低 3 位
	stuffingLen := int(data[start+13] & 0x07)
	end := start + 14 + stuffingLen
	if end > len(data) {
		return 0, fmt.Errorf("gb28181 ps: pack header stuffing overflow")
	}
	return end, nil
}

// skipSection 跳过带 2 字节 length 字段的 section（system header / PSM / PES）。
// 对于 PES（0xE0/0xC0），length=0 表示延续到包末尾。
func skipSection(data []byte, start int) (int, error) {
	if start+6 > len(data) {
		return 0, fmt.Errorf("gb28181 ps: section too short")
	}
	length := int(binary.BigEndian.Uint16(data[start+4 : start+6]))
	end := start + 6 + length
	if length == 0 {
		// 延续到末尾
		return len(data), nil
	}
	if end > len(data) {
		return len(data), nil
	}
	return end, nil
}

// pesPacket 解析后的 PES packet。
type pesPacket struct {
	StreamID   byte
	PayloadLen int   // PES packet length 字段（可能为 0）
	PTS        int64 // 90kHz 时钟
	DTS        int64 // 90kHz 时钟
	HasPTS     bool
	HasDTS     bool
	Payload    []byte // PES payload（去掉可选头后的数据）
}

// parsePES 解析 PES packet（起始码 0x000001E0/0xC0 等）。
func parsePES(data []byte, start int) (*pesPacket, int, error) {
	if start+9 > len(data) {
		return nil, 0, fmt.Errorf("gb28181 pes: header too short")
	}
	streamID := data[start+3]
	length := int(binary.BigEndian.Uint16(data[start+4 : start+6]))
	pes := &pesPacket{
		StreamID:   streamID,
		PayloadLen: length,
	}
	// PES header flags
	flags1 := data[start+7]
	flags2 := data[start+8]
	headerDataLen := int(data[start+8]) // PES_header_data_length（第 9 字节）

	// 注意：PES header data length 在 start+8
	_ = flags1
	pesHeaderDataLen := int(data[start+8])
	_ = flags2
	_ = headerDataLen

	// 修正：PES_header_data_length 在第 9 字节（index start+8）
	optStart := start + 9
	if optStart+pesHeaderDataLen > len(data) {
		// 数据不足，按可用处理
		pesHeaderDataLen = 0
	}

	// 解析 PTS/DTS（在 optional header 内）
	if pesHeaderDataLen >= 5 && (flags2&0xC0) == 0x80 {
		// 只有 PTS
		pes.PTS = parsePTS(data[optStart:])
		pes.HasPTS = true
	} else if pesHeaderDataLen >= 10 && (flags2&0xC0) == 0xC0 {
		// PTS + DTS
		pes.PTS = parsePTS(data[optStart:])
		pes.DTS = parsePTS(data[optStart+5:])
		pes.HasPTS = true
		pes.HasDTS = true
	}

	payloadStart := optStart + pesHeaderDataLen
	// PES packet length=0 表示延续到末尾（视频常见）
	var payloadEnd int
	if length == 0 {
		payloadEnd = len(data)
	} else {
		payloadEnd = start + 6 + length
	}
	if payloadEnd > len(data) {
		payloadEnd = len(data)
	}
	if payloadStart > payloadEnd {
		payloadStart = payloadEnd
	}
	pes.Payload = data[payloadStart:payloadEnd]
	return pes, payloadEnd, nil
}

// parsePTS 从 5 字节解析 33 位 PTS（90kHz）。
func parsePTS(b []byte) int64 {
	if len(b) < 5 {
		return 0
	}
	// PTS[32..30] | marker | PTS[29..15] | marker | PTS[14..0] | marker
	pts := int64(b[0]&0x0E) << 29
	pts |= int64(b[1]) << 22
	pts |= int64(b[2]&0xFE) << 14
	pts |= int64(b[3]) << 7
	pts |= int64(b[4]&0xFE) >> 1
	return pts
}

// ─── 视频 / 音频处理 ─────────────────────────────────────────────────────────

// handleVideoPES 处理视频 PES payload（Annex-B H.264/H.265 NALU）。
func (r *PSReceiver) handleVideoPES(pes *pesPacket, rtpTS uint32, rtpSeq uint16, rtpMarker bool) {
	if len(pes.Payload) == 0 {
		return
	}
	// 首次上报 video track
	r.reportVideoTrack()

	nalus := splitAnnexB(pes.Payload)
	for i, nal := range nalus {
		if len(nal) == 0 {
			continue
		}
		marker := rtpMarker && i == len(nalus)-1
		frame := common.MediaFrame{
			Type:       common.FrameVideo,
			Codec:      common.CodecH264,
			Payload:    nal,
			Timestamp:  rtpTS,
			Sequence:   r.videoSeq,
			SampleRate: 90000,
			SSRC:       r.rtpSSRC,
			Marker:     marker,
		}
		r.videoSeq++
		if err := r.handler.OnMediaFrame(r.sessionID, "video", frame); err != nil {
			r.log.Debug("gb28181 forward video frame failed", zap.Error(err))
		}
	}
}

// handleAudioPES 处理音频 PES payload（G.711）。
func (r *PSReceiver) handleAudioPES(pes *pesPacket, rtpTS uint32, rtpSeq uint16, rtpMarker bool) {
	if len(pes.Payload) == 0 {
		return
	}
	r.reportAudioTrack()

	frame := common.MediaFrame{
		Type:       common.FrameAudio,
		Codec:      common.CodecG711U, // GB28181 默认 G.711U
		Payload:    pes.Payload,
		Timestamp:  rtpTS,
		Sequence:   r.audioSeq,
		SampleRate: 8000,
		Channels:   1,
		SSRC:       r.rtpSSRC,
		Marker:     true,
	}
	r.audioSeq++
	if err := r.handler.OnMediaFrame(r.sessionID, "audio", frame); err != nil {
		r.log.Debug("gb28181 forward audio frame failed", zap.Error(err))
	}
}

// reportVideoTrack 首次上报视频轨道就绪。
func (r *PSReceiver) reportVideoTrack() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.videoReported {
		return
	}
	r.videoReported = true
	r.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventTrackAdded,
		Protocol:  common.ProtocolGB28181,
		SessionID: r.sessionID,
		Track: &common.TrackInfo{
			ID:         "video",
			Kind:       common.TrackVideo,
			Direction:  common.TrackRecv,
			Codec:      common.CodecH264,
			SampleRate: 90000,
			StreamID:   r.deviceID,
		},
		Timestamp: time.Now(),
	})
}

// reportAudioTrack 首次上报音频轨道就绪。
func (r *PSReceiver) reportAudioTrack() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.audioReported {
		return
	}
	r.audioReported = true
	r.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventTrackAdded,
		Protocol:  common.ProtocolGB28181,
		SessionID: r.sessionID,
		Track: &common.TrackInfo{
			ID:         "audio",
			Kind:       common.TrackAudio,
			Direction:  common.TrackRecv,
			Codec:      common.CodecG711U,
			SampleRate: 8000,
			Channels:   1,
			StreamID:   r.deviceID,
		},
		Timestamp: time.Now(),
	})
}

// splitAnnexB 将 Annex-B 字节流拆分为 NALU 列表（去掉起始码）。
func splitAnnexB(data []byte) [][]byte {
	var nalus [][]byte
	pos := 0
	for pos < len(data) {
		// 查找起始码（3 或 4 字节）
		scLen := 0
		if pos+4 <= len(data) && data[pos] == 0 && data[pos+1] == 0 && data[pos+2] == 0 && data[pos+3] == 1 {
			scLen = 4
		} else if pos+3 <= len(data) && data[pos] == 0 && data[pos+1] == 0 && data[pos+2] == 1 {
			scLen = 3
		} else {
			pos++
			continue
		}
		nalStart := pos + scLen
		// 查找下一个起始码
		next := -1
		for i := nalStart + 1; i < len(data)-3; i++ {
			if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
				next = i
				break
			}
		}
		var nal []byte
		if next >= 0 {
			nal = data[nalStart:next]
			pos = next
		} else {
			nal = data[nalStart:]
			pos = len(data)
		}
		nalus = append(nalus, nal)
	}
	return nalus
}

// NaluType 返回 H.264 NALU 类型（低 5 位）。
func NaluType(nal []byte) byte {
	if len(nal) == 0 {
		return 0
	}
	return nal[0] & 0x1F
}

// NaluTypeName 返回 H.264 NALU 类型名称。
func NaluTypeName(nal []byte) string {
	switch NaluType(nal) {
	case 1:
		return "NonIDR"
	case 5:
		return "IDR"
	case 6:
		return "SEI"
	case 7:
		return "SPS"
	case 8:
		return "PPS"
	default:
		return fmt.Sprintf("type=%d", NaluType(nal))
	}
}
