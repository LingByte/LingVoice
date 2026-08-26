package srt

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// HandshakePacket SRT HSv5 握手包（control packet type=0x0000）。
//
// 布局（HSv5）:
//   [0-3]   header (control type=0, subtype=handshake type)
//   [4-7]   version (HSv5 = 5)
//   [8-11]  encryption field (0=不加密)
//   [12-15] extension field
//   [16-19] initial packet sequence number
//   [20-23] maximum transmission unit
//   [24-27] maximum flow window
//   [28-31] handshake type (induction/conclusion)
//   [32-35] SRT socket ID
//   [36-39] SYN cookie
//   [40-47] peer IP address
//   [48+]   extension data (stream ID 等)
const (
	hsVersionV5    = 5
	hsHeaderLen    = 48
	hsMtuDefault   = 1500
	hsFlowDefault  = 8192
)

// HandshakeType 握手阶段类型
type HandshakeType uint32

const (
	HsInduction  HandshakeType = 1
	HsConclusion HandshakeType = 0xFFFFFFFF
)

// HandshakePacket 握手包结构。
type HandshakePacket struct {
	Version        uint32
	Encryption     uint32
	Extension      uint32
	InitSeqNum     uint32
	MTU            uint32
	MaxFlowWindow  uint32
	HandshakeType  HandshakeType
	SocketID       uint32
	SynCookie      uint32
	PeerIP         [4]byte
	StreamID       string // SRTO_STREAMID（从 extension 提取）
}

// ErrHandshakeTooShort 握手包长度不足
var ErrHandshakeTooShort = errors.New("srt: handshake packet too short")

// ParseHandshake 从 control packet payload 解析握手包。
func ParseHandshake(payload []byte) (*HandshakePacket, error) {
	if len(payload) < hsHeaderLen {
		return nil, ErrHandshakeTooShort
	}
	hs := &HandshakePacket{
		Version:       binary.BigEndian.Uint32(payload[0:4]),
		Encryption:    binary.BigEndian.Uint32(payload[4:8]),
		Extension:     binary.BigEndian.Uint32(payload[8:12]),
		InitSeqNum:    binary.BigEndian.Uint32(payload[12:16]),
		MTU:           binary.BigEndian.Uint32(payload[16:20]),
		MaxFlowWindow: binary.BigEndian.Uint32(payload[20:24]),
		HandshakeType: HandshakeType(binary.BigEndian.Uint32(payload[24:28])),
		SocketID:      binary.BigEndian.Uint32(payload[28:32]),
		SynCookie:     binary.BigEndian.Uint32(payload[32:36]),
	}
	copy(hs.PeerIP[:], payload[36:40])

	// 解析 extension（stream ID 等）
	if len(payload) > hsHeaderLen {
		hs.StreamID = parseExtensions(payload[hsHeaderLen:])
	}
	return hs, nil
}

// parseExtensions 解析 HSv5 extension，提取 SRTO_STREAMID。
// Extension 格式：[2B type][2B length][data...]，type=5 表示 SRT_CMD_SID (stream ID)。
func parseExtensions(data []byte) string {
	off := 0
	for off+4 <= len(data) {
		extType := binary.BigEndian.Uint16(data[off : off+2])
		extLen := int(binary.BigEndian.Uint16(data[off+2:off+4])) * 4 // length in 4-byte words
		off += 4
		if off+extLen > len(data) {
			break
		}
		// SRT extension type 5 = SRT_CMD_SID (stream ID)
		if extType == 5 && extLen > 0 {
			sid := string(data[off : off+extLen])
			// 去掉尾部 \x00
			for i := len(sid) - 1; i >= 0; i-- {
				if sid[i] != 0 {
					sid = sid[:i+1]
					break
				}
			}
			return sid
		}
		off += extLen
	}
	return ""
}

// BuildHandshake 构造握手响应包（control packet payload）。
func BuildHandshake(hs *HandshakePacket) []byte {
	buf := make([]byte, hsHeaderLen)
	binary.BigEndian.PutUint32(buf[0:4], hs.Version)
	binary.BigEndian.PutUint32(buf[4:8], hs.Encryption)
	binary.BigEndian.PutUint32(buf[8:12], hs.Extension)
	binary.BigEndian.PutUint32(buf[12:16], hs.InitSeqNum)
	binary.BigEndian.PutUint32(buf[16:20], hs.MTU)
	binary.BigEndian.PutUint32(buf[20:24], hs.MaxFlowWindow)
	binary.BigEndian.PutUint32(buf[24:28], uint32(hs.HandshakeType))
	binary.BigEndian.PutUint32(buf[28:32], hs.SocketID)
	binary.BigEndian.PutUint32(buf[32:36], hs.SynCookie)
	copy(buf[36:40], hs.PeerIP[:])

	// 附加 stream ID extension（如果有）
	if hs.StreamID != "" {
		ext := buildStreamIDExtension(hs.StreamID)
		buf = append(buf, ext...)
	}
	return buf
}

// buildStreamIDExtension 构造 SRT_CMD_SID extension。
func buildStreamIDExtension(streamID string) []byte {
	// type=5, length in 4-byte words
	data := []byte(streamID)
	// pad to 4-byte boundary
	padded := (len(data) + 3) &^ 3
	words := padded / 4
	ext := make([]byte, 4+padded)
	binary.BigEndian.PutUint16(ext[0:2], 5) // SRT_CMD_SID
	binary.BigEndian.PutUint16(ext[2:4], uint16(words))
	copy(ext[4:], data)
	return ext
}

// BuildHandshakeResponse 构造握手响应（作为 control packet 完整帧）。
func BuildHandshakeResponse(hs *HandshakePacket, destSocketID uint32) []byte {
	payload := BuildHandshake(hs)
	return BuildControlPacket(ctrlHandshake, uint16(hs.HandshakeType), destSocketID, payload)
}

// ValidateHandshake 简单校验握手包。
func ValidateHandshake(hs *HandshakePacket) error {
	if hs.Version != hsVersionV5 && hs.Version != 0 {
		// induction 阶段 version 可能为 0
		return fmt.Errorf("srt: unsupported handshake version %d", hs.Version)
	}
	return nil
}
