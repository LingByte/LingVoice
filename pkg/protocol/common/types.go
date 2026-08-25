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
	ProtocolRTMP   ProtocolType = "rtmp"
	ProtocolWHIP   ProtocolType = "whip"
	ProtocolWHEP   ProtocolType = "whep"
	ProtocolMQTT   ProtocolType = "mqtt"
)

// ─── 事件 ───────────────────────────────────────────────────────────────────

// EventType 协议事件类型（统一抽象，上层不关心是 SIP 还是 WebRTC）
type EventType int

const (
	EventIncomingCall  EventType = iota // 来电 (SIP INVITE / WebRTC offer / WS connect+offer)
	EventRinging                        // 振铃
	EventAnswered                       // 接听 / ICE 连接成功
	EventHangup                         // 挂断 / 连接关闭
	EventTransfer                       // 转接
	EventTrackAdded                     // 媒体轨道就绪（替代旧的 EventMediaReady）
	EventTrackRemoved                   // 媒体轨道移除
	EventDataChannel                    // 数据通道打开
	EventReconnect                      // 重连成功
	EventError                          // 错误
)

// ProtocolEvent 是所有协议信令事件的统一抽象。
// 上层（会话管理/编排）只看这个，不关心底层是 SIP 还是 WebRTC 还是 WS。
type ProtocolEvent struct {
	Type      EventType
	Protocol  ProtocolType
	SessionID string         // 协议层会话 ID (SIP Call-ID / WebRTC PC ID / WS Conn ID)
	From      string         // 主叫
	To        string         // 被叫
	Track     *TrackInfo     // EventTrackAdded/EventTrackRemoved 时填充
	Err       error          // EventError 时填充
	Timestamp time.Time
}

// ─── 指令 ───────────────────────────────────────────────────────────────────

// CommandType 协议指令类型
type CommandType int

const (
	CmdAnswer     CommandType = iota // 接听
	CmdReject                        // 拒绝（Reason 填原因）
	CmdHangup                        // 挂断
	CmdTransfer                      // 转接（Target 填目标）
	CmdStartMedia                    // 开始媒体传输
	CmdStopMedia                     // 停止媒体传输
	CmdIceRestart                    // ICE 重启（WebRTC 专用）
)

// ProtocolCommand 是所有协议指令的统一抽象。
// 上层通过这个控制协议会话，不直接操作 SIP socket 或 PeerConnection。
type ProtocolCommand struct {
	Type      CommandType
	SessionID string
	Reason    string          // CmdReject 时填（如 "busy", "declined"）
	Target    string          // CmdTransfer 时填目标
}

// ─── 媒体轨道 ───────────────────────────────────────────────────────────────

// TrackID 标识会话内的一条媒体轨道
type TrackID string

// TrackKind 轨道类型
type TrackKind uint8

const (
	TrackAudio TrackKind = 0x01
	TrackVideo TrackKind = 0x02
)

func (k TrackKind) String() string {
	switch k {
	case TrackAudio:
		return "audio"
	case TrackVideo:
		return "video"
	default:
		return "unknown"
	}
}

// TrackDirection 轨道方向
type TrackDirection uint8

const (
	TrackRecv TrackDirection = 0x01 // 从对端接收
	TrackSend TrackDirection = 0x02 // 向对端发送
)

// SimulcastLayer simulcast 层描述
type SimulcastLayer struct {
	RID    string // RID 标识："q"/"h"/"f" 或 "2"/"1"/"0"
	Width  uint16
	Height uint16
	FPS    uint8
}

// TrackInfo 描述一条媒体轨道
type TrackInfo struct {
	ID        TrackID
	Kind      TrackKind
	Direction TrackDirection
	Codec     CodecType
	SampleRate uint32
	Channels   uint16
	SSRC      uint32
	Layers    []SimulcastLayer // simulcast 层（视频），空表示非 simulcast
	StreamID  string           // 流 ID（WebRTC stream id / RTMP stream name）
}

// TrackConfig 创建轨道的配置
type TrackConfig struct {
	Kind       TrackKind
	Codec      CodecType
	SampleRate uint32
	Channels   uint16
	StreamID   string
	Label      string // track label
}

// ─── 媒体帧 ─────────────────────────────────────────────────────────────────

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
	CodecVP9   CodecType = 0x07
	CodecAV1   CodecType = 0x08
	CodecRTX   CodecType = 0x09
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
	case CodecVP9:
		return "vp9"
	case CodecAV1:
		return "av1"
	case CodecRTX:
		return "rtx"
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
	case "pcm16", "pcm":
		return CodecPCM16, nil
	case "h264":
		return CodecH264, nil
	case "vp8":
		return CodecVP8, nil
	case "vp9":
		return CodecVP9, nil
	case "av1":
		return CodecAV1, nil
	case "rtx":
		return CodecRTX, nil
	default:
		return 0, errors.New("unknown codec: " + s)
	}
}

// MediaFrame 音视频帧（协议无关）
type MediaFrame struct {
	Type       FrameType
	Codec      CodecType
	Payload    []byte
	Timestamp  uint32  // RTP timestamp（样本数时钟）
	Sequence   uint16  // RTP sequence number
	SampleRate uint32
	Channels   uint16
	SSRC       uint32  // RTP SSRC
	Marker     bool    // RTP marker bit（音频=VAD，视频=帧结束）
	RID        string  // simulcast RID（接收 simulcast 时填充）
}

// ─── 媒体统计 ───────────────────────────────────────────────────────────────

// TrackStats 轨道统计信息
type TrackStats struct {
	PacketsSent     uint64
	PacketsReceived uint64
	PacketsLost     uint64
	BytesSent       uint64
	BytesReceived   uint64
	RTT             time.Duration
	Jitter          time.Duration
	Bitrate         uint64 // bps
}

// ─── 数据通道 ───────────────────────────────────────────────────────────────

// DataMessage 数据通道消息
type DataMessage struct {
	Channel  string // 通道标签
	Data     []byte
	IsString bool // true=文本消息, false=二进制
}

// ─── 会话接口 ───────────────────────────────────────────────────────────────

// SignalSession 信令层接口（所有协议必须实现）。
// 负责会话生命周期管理、信令事件、控制指令。
type SignalSession interface {
	ID() string
	Protocol() ProtocolType
	// SendCommand 向协议会话下发指令（接听/拒绝/挂断/转接/ICE重启等）
	SendCommand(cmd ProtocolCommand) error
	// Close 关闭会话
	Close() error
}

// MediaSession 媒体层接口（支持媒体的协议实现）。
// 负责媒体帧收发、轨道查询、统计信息。
type MediaSession interface {
	// SendMediaFrame 向指定轨道发送音视频帧
	SendMediaFrame(trackID TrackID, frame MediaFrame) error
	// Tracks 返回当前所有轨道信息
	Tracks() []TrackInfo
	// MediaStats 返回所有轨道的统计信息
	MediaStats() map[TrackID]TrackStats
}

// TrackManager 动态轨道管理接口（WebRTC 等需要动态增删轨道的协议实现）。
// 对于固定轨道的协议（SIP/WS/RTMP），轨道由协议协商决定，不需要实现此接口。
type TrackManager interface {
	// AddTrack 添加一条发送轨道，返回轨道 ID
	AddTrack(config TrackConfig) (TrackID, error)
	// RemoveTrack 移除一条轨道
	RemoveTrack(trackID TrackID) error
}

// DataSession 数据层接口（支持 DataChannel 的协议实现）。
type DataSession interface {
	// SendData 通过指定通道发送数据
	SendData(channel string, data []byte) error
}

// ProtocolSession 协议会话基础接口。
// 所有协议至少实现 SignalSession，可选实现 MediaSession、TrackManager、DataSession。
// 调用方通过类型断言检查可选接口：
//
//	if ms, ok := sess.(MediaSession); ok { ms.SendMediaFrame(...) }
type ProtocolSession = SignalSession

// EventHandler 上层事件处理器接口。
// 协议层收到事件时回调上层；上层通过 ProtocolCommand 控制协议层。
type EventHandler interface {
	// OnEvent 协议信令事件（来电/接听/挂断/轨道就绪等）
	OnEvent(event ProtocolEvent) error
	// OnMediaFrame 收到对端的音视频帧
	OnMediaFrame(sessionID string, trackID TrackID, frame MediaFrame) error
	// OnData 收到数据通道消息
	OnData(sessionID string, msg DataMessage) error
}

// ─── 兼容类型（保留给 media/negotiator 等使用） ─────────────────────────────

// MediaDescription 协商后的媒体描述（用于协议层内部协商，不作为事件字段）
type MediaDescription struct {
	Audio *AudioMedia
	Video *VideoMedia
}

// AudioMedia 音频媒体描述
type AudioMedia struct {
	Codec           CodecType
	SampleRate      uint32
	Channels        uint16
	FrameDurationMs uint16 // 帧时长（ptime），如 20ms
}

// VideoMedia 视频媒体描述
type VideoMedia struct {
	Codec  CodecType
	Width  uint16
	Height uint16
	FPS    uint8
}
