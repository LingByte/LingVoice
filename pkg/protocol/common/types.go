package common

import (
	"errors"
	"time"
)

// ProtocolType 标识协议来源
type ProtocolType string

const (
	ProtocolSIP    ProtocolType = "sip"
	ProtocolWebRTC ProtocolType = "webrtc"
	ProtocolWS     ProtocolType = "ws"
)

// EventType 协议事件类型（统一抽象，上层不关心是 SIP 还是 WebRTC）
type EventType int

const (
	EventIncomingCall EventType = iota // 来电 (SIP INVITE / WebRTC offer / WS connect+offer)
	EventRinging                       // 振铃
	EventAnswered                      // 接听
	EventHangup                        // 挂断
	EventTransfer                      // 转接
	EventMediaReady                    // 媒体就绪，可以收发音视频帧
	EventError                         // 错误
)

// ProtocolEvent 是所有协议信令事件的统一抽象。
// 上层（会话管理/编排）只看这个，不关心底层是 SIP 还是 WebRTC 还是 WS。
type ProtocolEvent struct {
	Type      EventType
	Protocol  ProtocolType
	SessionID string // 协议层会话 ID (SIP Call-ID / WebRTC PC ID / WS Conn ID)
	From      string // 主叫
	To        string // 被叫
	Media     *MediaDescription // 协商后的媒体描述（可能为空）
	Err       error             // EventError 时填充
	Timestamp time.Time
}

// CommandType 协议指令类型
type CommandType int

const (
	CmdAnswer     CommandType = iota // 接听
	CmdReject                        // 拒绝（Reason 填原因）
	CmdHangup                        // 挂断
	CmdTransfer                      // 转接（Target 填目标）
	CmdStartMedia                    // 开始媒体传输
	CmdStopMedia                     // 停止媒体传输
)

// ProtocolCommand 是所有协议指令的统一抽象。
// 上层通过这个控制协议会话，不直接操作 SIP socket 或 PeerConnection。
type ProtocolCommand struct {
	Type      CommandType
	SessionID string
	Reason    string          // CmdReject 时填（如 "busy", "declined"）
	Target    string          // CmdTransfer 时填目标
	Media     *MediaDescription // CmdAnswer 时填协商后的媒体描述
}

// FrameType 帧类型
type FrameType uint8

const (
	FrameAudio FrameType = 0x01
	FrameVideo FrameType = 0x02
)

// CodecType 编解码类型
type CodecType uint8

const (
	CodecOpus  CodecType = 0x01
	CodecPCMU  CodecType = 0x02
	CodecPCMA  CodecType = 0x03
	CodecPCM16 CodecType = 0x04
	CodecH264  CodecType = 0x05
	CodecVP8   CodecType = 0x06
)

func (c CodecType) String() string {
	switch c {
	case CodecOpus:
		return "opus"
	case CodecPCMU:
		return "pcmu"
	case CodecPCMA:
		return "pcma"
	case CodecPCM16:
		return "pcm16"
	case CodecH264:
		return "h264"
	case CodecVP8:
		return "vp8"
	default:
		return "unknown"
	}
}

func CodecFromString(s string) (CodecType, error) {
	switch s {
	case "opus":
		return CodecOpus, nil
	case "pcmu":
		return CodecPCMU, nil
	case "pcma":
		return CodecPCMA, nil
	case "pcm16":
		return CodecPCM16, nil
	case "h264":
		return CodecH264, nil
	case "vp8":
		return CodecVP8, nil
	default:
		return 0, errors.New("unknown codec: " + s)
	}
}

// MediaFrame 音视频帧（协议无关）
type MediaFrame struct {
	Type       FrameType
	Codec      CodecType
	Payload    []byte
	Timestamp  uint32 // 样本数时钟
	Sequence   uint16
	SampleRate uint32
	Channels   uint16
}

// MediaDescription 协商后的媒体描述
type MediaDescription struct {
	Audio *AudioMedia
	Video *VideoMedia
}

// AudioMedia 音频媒体描述
type AudioMedia struct {
	Codec          CodecType
	SampleRate     uint32
	Channels       uint16
	FrameDurationMs uint16 // 帧时长（ptime），如 20ms
}

// VideoMedia 视频媒体描述
type VideoMedia struct {
	Codec CodecType
	Width  uint16
	Height uint16
	FPS    uint8
}

// ProtocolSession 表示一个协议会话（统一接口）
type ProtocolSession interface {
	ID() string
	Protocol() ProtocolType
	// SendCommand 向协议会话下发指令（接听/拒绝/挂断/转接等）
	SendCommand(cmd ProtocolCommand) error
	// SendMediaFrame 向对端发送音视频帧
	SendMediaFrame(frame MediaFrame) error
	// Close 关闭会话
	Close() error
}

// EventHandler 上层事件处理器接口。
// 协议层收到事件时回调上层；上层通过 ProtocolCommand 控制协议层。
type EventHandler interface {
	// OnEvent 协议信令事件（来电/接听/挂断等）
	OnEvent(event ProtocolEvent) error
	// OnMediaFrame 收到对端的音视频帧
	OnMediaFrame(sessionID string, frame MediaFrame) error
}
