package ws

// LingVoice WebSocket 语音协议定义
//
// 协议分两个阶段：
//   1. 协商阶段：文本 JSON 消息，完成编解码/采样率/会话协商
//   2. 媒体阶段：二进制帧传输音视频，文本 JSON 传控制消息
//
// 协商流程：
//   Client → Server: OfferMessage   (声明支持的编解码列表)
//   Server → Client: AnswerMessage  (选定编解码 + 分配 sessionID)
//   Client → Server: StartMessage   (客户端准备就绪)
//   Server → Client: ReadyMessage   (服务端准备就绪，可以开始发媒体)
//
// 媒体阶段二进制帧格式（8 字节头 + payload）：
//   [0]    frame_type:  0x01=audio, 0x02=video
//   [1]    codec_id:    0x01=opus, 0x02=pcmu, 0x03=pcma, 0x04=pcm16, 0x05=h264, 0x06=vp8
//   [2-3]  sequence:    uint16 big-endian
//   [4-7]  timestamp:   uint32 big-endian (样本数时钟)
//   [8+]   payload
//
// 媒体阶段控制消息（文本 JSON）：
//   {"type":"stop"}                    停止媒体传输
//   {"type":"dtmf","digit":"1"}        DTMF 事件
//   {"type":"event","event":"speaking"}  VAD 事件（speaking/silence）

import "github.com/LingByte/LingVoice/pkg/protocol/common"

// 协议版本
const ProtocolVersion = 1

// 二进制帧头长度
const FrameHeaderLen = 8

// 消息类型（协商阶段 + 控制阶段）
const (
	MsgTypeOffer  = "offer"
	MsgTypeAnswer = "answer"
	MsgTypeStart  = "start"
	MsgTypeReady  = "ready"
	MsgTypeStop   = "stop"
	MsgTypeDTMF   = "dtmf"
	MsgTypeEvent  = "event"
	MsgTypeError  = "error"
)

// OfferMessage 客户端 → 服务端：声明支持的编解码
type OfferMessage struct {
	Type    string      `json:"type"`
	Version int         `json:"version"`
	Session string      `json:"session,omitempty"` // 可选，续接已有会话
	Media   OfferMedia  `json:"media"`
}

// OfferMedia 客户端声明的媒体能力
type OfferMedia struct {
	Audio *OfferAudio `json:"audio,omitempty"`
	Video *OfferVideo `json:"video,omitempty"`
}

// OfferAudio 客户端声明的音频能力
type OfferAudio struct {
	Codecs      []string `json:"codecs"`       // 如 ["opus","pcmu","pcma"]
	SampleRates []uint32 `json:"sampleRates"`  // 如 [48000,8000]
	Channels    []uint16 `json:"channels"`     // 如 [1,2]
}

// OfferVideo 客户端声明的视频能力
type OfferVideo struct {
	Codecs []string `json:"codecs"` // 如 ["h264","vp8"]
}

// AnswerMessage 服务端 → 客户端：选定编解码 + 分配 sessionID
type AnswerMessage struct {
	Type    string       `json:"type"`
	Session string       `json:"session"` // 服务端分配的会话 ID
	Media   AnswerMedia  `json:"media"`
}

// AnswerMedia 服务端选定的媒体
type AnswerMedia struct {
	Audio *AnswerAudio `json:"audio,omitempty"`
	Video *AnswerVideo `json:"video,omitempty"`
}

// AnswerAudio 服务端选定的音频参数
type AnswerAudio struct {
	Codec          string `json:"codec"`          // 如 "opus"
	SampleRate     uint32 `json:"sampleRate"`     // 如 48000
	Channels       uint16 `json:"channels"`       // 如 1
	FrameDurationMs uint16 `json:"frameDurationMs"` // 如 20
}

// AnswerVideo 服务端选定的视频参数
type AnswerVideo struct {
	Codec  string `json:"codec"`
	Width  uint16 `json:"width"`
	Height uint16 `json:"height"`
	FPS    uint8  `json:"fps"`
}

// StartMessage 客户端 → 服务端：准备就绪，可以开始
type StartMessage struct {
	Type string `json:"type"`
}

// ReadyMessage 服务端 → 客户端：服务端就绪，可以发媒体了
type ReadyMessage struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"` // 服务端时间戳（unix ms）
}

// StopMessage 媒体阶段控制：停止传输
type StopMessage struct {
	Type string `json:"type"`
}

// DTMFMessage DTMF 事件
type DTMFMessage struct {
	Type  string `json:"type"`
	Digit string `json:"digit"`
}

// EventMessage VAD 等事件
type EventMessage struct {
	Type  string `json:"type"`
	Event string `json:"event"` // "speaking", "silence", "barge-in"
}

// ErrorMessage 错误消息
type ErrorMessage struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// --- 二进制帧编解码 ---

// EncodeFrame 将 MediaFrame 编码为二进制帧（8字节头 + payload）
func EncodeFrame(frame common.MediaFrame) []byte {
	buf := make([]byte, FrameHeaderLen+len(frame.Payload))
	buf[0] = byte(frame.Type)
	buf[1] = byte(frame.Codec)
	buf[2] = byte(frame.Sequence >> 8)
	buf[3] = byte(frame.Sequence)
	buf[4] = byte(frame.Timestamp >> 24)
	buf[5] = byte(frame.Timestamp >> 16)
	buf[6] = byte(frame.Timestamp >> 8)
	buf[7] = byte(frame.Timestamp)
	copy(buf[FrameHeaderLen:], frame.Payload)
	return buf
}

// DecodeFrame 从二进制帧解码出 MediaFrame
func DecodeFrame(data []byte) (common.MediaFrame, error) {
	if len(data) < FrameHeaderLen {
		return common.MediaFrame{}, ErrFrameTooShort
	}
	frame := common.MediaFrame{
		Type:      common.FrameType(data[0]),
		Codec:     common.CodecType(data[1]),
		Sequence:  uint16(data[2])<<8 | uint16(data[3]),
		Timestamp: uint32(data[4])<<24 | uint32(data[5])<<16 | uint32(data[6])<<8 | uint32(data[7]),
		Payload:   data[FrameHeaderLen:],
	}
	return frame, nil
}
