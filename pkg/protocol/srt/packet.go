// Package srt implements an SRT (Secure Reliable Transport) UDP server
// that receives pushed streams (ingest mode) and forwards media data
// (TS-over-SRT or RTP-over-SRT) to the upper layer via EventHandler.
//
// SRT 握手：
//   - Caller-Listener 模式（server 作为 listener，接收 caller 的连接）
//   - HSv5 握手协议
//   - SRTO_STREAMID 用于标识流
//
// 数据传输：
//   - SRT data packet：header(16B) + payload
//   - 接收 RTP/TS 包，提取媒体数据
//   - 通过 OnMediaFrame 转发上层
package srt

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// SRT 包类型（control packet 第 15 bit = 1）
const (
	packetTypeData    = false
	packetTypeControl = true
)

// Control packet 类型（高 15 bit of first 4 bytes）
const (
	ctrlHandshake     = 0x0000
	ctrlKeepalive     = 0x0001
	ctrlAck           = 0x0002
	ctrlNak           = 0x0003
	ctrlCongestionWarn = 0x0004
	ctrlShutdown      = 0x0005
	ctrlAck2          = 0x0006
	ctrlMessageDrop   = 0x0007
	ctrlUserDefined   = 0x7FFF
)

// Handshake 类型（HSv5）
const (
	hsTypeDone       = 0xFFFFFFFD
	hsTypeAgreement  = 0xFFFFFFFE
	hsTypeConclusion = 0xFFFFFFFF
	hsTypeWaveahand  = 0x00000000
	hsTypeInduction  = 0x00000001
	hsTypeRegular    = -1 // 1st pass
)

// SRT header 长度
const (
	headerLen = 16
)

// ErrPacketTooShort SRT 包长度不足
var ErrPacketTooShort = errors.New("srt: packet too short")

// Packet 表示一个 SRT 包（data 或 control）。
type Packet struct {
	IsControl bool
	// Control fields
	CtrlType  uint16
	Subtype   uint16
	// Data fields
	SeqNum    uint32
	// Common
	MsgNo     uint32 // control: type-specific; data: message number
	Timestamp uint32 // microseconds
	SocketID  uint32 // destination socket ID
	Payload   []byte
}

// ParsePacket 解析 SRT 包。
func ParsePacket(data []byte) (*Packet, error) {
	if len(data) < headerLen {
		return nil, ErrPacketTooShort
	}
	pkt := &Packet{}
	first := binary.BigEndian.Uint32(data[0:4])

	// 最高 bit 为 1 表示 control packet
	isControl := (first & 0x80000000) != 0
	pkt.IsControl = isControl

	if isControl {
		// control: [15bit type][16bit subtype][1]
		// 实际：bit31=1, bit30-16=type, bit15-0=subtype
		pkt.CtrlType = uint16((first >> 16) & 0x7FFF)
		pkt.Subtype = uint16(first & 0xFFFF)
	} else {
		// data: sequence number
		pkt.SeqNum = first & 0x7FFFFFFF
	}

	pkt.MsgNo = binary.BigEndian.Uint32(data[4:8])
	pkt.Timestamp = binary.BigEndian.Uint32(data[8:12])
	pkt.SocketID = binary.BigEndian.Uint32(data[12:16])
	pkt.Payload = data[headerLen:]
	return pkt, nil
}

// IsControlPacket 判断是否为 control packet。
func IsControlPacket(data []byte) bool {
	return len(data) >= 4 && (data[0]&0x80) != 0
}

// BuildControlPacket 构造一个 control packet。
func BuildControlPacket(ctrlType uint16, subtype uint16, socketID uint32, payload []byte) []byte {
	buf := make([]byte, headerLen+len(payload))
	first := uint32(0x80000000) | (uint32(ctrlType&0x7FFF) << 16) | uint32(subtype)
	binary.BigEndian.PutUint32(buf[0:4], first)
	// msgNo: 0 for most control
	binary.BigEndian.PutUint32(buf[4:8], 0)
	binary.BigEndian.PutUint32(buf[8:12], 0) // timestamp
	binary.BigEndian.PutUint32(buf[12:16], socketID)
	copy(buf[headerLen:], payload)
	return buf
}

// BuildDataPacket 构造一个 data packet。
func BuildDataPacket(seqNum uint32, msgNo uint32, timestamp uint32, socketID uint32, payload []byte) []byte {
	buf := make([]byte, headerLen+len(payload))
	binary.BigEndian.PutUint32(buf[0:4], seqNum&0x7FFFFFFF)
	binary.BigEndian.PutUint32(buf[4:8], msgNo)
	binary.BigEndian.PutUint32(buf[8:12], timestamp)
	binary.BigEndian.PutUint32(buf[12:16], socketID)
	copy(buf[headerLen:], payload)
	return buf
}

// BuildShutdown 构造 shutdown control packet。
func BuildShutdown(socketID uint32) []byte {
	return BuildControlPacket(ctrlShutdown, 0, socketID, nil)
}

// BuildKeepalive 构造 keepalive control packet。
func BuildKeepalive(socketID uint32) []byte {
	return BuildControlPacket(ctrlKeepalive, 0, socketID, nil)
}

// String 返回包的简短描述（调试用）。
func (p *Packet) String() string {
	if p.IsControl {
		return fmt.Sprintf("SRT[ctrl type=%d subtype=%d socket=%d payload=%d]",
			p.CtrlType, p.Subtype, p.SocketID, len(p.Payload))
	}
	return fmt.Sprintf("SRT[data seq=%d msg=%d ts=%d socket=%d payload=%d]",
		p.SeqNum, p.MsgNo, p.Timestamp, p.SocketID, len(p.Payload))
}
