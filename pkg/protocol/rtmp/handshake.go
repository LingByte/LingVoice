package rtmp

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// RTMP handshake 常量
const (
	rtmpVersion    byte   = 3
	handshakeSize  int    = 1536 // C1/S1/C2/S2 固定 1536 字节
	handshakeTime  uint32 = 0    // S1 time（0 表示服务器不关心）
)

// Handshake 执行 RTMP 服务端握手：
//
//	客户端 → 服务端: C0 (1 byte version) + C1 (1536 bytes)
//	服务端 → 客户端: S0 (1 byte version) + S1 (1536 bytes) + S2 (1536 bytes, echo C1)
//	客户端 → 服务端: C2 (1536 bytes, echo S1)
//
// 参考规范：RTMP 1.0 spec §5.3。
// 实现采用简化版（不校验 digest），兼容 ffmpeg/OBS 等标准客户端。
func Handshake(rw io.ReadWriter) error {
	// 1. 读 C0 + C1（客户端通常一起发送）
	c0 := make([]byte, 1)
	if _, err := io.ReadFull(rw, c0); err != nil {
		return fmt.Errorf("handshake: read C0: %w", err)
	}
	if c0[0] != rtmpVersion {
		return fmt.Errorf("handshake: unsupported version %d (expected %d)", c0[0], rtmpVersion)
	}

	c1 := make([]byte, handshakeSize)
	if _, err := io.ReadFull(rw, c1); err != nil {
		return fmt.Errorf("handshake: read C1: %w", err)
	}

	// 2. 构造 S0 + S1 + S2 并发送
	s0 := []byte{rtmpVersion}

	s1 := make([]byte, handshakeSize)
	// S1: time (4) + zero (4) + random (1528)
	binary.BigEndian.PutUint32(s1[0:4], handshakeTime)
	binary.BigEndian.PutUint32(s1[4:8], 0) // version zero
	if _, err := rand.Read(s1[8:]); err != nil {
		return fmt.Errorf("handshake: generate S1 random: %w", err)
	}

	// S2: echo C1（time + time2 + data）
	// S2[0:4] = C1[0:4] (client time), S2[4:8] = server time, S2[8:] = C1[8:]
	s2 := make([]byte, handshakeSize)
	copy(s2[0:4], c1[0:4])
	binary.BigEndian.PutUint32(s2[4:8], uint32(time.Now().UnixMilli()))
	copy(s2[8:], c1[8:])

	if _, err := rw.Write(append(append(s0, s1...), s2...)); err != nil {
		return fmt.Errorf("handshake: write S0+S1+S2: %w", err)
	}

	// 3. 读 C2（echo S1）
	c2 := make([]byte, handshakeSize)
	if _, err := io.ReadFull(rw, c2); err != nil {
		return fmt.Errorf("handshake: read C2: %w", err)
	}

	// 简化：不严格校验 C2 是否等于 S1（兼容性更好）
	return nil
}
