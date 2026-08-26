package rtmp

import (
	"encoding/binary"
	"fmt"
	"io"
)

// ChunkReader 从 io.Reader 读取 RTMP chunk stream 并重组为完整 Message。
//
// chunk 格式（RTMP spec §6）：
//
//	+---------------+----------------+--------------------+--------------+
//	| Basic Header  | Message Header | Extended Timestamp |  Chunk Data  |
//	+---------------+----------------+--------------------+--------------+
//
// Basic Header: fmt(2 bit) + csid(6 bit)
//   - csid 0    → 2 字节，csid = byte2 + 64
//   - csid 1    → 3 字节，csid = (byte3<<8) + byte2 + 64
//   - csid 2    → 保留（协议控制消息）
//   - csid 3-63 → 1 字节
//
// Message Header（按 fmt）：
//   - fmt 0: 11 字节 timestamp(3) + length(3) + type(1) + streamID(4 LE)
//   - fmt 1: 7 字节  timestampDelta(3) + length(3) + type(1)
//   - fmt 2: 3 字节  timestampDelta(3)
//   - fmt 3: 0 字节  （continuation）
//
// Extended Timestamp: 当 timestamp/delta == 0xFFFFFF 时，后跟 4 字节完整时间戳。
type ChunkReader struct {
	r        io.Reader
	chunkSize int

	// per-CSID 重组状态
	states map[uint32]*chunkState
}

type chunkState struct {
	// 当前正在重组的 message
	timestamp      uint32
	timestampDelta uint32
	hasDelta       bool
	msgType        uint8
	msgLength      uint32
	streamID       uint32
	payload        []byte
	bytesRead      int
	// 是否在第一个 chunk（用于 fmt 0/1/2 初始化）
	newMessage     bool
}

// NewChunkReader 创建 chunk reader，默认 chunk size 128。
func NewChunkReader(r io.Reader) *ChunkReader {
	return &ChunkReader{
		r:         r,
		chunkSize: defaultChunkSize,
		states:    make(map[uint32]*chunkState),
	}
}

// SetChunkSize 更新 chunk size（收到 Set Chunk Size 控制消息后调用）。
func (cr *ChunkReader) SetChunkSize(size int) {
	if size > 0 {
		cr.chunkSize = size
	}
}

// ReadMessage 读取并重组一个完整的 RTMP message。
// 阻塞直到一个 message 重组完成或发生错误。
func (cr *ChunkReader) ReadMessage() (*Message, error) {
	for {
		// 1. 读 basic header
		fmt_, csid, err := cr.readBasicHeader()
		if err != nil {
			return nil, fmt.Errorf("chunk: read basic header: %w", err)
		}

		st := cr.getState(csid)

		// 2. 读 message header（按 fmt）
		switch fmt_ {
		case 0:
			hdr := make([]byte, 11)
			if _, err := io.ReadFull(cr.r, hdr); err != nil {
				return nil, fmt.Errorf("chunk: read msg header fmt0: %w", err)
			}
			ts := uint32(hdr[0])<<16 | uint32(hdr[1])<<8 | uint32(hdr[2])
			length := uint32(hdr[3])<<16 | uint32(hdr[4])<<8 | uint32(hdr[5])
			mtype := hdr[6]
			streamID := binary.LittleEndian.Uint32(hdr[7:11])
			// extended timestamp
			if ts == 0xFFFFFF {
				ext := make([]byte, 4)
				if _, err := io.ReadFull(cr.r, ext); err != nil {
					return nil, fmt.Errorf("chunk: read ext timestamp fmt0: %w", err)
				}
				ts = binary.BigEndian.Uint32(ext)
			}
			st.timestamp = ts
			st.timestampDelta = 0
			st.hasDelta = false
			st.msgType = mtype
			st.msgLength = length
			st.streamID = streamID
			st.payload = make([]byte, length)
			st.bytesRead = 0
			st.newMessage = true

		case 1:
			hdr := make([]byte, 7)
			if _, err := io.ReadFull(cr.r, hdr); err != nil {
				return nil, fmt.Errorf("chunk: read msg header fmt1: %w", err)
			}
			delta := uint32(hdr[0])<<16 | uint32(hdr[1])<<8 | uint32(hdr[2])
			length := uint32(hdr[3])<<16 | uint32(hdr[4])<<8 | uint32(hdr[5])
			mtype := hdr[6]
			if delta == 0xFFFFFF {
				ext := make([]byte, 4)
				if _, err := io.ReadFull(cr.r, ext); err != nil {
					return nil, fmt.Errorf("chunk: read ext timestamp fmt1: %w", err)
				}
				delta = binary.BigEndian.Uint32(ext)
			}
			if st.newMessage {
				st.timestampDelta = delta
				st.timestamp = delta
			} else {
				st.timestamp += delta
			}
			st.hasDelta = true
			st.msgType = mtype
			st.msgLength = length
			st.payload = make([]byte, length)
			st.bytesRead = 0
			st.newMessage = true

		case 2:
			hdr := make([]byte, 3)
			if _, err := io.ReadFull(cr.r, hdr); err != nil {
				return nil, fmt.Errorf("chunk: read msg header fmt2: %w", err)
			}
			delta := uint32(hdr[0])<<16 | uint32(hdr[1])<<8 | uint32(hdr[2])
			if delta == 0xFFFFFF {
				ext := make([]byte, 4)
				if _, err := io.ReadFull(cr.r, ext); err != nil {
					return nil, fmt.Errorf("chunk: read ext timestamp fmt2: %w", err)
				}
				delta = binary.BigEndian.Uint32(ext)
			}
			if st.newMessage {
				st.timestampDelta = delta
				st.timestamp = delta
			} else {
				st.timestamp += delta
			}
			st.hasDelta = true
			// length/type/streamID 沿用上一个 message
			if st.msgLength == 0 {
				return nil, fmt.Errorf("chunk: fmt2 but no previous message header")
			}
			st.payload = make([]byte, st.msgLength)
			st.bytesRead = 0
			st.newMessage = true

		case 3:
			// continuation：沿用上一个 message 的 header
			// 但需要处理 extended timestamp（当上一个 chunk 用了 ext ts 时）
			if st.hasDelta && st.timestampDelta == 0xFFFFFF {
				ext := make([]byte, 4)
				if _, err := io.ReadFull(cr.r, ext); err != nil {
					return nil, fmt.Errorf("chunk: read ext timestamp fmt3: %w", err)
				}
				// 注意：fmt3 的 ext timestamp 行为有争议，多数实现忽略
				_ = ext
			}
			if st.msgLength == 0 {
				return nil, fmt.Errorf("chunk: fmt3 but no previous message header")
			}
			if !st.newMessage {
				// 同一 message 的后续 chunk，timestamp 不变
			}

		default:
			return nil, fmt.Errorf("chunk: invalid fmt %d", fmt_)
		}

		// 3. 读 chunk data（最多 chunkSize 字节，或 message 剩余字节）
		remaining := int(st.msgLength) - st.bytesRead
		if remaining <= 0 {
			return nil, fmt.Errorf("chunk: invalid remaining %d", remaining)
		}
		toRead := remaining
		if toRead > cr.chunkSize {
			toRead = cr.chunkSize
		}
		n, err := io.ReadFull(cr.r, st.payload[st.bytesRead:st.bytesRead+toRead])
		if err != nil {
			return nil, fmt.Errorf("chunk: read data: %w", err)
		}
		st.bytesRead += n

		// 4. message 重组完成？
		if st.bytesRead >= int(st.msgLength) {
			st.newMessage = false
			return &Message{
				Type:      st.msgType,
				StreamID:  st.streamID,
				CSID:      csid,
				Timestamp: st.timestamp,
				Payload:   st.payload,
			}, nil
		}
		// 否则继续读下一个 chunk
	}
}

// readBasicHeader 解析 basic header，返回 (fmt, csid)
func (cr *ChunkReader) readBasicHeader() (uint8, uint32, error) {
	b, err := readByte(cr.r)
	if err != nil {
		return 0, 0, err
	}
	fmt_ := (b >> 6) & 0x03
	csid := uint32(b & 0x3f)
	switch csid {
	case 0:
		// 2 字节 header
		b2, err := readByte(cr.r)
		if err != nil {
			return 0, 0, err
		}
		csid = uint32(b2) + 64
	case 1:
		// 3 字节 header
		b2, err := readByte(cr.r)
		if err != nil {
			return 0, 0, err
		}
		b3, err := readByte(cr.r)
		if err != nil {
			return 0, 0, err
		}
		csid = uint32(b2) + uint32(b3)<<8 + 64
	}
	return fmt_, csid, nil
}

func (cr *ChunkReader) getState(csid uint32) *chunkState {
	st, ok := cr.states[csid]
	if !ok {
		st = &chunkState{}
		cr.states[csid] = st
	}
	return st
}

func readByte(r io.Reader) (byte, error) {
	buf := make([]byte, 1)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, err
	}
	return buf[0], nil
}

// ─── ChunkWriter ────────────────────────────────────────────────────────────

// ChunkWriter 把 Message 拆分为 chunk 写入 io.Writer。
type ChunkWriter struct {
	w         io.Writer
	chunkSize int
}

// NewChunkWriter 创建 chunk writer。
func NewChunkWriter(w io.Writer) *ChunkWriter {
	return &ChunkWriter{w: w, chunkSize: defaultChunkSize}
}

// SetChunkSize 更新 chunk size。
func (cw *ChunkWriter) SetChunkSize(size int) {
	if size > 0 {
		cw.chunkSize = size
	}
}

// WriteMessage 把一个 message 按 chunk 写出。
// 使用 fmt 0 写第一个 chunk，fmt 3 写后续 chunk（同一 CSID）。
func (cw *ChunkWriter) WriteMessage(msg *Message) error {
	if len(msg.Payload) == 0 {
		return nil
	}
	csid := msg.CSID
	if csid == 0 {
		csid = 3 // 默认用 CSID 3 写命令消息
	}

	// 第一个 chunk：fmt 0
	hdr := buildBasicHeader(0, csid)
	// message header fmt0: timestamp(3) + length(3) + type(1) + streamID(4 LE)
	ts := msg.Timestamp
	extTs := false
	if ts >= 0xFFFFFF {
		ts = 0xFFFFFF
		extTs = true
	}
	hdr = append(hdr, byte(ts>>16), byte(ts>>8), byte(ts))
	length := uint32(len(msg.Payload))
	hdr = append(hdr, byte(length>>16), byte(length>>8), byte(length))
	hdr = append(hdr, msg.Type)
	sid := make([]byte, 4)
	binary.LittleEndian.PutUint32(sid, msg.StreamID)
	hdr = append(hdr, sid...)
	if extTs {
		ext := make([]byte, 4)
		binary.BigEndian.PutUint32(ext, msg.Timestamp)
		hdr = append(hdr, ext...)
	}

	// 写第一个 chunk data
	toWrite := len(msg.Payload)
	if toWrite > cw.chunkSize {
		toWrite = cw.chunkSize
	}
	buf := append(hdr, msg.Payload[:toWrite]...)
	if _, err := cw.w.Write(buf); err != nil {
		return fmt.Errorf("chunk: write first chunk: %w", err)
	}

	// 后续 chunk：fmt 3
	written := toWrite
	for written < len(msg.Payload) {
		chdr := buildBasicHeader(3, csid)
		if extTs {
			ext := make([]byte, 4)
			binary.BigEndian.PutUint32(ext, msg.Timestamp)
			chdr = append(chdr, ext...)
		}
		remaining := len(msg.Payload) - written
		if remaining > cw.chunkSize {
			remaining = cw.chunkSize
		}
		cbuf := append(chdr, msg.Payload[written:written+remaining]...)
		if _, err := cw.w.Write(cbuf); err != nil {
			return fmt.Errorf("chunk: write continuation chunk: %w", err)
		}
		written += remaining
	}
	return nil
}

// buildBasicHeader 构造 basic header 字节
func buildBasicHeader(fmt_, csid uint32) []byte {
	switch {
	case csid < 64:
		return []byte{byte(fmt_<<6) | byte(csid)}
	case csid < 320:
		return []byte{byte(fmt_<<6) | 0, byte(csid - 64)}
	default:
		c := csid - 64
		return []byte{byte(fmt_<<6) | 1, byte(c & 0xff), byte(c >> 8)}
	}
}
