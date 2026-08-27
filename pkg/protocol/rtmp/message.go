// Package rtmp implements an RTMP protocol-layer server (publish ingest).
//
// 纯协议层职责：
//   - 完成 RTMP handshake (C0/C1/C2 → S0/S1/S2)
//   - 解析 chunk stream，重组为 RTMP message
//   - 处理协议控制消息 (Set Chunk Size / Window Ack / Set Peer Bandwidth)
//   - 处理 publish 命令，提取 H.264/AVC + AAC payload，转为 RTP 包
//   - 通过 EventHandler.OnMediaFrame 转发给上层（rustbridge → Rust media-node）
//
// 不做媒体路由/分发/转码——那是 Rust 媒体层的职责。
package rtmp

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
)

// ─── RTMP message type IDs ───────────────────────────────────────────────────

const (
	MsgSetChunkSize      uint8 = 1  // 协议控制：设置 chunk size
	MsgAbortMessage      uint8 = 2
	MsgAcknowledgement   uint8 = 3  // 协议控制：确认
	MsgWindowAckSize     uint8 = 5  // 协议控制：窗口确认大小
	MsgSetPeerBandwidth  uint8 = 6  // 协议控制：设置对端带宽
	MsgUserControl       uint8 = 4  // 用户控制消息
	MsgCommandAMF0       uint8 = 20 // 命令消息 (AMF0)
	MsgCommandAMF3       uint8 = 17 // 命令消息 (AMF3)
	MsgDataAMF0          uint8 = 18 // 数据消息 (AMF0)
	MsgDataAMF3          uint8 = 15
	MsgSharedObjectAMF0  uint8 = 19
	MsgSharedObjectAMF3  uint8 = 16
	MsgAudio             uint8 = 8  // 音频消息
	MsgVideo             uint8 = 9  // 视频消息
	MsgAggregate         uint8 = 22
)

// UserControl event types
const (
	UCStreamBegin      uint16 = 0
	UCStreamEOF        uint16 = 1
	UCStreamDry        uint16 = 2
	UCSetBufferLength  uint16 = 3
	UCStreamIsRecorded uint16 = 4
	UCPingRequest      uint16 = 6
	UCPingResponse     uint16 = 7
)

// 默认 chunk size（RTMP 规范默认 128）
const defaultChunkSize = 128

// 默认 window acknowledgement size
const defaultWindowAckSize = 2500000

// Message 表示一个完整的 RTMP message（由一个或多个 chunk 重组而成）。
type Message struct {
	Type      uint8  // message type id
	StreamID  uint32 // message stream id (little-endian in wire)
	CSID      uint32 // chunk stream id (来自 basic header)
	Timestamp uint32 // message timestamp (ms)
	Payload   []byte // message payload
}

// ─── AMF0 编码/解码 ─────────────────────────────────────────────────────────

// AMF0 marker 类型
const (
	amf0Number       = 0x00
	amf0Boolean      = 0x01
	amf0String       = 0x02
	amf0Object       = 0x03
	amf0Null         = 0x05
	amf0Undefined    = 0x06
	amf0Reference    = 0x07
	amf0EcmaArray    = 0x08
	amf0ObjectEnd    = 0x09
	amf0StrictArray  = 0x0a
	amf0Date         = 0x0b
	amf0LongString   = 0x0c
	amf0XMLDoc       = 0x0f
	amf0TypedObject  = 0x10
)

// AMFValue 是 AMF0 解码后的值（动态类型）
type AMFValue struct {
	Type   int
	Number float64
	Bool   bool
	Str    string
	Obj    map[string]AMFValue
	Arr    []AMFValue
}

// AMF0 marker 常量（供外部判断 Type）
const (
	AMFTypeNumber   = 0
	AMFTypeBoolean  = 1
	AMFTypeString   = 2
	AMFTypeObject   = 3
	AMFTypeNull     = 5
	AMFTypeEcmaArray = 8
)

// amfReader 是 AMF0 字节流的读取器
type amfReader struct {
	buf []byte
	pos int
}

func newAMFReader(buf []byte) *amfReader { return &amfReader{buf: buf} }

func (r *amfReader) remaining() int { return len(r.buf) - r.pos }

func (r *amfReader) readByte() (byte, error) {
	if r.pos >= len(r.buf) {
		return 0, fmt.Errorf("amf: unexpected eof")
	}
	b := r.buf[r.pos]
	r.pos++
	return b, nil
}

func (r *amfReader) readBytes(n int) ([]byte, error) {
	if r.pos+n > len(r.buf) {
		return nil, fmt.Errorf("amf: unexpected eof, need %d have %d", n, r.remaining())
	}
	b := r.buf[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

func (r *amfReader) readUint16() (uint16, error) {
	b, err := r.readBytes(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b), nil
}

func (r *amfReader) readUint32() (uint32, error) {
	b, err := r.readBytes(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (r *amfReader) readFloat64() (float64, error) {
	b, err := r.readBytes(8)
	if err != nil {
		return 0, err
	}
	bits := binary.BigEndian.Uint64(b)
	return math.Float64frombits(bits), nil
}

func (r *amfReader) readString() (string, error) {
	n, err := r.readUint16()
	if err != nil {
		return "", err
	}
	b, err := r.readBytes(int(n))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// readValue 读取一个 AMF0 值（含 marker）
func (r *amfReader) readValue() (AMFValue, error) {
	marker, err := r.readByte()
	if err != nil {
		return AMFValue{}, err
	}
	switch marker {
	case amf0Number:
		v, err := r.readFloat64()
		return AMFValue{Type: AMFTypeNumber, Number: v}, err
	case amf0Boolean:
		b, err := r.readByte()
		return AMFValue{Type: AMFTypeBoolean, Bool: b != 0}, err
	case amf0String:
		s, err := r.readString()
		return AMFValue{Type: AMFTypeString, Str: s}, err
	case amf0Object:
		return r.readObject()
	case amf0Null:
		return AMFValue{Type: AMFTypeNull}, nil
	case amf0Undefined:
		return AMFValue{Type: AMFTypeNull}, nil
	case amf0EcmaArray:
		// 4 字节 count（通常被忽略，按 object 结束标志解析）
		if _, err := r.readUint32(); err != nil {
			return AMFValue{}, err
		}
		return r.readObjectAsArray()
	case amf0StrictArray:
		count, err := r.readUint32()
		if err != nil {
			return AMFValue{}, err
		}
		arr := make([]AMFValue, 0, count)
		for i := uint32(0); i < count; i++ {
			v, err := r.readValue()
			if err != nil {
				return AMFValue{}, err
			}
			arr = append(arr, v)
		}
		return AMFValue{Type: AMFTypeEcmaArray, Arr: arr}, nil
	case amf0LongString, amf0XMLDoc:
		n, err := r.readUint32()
		if err != nil {
			return AMFValue{}, err
		}
		b, err := r.readBytes(int(n))
		if err != nil {
			return AMFValue{}, err
		}
		return AMFValue{Type: AMFTypeString, Str: string(b)}, nil
	case amf0Date:
		_, err := r.readFloat64()
		if err != nil {
			return AMFValue{}, err
		}
		if _, err := r.readUint16(); err != nil { // timezone
			return AMFValue{}, err
		}
		return AMFValue{Type: AMFTypeNull}, nil
	default:
		return AMFValue{}, fmt.Errorf("amf: unsupported marker 0x%02x", marker)
	}
}

func (r *amfReader) readObject() (AMFValue, error) {
	obj := make(map[string]AMFValue)
	for {
		// 读 property name（UTF-8）
		name, err := r.readString()
		if err != nil {
			return AMFValue{}, err
		}
		// 检查 object end marker
		if len(name) == 0 {
			m, err := r.readByte()
			if err != nil {
				return AMFValue{}, err
			}
			if m == amf0ObjectEnd {
				return AMFValue{Type: AMFTypeObject, Obj: obj}, nil
			}
			return AMFValue{}, fmt.Errorf("amf: expected object end marker, got 0x%02x", m)
		}
		v, err := r.readValue()
		if err != nil {
			return AMFValue{}, err
		}
		obj[name] = v
	}
}

// readObjectAsArray 把 ecma array 当作 object 解析（结构相同，只是前面多了 count）
func (r *amfReader) readObjectAsArray() (AMFValue, error) {
	obj := make(map[string]AMFValue)
	for {
		name, err := r.readString()
		if err != nil {
			return AMFValue{}, err
		}
		if len(name) == 0 {
			m, err := r.readByte()
			if err != nil {
				return AMFValue{}, err
			}
			if m == amf0ObjectEnd {
				return AMFValue{Type: AMFTypeEcmaArray, Obj: obj}, nil
			}
			return AMFValue{}, fmt.Errorf("amf: expected object end marker, got 0x%02x", m)
		}
		v, err := r.readValue()
		if err != nil {
			return AMFValue{}, err
		}
		obj[name] = v
	}
}

// DecodeAMF0 解码 AMF0 字节流为有序值列表。
// RTMP 命令消息通常是一串 AMF0 值：commandName, transactionID, commandObject, ...
func DecodeAMF0(buf []byte) ([]AMFValue, error) {
	r := newAMFReader(buf)
	var values []AMFValue
	for r.remaining() > 0 {
		v, err := r.readValue()
		if err != nil {
			return values, err
		}
		values = append(values, v)
	}
	return values, nil
}

// ─── AMF0 编码 ──────────────────────────────────────────────────────────────

// amfWriter 是 AMF0 字节流的写入器
type amfWriter struct {
	buf []byte
}

func newAMFWriter() *amfWriter { return &amfWriter{} }

func (w *amfWriter) Bytes() []byte { return w.buf }

func (w *amfWriter) writeByte(b byte) { w.buf = append(w.buf, b) }

func (w *amfWriter) writeBytes(b ...byte) { w.buf = append(w.buf, b...) }

func (w *amfWriter) writeUint16(v uint16) {
	w.buf = append(w.buf, byte(v>>8), byte(v))
}

func (w *amfWriter) writeUint32(v uint32) {
	w.buf = append(w.buf, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (w *amfWriter) writeFloat64(v float64) {
	bits := math.Float64bits(v)
	w.buf = append(w.buf,
		byte(bits>>56), byte(bits>>48), byte(bits>>40), byte(bits>>32),
		byte(bits>>24), byte(bits>>16), byte(bits>>8), byte(bits))
}

// WriteString 编码一个 AMF0 string
func (w *amfWriter) WriteString(s string) {
	w.writeByte(amf0String)
	w.writeUint16(uint16(len(s)))
	w.writeBytes([]byte(s)...)
}

// WriteNumber 编码一个 AMF0 number
func (w *amfWriter) WriteNumber(v float64) {
	w.writeByte(amf0Number)
	w.writeFloat64(v)
}

// WriteBoolean 编码一个 AMF0 boolean
func (w *amfWriter) WriteBoolean(v bool) {
	w.writeByte(amf0Boolean)
	if v {
		w.writeByte(1)
	} else {
		w.writeByte(0)
	}
}

// WriteNull 编码一个 AMF0 null
func (w *amfWriter) WriteNull() {
	w.writeByte(amf0Null)
}

// WriteObject 编码一个 AMF0 object（属性 map）
func (w *amfWriter) WriteObject(props map[string]AMFValue) {
	w.writeByte(amf0Object)
	for name, v := range props {
		w.writeUint16(uint16(len(name)))
		w.writeBytes([]byte(name)...)
		w.writeValue(v)
	}
	// object end: empty name + 0x09
	w.writeUint16(0)
	w.writeByte(amf0ObjectEnd)
}

// WriteEcmaArray 编码一个 AMF0 ecma array（属性 map，前面带 count）
func (w *amfWriter) WriteEcmaArray(props map[string]AMFValue) {
	w.writeByte(amf0EcmaArray)
	w.writeUint32(uint32(len(props)))
	for name, v := range props {
		w.writeUint16(uint16(len(name)))
		w.writeBytes([]byte(name)...)
		w.writeValue(v)
	}
	w.writeUint16(0)
	w.writeByte(amf0ObjectEnd)
}

func (w *amfWriter) writeValue(v AMFValue) {
	switch v.Type {
	case AMFTypeNumber:
		w.WriteNumber(v.Number)
	case AMFTypeBoolean:
		w.WriteBoolean(v.Bool)
	case AMFTypeString:
		w.WriteString(v.Str)
	case AMFTypeNull:
		w.WriteNull()
	default:
		w.WriteNull()
	}
}

// ─── 辅助：从 AMF 值取值 ────────────────────────────────────────────────────

func amfString(v AMFValue) (string, bool) {
	if v.Type == AMFTypeString {
		return v.Str, true
	}
	return "", false
}

func amfNumber(v AMFValue) (float64, bool) {
	if v.Type == AMFTypeNumber {
		return v.Number, true
	}
	return 0, false
}

func amfBool(v AMFValue) (bool, bool) {
	if v.Type == AMFTypeBoolean {
		return v.Bool, true
	}
	return false, false
}

func amfObject(v AMFValue) (map[string]AMFValue, bool) {
	if v.Type == AMFTypeObject || v.Type == AMFTypeEcmaArray {
		return v.Obj, true
	}
	return nil, false
}

// amfInt 把 number 当作 int 返回
func amfInt(v AMFValue) (int, bool) {
	n, ok := amfNumber(v)
	if !ok {
		return 0, false
	}
	return int(n), true
}

// ─── 辅助：构造 connect 响应属性 ────────────────────────────────────────────

// ConnectResultProps 构造 _result 的 properties（NetConnection.Connect.Success）
func ConnectResultProps() map[string]AMFValue {
	return map[string]AMFValue{
		"fmsVer":       {Type: AMFTypeString, Str: "FMS/3,0,1,123"},
		"capabilities": {Type: AMFTypeNumber, Number: 31},
		"mode":         {Type: AMFTypeNumber, Number: 1},
	}
}

// ConnectResultInfo 构造 _result 的 info object
func ConnectResultInfo() map[string]AMFValue {
	return map[string]AMFValue{
		"level":          {Type: AMFTypeString, Str: "status"},
		"code":           {Type: AMFTypeString, Str: "NetConnection.Connect.Success"},
		"description":    {Type: AMFTypeString, Str: "Connection succeeded"},
		"objectEncoding": {Type: AMFTypeNumber, Number: 0},
	}
}

// PublishStatusInfo 构造 publish 的 status info
func PublishStatusInfo(code, description string) map[string]AMFValue {
	return map[string]AMFValue{
		"level":       {Type: AMFTypeString, Str: "status"},
		"code":        {Type: AMFTypeString, Str: code},
		"description": {Type: AMFTypeString, Str: description},
	}
}

// floatToStr 把 AMF number 转为字符串（用于 transaction id 比较）
func floatToStr(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
