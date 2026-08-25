package webrtc

import (
	"time"

	"github.com/pion/webrtc/v4"
)

// Config WebRTC 服务配置
type Config struct {
	// Addr 信令 HTTP/WS 监听地址
	Addr string
	// Path 信令路径
	Path string

	// ICEServers STUN/TURN 服务器
	ICEServers []webrtc.ICEServer

	// EphemeralUDPPortRange ICE UDP 端口范围 [min, max]
	EphemeralUDPPortRange [2]uint16

	// EnableTURN 是否启用内置 TURN
	EnableTURN bool
	// TURNPort TURN UDP 端口
	TURNPort uint16
	// TURNTLSPort TURN TLS 端口
	TURNTLSPort uint16

	// Publisher 编解码配置（接收客户端媒体）
	Publisher DirectionConfig
	// Subscriber 编解码配置（向客户端发送媒体）
	Subscriber DirectionConfig

	// ICEDisconnectedTimeout ICE 断开超时（秒）
	ICEDisconnectedTimeout uint16
	// ICEFailedTimeout ICE 失败超时（秒）
	ICEFailedTimeout uint16
	// ICEKeepaliveInterval ICE 保活间隔（秒）
	ICEKeepaliveInterval uint16

	// EnableSimulcast 是否支持 simulcast
	EnableSimulcast bool
	// EnableTWCC 是否启用 Transport-wide Congestion Control
	EnableTWCC bool

	// PLIInterval PLI（关键帧请求）间隔
	PLIInterval time.Duration

	// NACKMaxRetry NACK 最大重试次数
	NACKMaxRetry uint16

	// DataChannelReliableLabel 可靠数据通道标签
	DataChannelReliableLabel string
	// DataChannelLossyLabel 不可靠数据通道标签
	DataChannelLossyLabel string
}

// DirectionConfig 方向配置（发布者/订阅者）
type DirectionConfig struct {
	// AudioCodecs 支持的音频编解码列表（按优先级排序）
	AudioCodecs []string
	// VideoCodecs 支持的视频编解码列表（按优先级排序）
	VideoCodecs []string
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Addr:                    ":8081",
		Path:                    "/webrtc/signal",
		ICEServers:              []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
		EphemeralUDPPortRange:   [2]uint16{50000, 60000},
		Publisher: DirectionConfig{
			AudioCodecs: []string{"opus", "pcmu", "pcma"},
			VideoCodecs: []string{"h264", "vp8", "vp9"},
		},
		Subscriber: DirectionConfig{
			AudioCodecs: []string{"opus", "pcmu", "pcma"},
			VideoCodecs: []string{"h264", "vp8", "vp9"},
		},
		ICEDisconnectedTimeout:   5,
		ICEFailedTimeout:         25,
		ICEKeepaliveInterval:     2,
		EnableSimulcast:          true,
		EnableTWCC:               true,
		PLIInterval:              3 * time.Second,
		NACKMaxRetry:             3,
		DataChannelReliableLabel: "reliable",
		DataChannelLossyLabel:    "lossy",
	}
}
