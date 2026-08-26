// Package rtsp implements an RTSP server that receives pushed streams
// (RTSP source / ingest mode) and forwards RTP packets to the upper layer
// via the common.EventHandler / OnMediaFrame interface.
//
// 媒体传输：
//   - Interleaved TCP 模式：RTP 包通过 `$<channel><length><data>` 帧封装在 TCP 上
//   - 接收到的 RTP 包通过 OnMediaFrame 转发上层（上层再通过 rustbridge PushRtp 推给 Rust）
package rtsp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/pion/rtp"
)

// Interleaved frame 帧头：'$' + channel(1) + length(2 big-endian)
const (
	interleavedMagic   = '$'
	interleavedHdrLen  = 4
	maxInterleavedSize = 1 << 16 // 64 KiB
)

// ErrInterleavedTooShort interleaved 帧长度不足
var ErrInterleavedTooShort = errors.New("rtsp: interleaved frame too short")

// ErrInterleavedTooLarge interleaved 帧长度超限
var ErrInterleavedTooLarge = errors.New("rtsp: interleaved frame too large")

// Transport 管理 interleaved TCP 上的 RTP/RTCP 读写。
// channel 编号由 SETUP 协商：偶数=RTP，奇数=RTCP（成对分配）。
type Transport struct {
	mu       sync.Mutex
	rw       io.ReadWriter
	channels map[byte]*channelInfo // channel -> track 信息
	log      io.Writer // 调试日志输出（可选）
}

type channelInfo struct {
	trackID common.TrackID
	kind    common.TrackKind
	codec   common.CodecType
}

// NewTransport 创建 interleaved TCP 传输。
func NewTransport(rw io.ReadWriter) *Transport {
	return &Transport{
		rw:       rw,
		channels: make(map[byte]*channelInfo),
	}
}

// RegisterChannel 注册 channel 编号到轨道的映射。
func (t *Transport) RegisterChannel(channel byte, trackID common.TrackID, kind common.TrackKind, codec common.CodecType) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.channels[channel] = &channelInfo{trackID: trackID, kind: kind, codec: codec}
}

// ChannelInfo 返回 channel 对应的轨道信息。
func (t *Transport) ChannelInfo(channel byte) (common.TrackID, common.TrackKind, common.CodecType, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ci, ok := t.channels[channel]
	if !ok {
		return "", 0, 0, false
	}
	return ci.trackID, ci.kind, ci.codec, true
}

// WriteInterleaved 写入一个 interleaved 帧（用于发送 RTCP 等）。
func (t *Transport) WriteInterleaved(channel byte, data []byte) error {
	if len(data) > maxInterleavedSize {
		return ErrInterleavedTooLarge
	}
	hdr := [interleavedHdrLen]byte{
		interleavedMagic,
		channel,
		byte(len(data) >> 8),
		byte(len(data)),
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.rw.Write(hdr[:]); err != nil {
		return fmt.Errorf("rtsp: write interleaved header: %w", err)
	}
	if _, err := t.rw.Write(data); err != nil {
		return fmt.Errorf("rtsp: write interleaved payload: %w", err)
	}
	return nil
}

// ReadFrame 读取一个 interleaved 帧，返回 channel 和 payload。
// 如果读取到的不是 interleaved 帧（不是 '$' 开头），返回 ErrNotInterleaved，
// 调用方应回退到 RTSP 信令解析模式。
var ErrNotInterleaved = errors.New("rtsp: not an interleaved frame")

// ReadInterleaved 读取一个 interleaved 帧。
// 返回 channel 和 payload（payload 复用内部 buffer，调用方不应保留引用）。
func (t *Transport) ReadInterleaved() (byte, []byte, error) {
	var hdr [interleavedHdrLen]byte
	if _, err := io.ReadFull(t.rw, hdr[:]); err != nil {
		return 0, nil, err
	}
	if hdr[0] != interleavedMagic {
		return 0, nil, ErrNotInterleaved
	}
	channel := hdr[1]
	length := binary.BigEndian.Uint16(hdr[2:4])
	if length == 0 {
		return channel, nil, nil
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(t.rw, buf); err != nil {
		return 0, nil, fmt.Errorf("rtsp: read interleaved payload: %w", err)
	}
	return channel, buf, nil
}

// ParseRtpPacket 解析 RTP 包，返回 common.MediaFrame。
func ParseRtpPacket(data []byte) (rtp.Packet, error) {
	var pkt rtp.Packet
	if err := pkt.Unmarshal(data); err != nil {
		return rtp.Packet{}, fmt.Errorf("rtsp: parse rtp: %w", err)
	}
	return pkt, nil
}

// RtpToFrame 将 pion rtp.Packet 转换为 common.MediaFrame。
func RtpToFrame(pkt rtp.Packet, kind common.TrackKind, codec common.CodecType) common.MediaFrame {
	return common.MediaFrame{
		Type:       common.FrameTypeFromKind(kind),
		Codec:      codec,
		Payload:    pkt.Payload,
		Timestamp:  pkt.Timestamp,
		Sequence:   pkt.SequenceNumber,
		SSRC:       pkt.SSRC,
		Marker:     pkt.Marker,
		SampleRate: clockRateForCodec(codec),
	}
}

// clockRateForCodec 返回编解码的默认时钟频率。
func clockRateForCodec(codec common.CodecType) uint32 {
	switch codec {
	case common.CodecOpus:
		return 48000
	case common.CodecPCMU, common.CodecPCMA:
		return 8000
	case common.CodecH264:
		return 90000
	case common.CodecVP8, common.CodecVP9, common.CodecAV1:
		return 90000
	default:
		return 90000
	}
}
