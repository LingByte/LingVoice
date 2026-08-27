// Package moq transport layer: QUIC stream framing and MoQ message codec.
//
// 本文件封装 quic-go 的 stream 读写操作, 提供 MoQ 消息的编解码:
//   - varint (QUIC variable-length integer) 长度前缀编码
//   - Object 消息: track_alias + group_id + object_id + timestamp + payload
//   - Subscribe 消息: track_namespace + track_name + start_group + end_group
//   - Announce 消息: track_namespace + track_names
//
// 协议层 (Go) 负责信令/会话管理/轨道协商; 媒体处理由 Rust 层完成。
package moq

import (
	"fmt"
	"io"
	"math"

	"github.com/quic-go/quic-go/quicvarint"
)

// ─── varint 辅助 ────────────────────────────────────────────────────────────

// appendVarint 向 b 追加一个 QUIC varint 编码的 uint64。
func appendVarint(b []byte, v uint64) []byte {
	return quicvarint.Append(b, v)
}

// readVarint 从 r 读取一个 QUIC varint。
func readVarint(r quicvarint.Reader) (uint64, error) {
	return quicvarint.Read(r)
}

// appendLengthPrefixed 向 b 追加一段以 varint 长度为前缀的字节串。
func appendLengthPrefixed(b, data []byte) []byte {
	b = appendVarint(b, uint64(len(data)))
	return append(b, data...)
}

// readLengthPrefixed 读取一段 varint 长度前缀的字节串。
func readLengthPrefixed(r quicvarint.Reader) ([]byte, error) {
	n, err := readVarint(r)
	if err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}
	if n > math.MaxInt32 {
		return nil, fmt.Errorf("length too large: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}
	return buf, nil
}

// appendString 读取/写入 UTF-8 字符串 (varint 长度前缀)。
func appendString(b []byte, s string) []byte {
	return appendLengthPrefixed(b, []byte(s))
}

func readString(r quicvarint.Reader) (string, error) {
	buf, err := readLengthPrefixed(r)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// ─── MoQ 消息 ───────────────────────────────────────────────────────────────

// ObjectMessage MoQ Object 消息 (媒体数据单元)。
// 编码格式: type(varint) + track_alias(u64) + group_id(u64) + object_id(u64) + timestamp(u64) + payload(bytes)
type ObjectMessage struct {
	TrackAlias TrackAlias
	GroupID    uint64
	ObjectID   uint64
	Timestamp  uint64
	Payload    []byte
}

// Encode 将 ObjectMessage 编码为字节串 (含消息类型前缀)。
func (m *ObjectMessage) Encode() []byte {
	var b []byte
	b = appendVarint(b, uint64(MsgObject))
	b = appendVarint(b, uint64(m.TrackAlias))
	b = appendVarint(b, m.GroupID)
	b = appendVarint(b, m.ObjectID)
	b = appendVarint(b, m.Timestamp)
	b = appendLengthPrefixed(b, m.Payload)
	return b
}

// DecodeObjectMessage 从 reader 解码 Object 消息 (跳过已读取的消息类型)。
func DecodeObjectMessage(r quicvarint.Reader) (*ObjectMessage, error) {
	alias, err := readVarint(r)
	if err != nil {
		return nil, fmt.Errorf("read track_alias: %w", err)
	}
	groupID, err := readVarint(r)
	if err != nil {
		return nil, fmt.Errorf("read group_id: %w", err)
	}
	objID, err := readVarint(r)
	if err != nil {
		return nil, fmt.Errorf("read object_id: %w", err)
	}
	ts, err := readVarint(r)
	if err != nil {
		return nil, fmt.Errorf("read timestamp: %w", err)
	}
	payload, err := readLengthPrefixed(r)
	if err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}
	return &ObjectMessage{
		TrackAlias: TrackAlias(alias),
		GroupID:    groupID,
		ObjectID:   objID,
		Timestamp:  ts,
		Payload:    payload,
	}, nil
}

// SubscribeMessage MoQ SUBSCRIBE 消息。
// 编码格式: type(varint) + subscribe_id(u64) + track_alias(u64) + track_namespace(string) + track_name(string) + start_group(u64) + end_group(u64)
type SubscribeMessage struct {
	SubscribeID    SubscribeID
	TrackAlias     TrackAlias
	TrackNamespace string
	TrackName      string
	StartGroup     uint64
	EndGroup       uint64
}

// Encode 将 SubscribeMessage 编码为字节串。
func (m *SubscribeMessage) Encode() []byte {
	var b []byte
	b = appendVarint(b, uint64(MsgSubscribe))
	b = appendVarint(b, uint64(m.SubscribeID))
	b = appendVarint(b, uint64(m.TrackAlias))
	b = appendString(b, m.TrackNamespace)
	b = appendString(b, m.TrackName)
	b = appendVarint(b, m.StartGroup)
	b = appendVarint(b, m.EndGroup)
	return b
}

// DecodeSubscribeMessage 从 reader 解码 SUBSCRIBE 消息 (跳过消息类型)。
func DecodeSubscribeMessage(r quicvarint.Reader) (*SubscribeMessage, error) {
	subID, err := readVarint(r)
	if err != nil {
		return nil, fmt.Errorf("read subscribe_id: %w", err)
	}
	alias, err := readVarint(r)
	if err != nil {
		return nil, fmt.Errorf("read track_alias: %w", err)
	}
	ns, err := readString(r)
	if err != nil {
		return nil, fmt.Errorf("read track_namespace: %w", err)
	}
	name, err := readString(r)
	if err != nil {
		return nil, fmt.Errorf("read track_name: %w", err)
	}
	start, err := readVarint(r)
	if err != nil {
		return nil, fmt.Errorf("read start_group: %w", err)
	}
	end, err := readVarint(r)
	if err != nil {
		return nil, fmt.Errorf("read end_group: %w", err)
	}
	return &SubscribeMessage{
		SubscribeID:    SubscribeID(subID),
		TrackAlias:     TrackAlias(alias),
		TrackNamespace: ns,
		TrackName:      name,
		StartGroup:     start,
		EndGroup:       end,
	}, nil
}

// AnnounceMessage MoQ ANNOUNCE 消息。
// 编码格式: type(varint) + track_namespace(string) + track_names_count(u64) + track_names[]string
type AnnounceMessage struct {
	TrackNamespace string
	TrackNames     []string
}

// Encode 将 AnnounceMessage 编码为字节串。
func (m *AnnounceMessage) Encode() []byte {
	var b []byte
	b = appendVarint(b, uint64(MsgAnnounce))
	b = appendString(b, m.TrackNamespace)
	b = appendVarint(b, uint64(len(m.TrackNames)))
	for _, name := range m.TrackNames {
		b = appendString(b, name)
	}
	return b
}

// DecodeAnnounceMessage 从 reader 解码 ANNOUNCE 消息 (跳过消息类型)。
func DecodeAnnounceMessage(r quicvarint.Reader) (*AnnounceMessage, error) {
	ns, err := readString(r)
	if err != nil {
		return nil, fmt.Errorf("read track_namespace: %w", err)
	}
	count, err := readVarint(r)
	if err != nil {
		return nil, fmt.Errorf("read track_names count: %w", err)
	}
	if count > math.MaxInt32 {
		return nil, fmt.Errorf("track_names count too large: %d", count)
	}
	names := make([]string, 0, count)
	for i := uint64(0); i < count; i++ {
		name, err := readString(r)
		if err != nil {
			return nil, fmt.Errorf("read track_name[%d]: %w", i, err)
		}
		names = append(names, name)
	}
	return &AnnounceMessage{
		TrackNamespace: ns,
		TrackNames:     names,
	}, nil
}

// GoAwayMessage MoQ GOAWAY 消息 (无载荷, 仅类型)。
type GoAwayMessage struct {
	NewURI string // 可选, 空表示无重定向
}

// Encode 将 GoAwayMessage 编码为字节串。
func (m *GoAwayMessage) Encode() []byte {
	var b []byte
	b = appendVarint(b, uint64(MsgGoAway))
	b = appendString(b, m.NewURI)
	return b
}

// DecodeGoAwayMessage 从 reader 解码 GOAWAY 消息 (跳过消息类型)。
func DecodeGoAwayMessage(r quicvarint.Reader) (*GoAwayMessage, error) {
	uri, err := readString(r)
	if err != nil {
		return nil, fmt.Errorf("read new_uri: %w", err)
	}
	return &GoAwayMessage{NewURI: uri}, nil
}

// ─── 消息帧读写 ──────────────────────────────────────────────────────────────

// EncodeMessage 将任意 MoQ 消息编码并写入 writer (varint 长度前缀)。
func EncodeMessage(w io.Writer, msg interface{ Encode() []byte }) error {
	data := msg.Encode()
	buf := appendVarint(nil, uint64(len(data)))
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("write length: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	return nil
}

// ReadMessage 从 reader 读取一条完整的 MoQ 消息 (varint 长度前缀)。
// 返回消息类型和原始载荷 (不含长度前缀), 由调用方根据类型进一步解码。
func ReadMessage(r io.Reader) (MessageType, []byte, error) {
	vr := quicvarint.NewReader(r)
	n, err := readVarint(vr)
	if err != nil {
		return 0, nil, fmt.Errorf("read message length: %w", err)
	}
	if n > math.MaxInt32 {
		return 0, nil, fmt.Errorf("message too large: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, fmt.Errorf("read message body: %w", err)
	}
	// 解析消息类型 (第一个 varint)
	val, consumed, err := quicvarint.Parse(buf)
	if err != nil {
		return 0, nil, fmt.Errorf("parse message type: %w", err)
	}
	return MessageType(val), buf[consumed:], nil
}

// DecodeMessageBody 根据消息类型从载荷解码出具体消息。
func DecodeMessageBody(msgType MessageType, body []byte) (interface{}, error) {
	r := newBytesReader(body)
	switch msgType {
	case MsgObject:
		return DecodeObjectMessage(r)
	case MsgSubscribe:
		return DecodeSubscribeMessage(r)
	case MsgAnnounce:
		return DecodeAnnounceMessage(r)
	case MsgGoAway:
		return DecodeGoAwayMessage(r)
	default:
		return nil, fmt.Errorf("unsupported message type: %s", msgType)
	}
}

// bytesReader 包装 []byte 为 quicvarint.Reader。
type bytesReader struct {
	buf []byte
	pos int
}

func newBytesReader(b []byte) *bytesReader { return &bytesReader{buf: b} }

func (b *bytesReader) Read(p []byte) (int, error) {
	if b.pos >= len(b.buf) {
		return 0, io.EOF
	}
	n := copy(p, b.buf[b.pos:])
	b.pos += n
	return n, nil
}

func (b *bytesReader) ReadByte() (byte, error) {
	if b.pos >= len(b.buf) {
		return 0, io.EOF
	}
	c := b.buf[b.pos]
	b.pos++
	return c, nil
}

// ─── QUIC stream 传输封装 ────────────────────────────────────────────────────

// StreamWriter 封装 QUIC stream 的写操作 (接口形式便于测试)。
type StreamWriter interface {
	io.Writer
	io.Closer
}

// StreamReader 封装 QUIC stream 的读操作 (接口形式便于测试)。
type StreamReader interface {
	io.Reader
}

// WriteObject 通过 QUIC stream 发送一个 MoQ Object 消息。
func WriteObject(w StreamWriter, msg *ObjectMessage) error {
	return EncodeMessage(w, msg)
}

// ReadObject 从 QUIC stream 读取一个 MoQ Object 消息。
func ReadObject(r StreamReader) (*ObjectMessage, error) {
	msgType, body, err := ReadMessage(r)
	if err != nil {
		return nil, err
	}
	if msgType != MsgObject {
		return nil, fmt.Errorf("expected OBJECT, got %s", msgType)
	}
	m, err := DecodeMessageBody(msgType, body)
	if err != nil {
		return nil, err
	}
	return m.(*ObjectMessage), nil
}
