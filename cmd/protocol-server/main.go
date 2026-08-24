package main

import (
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol"
	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/LingVoice/pkg/protocol/sip"
	"github.com/LingByte/LingVoice/pkg/protocol/webrtc"
	"github.com/LingByte/LingVoice/pkg/protocol/ws"
)

// DemoEventHandler 是一个简单的事件处理器，打印所有事件和媒体帧统计
type DemoEventHandler struct {
	logger *slog.Logger
}

func (h *DemoEventHandler) OnEvent(event common.ProtocolEvent) error {
	switch event.Type {
	case common.EventIncomingCall:
		h.logger.Info(">> 来电",
			"protocol", event.Protocol,
			"session", event.SessionID,
			"from", event.From,
			"to", event.To,
		)
	case common.EventRinging:
		h.logger.Info(">> 振铃", "session", event.SessionID)
	case common.EventAnswered:
		h.logger.Info(">> 接听", "session", event.SessionID)
	case common.EventHangup:
		h.logger.Info(">> 挂断", "session", event.SessionID)
	case common.EventMediaReady:
		if event.Media != nil && event.Media.Audio != nil {
			h.logger.Info(">> 媒体就绪",
				"session", event.SessionID,
				"codec", event.Media.Audio.Codec.String(),
				"sampleRate", event.Media.Audio.SampleRate,
				"channels", event.Media.Audio.Channels,
				"frameMs", event.Media.Audio.FrameDurationMs,
			)
		} else {
			h.logger.Info(">> 媒体就绪", "session", event.SessionID)
		}
	case common.EventError:
		h.logger.Error(">> 错误", "session", event.SessionID, "error", event.Err)
	}
	return nil
}

func (h *DemoEventHandler) OnMediaFrame(sessionID string, frame common.MediaFrame) error {
	// 只打印前几帧和统计，避免日志爆炸
	h.logger.Debug("<< 媒体帧",
		"session", sessionID,
		"type", frame.Type,
		"codec", frame.Codec.String(),
		"seq", frame.Sequence,
		"ts", frame.Timestamp,
		"payloadLen", len(frame.Payload),
	)
	return nil
}

func main() {
	var (
		wsAddr      = flag.String("ws-addr", ":8080", "WebSocket 监听地址")
		wsPath      = flag.String("ws-path", "/ws/voice", "WebSocket 路径")
		sipAddr     = flag.String("sip-addr", "0.0.0.0:5060", "SIP 监听地址")
		webrtcAddr  = flag.String("webrtc-addr", ":8081", "WebRTC 信令监听地址")
		enableWS    = flag.Bool("ws", true, "启用 WebSocket 协议")
		enableSIP   = flag.Bool("sip", true, "启用 SIP 协议")
		enableRTC   = flag.Bool("webrtc", true, "启用 WebRTC 协议")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	handler := &DemoEventHandler{logger: logger}

	// 创建协议管理器
	mgr := protocol.NewManager(handler, logger)

	// 配置各协议
	if *enableWS {
		wsConfig := ws.DefaultConfig()
		wsConfig.Addr = *wsAddr
		wsConfig.Path = *wsPath
		mgr.WithWebSocket(wsConfig)
		logger.Info("WebSocket 协议已启用", "addr", *wsAddr, "path", *wsPath)
	}

	if *enableSIP {
		sipConfig := sip.DefaultConfig()
		sipConfig.Addr = *sipAddr
		sipConfig.AuthFunc = nil // 开发模式：不鉴权
		sipConfig.RouteFunc = func(to string) (string, bool) {
			logger.Info("SIP 路由查询", "to", to)
			return to, true // 默认接受所有
		}
		if _, err := mgr.WithSIP(sipConfig); err != nil {
			logger.Error("SIP 初始化失败", "error", err)
		} else {
			logger.Info("SIP 协议已启用", "addr", *sipAddr)
		}
	}

	if *enableRTC {
		rtcConfig := webrtc.DefaultConfig()
		rtcConfig.Addr = *webrtcAddr
		mgr.WithWebRTC(rtcConfig)
		logger.Info("WebRTC 协议已启用", "addr", *webrtcAddr)
	}

	// 启动
	logger.Info("LingVoice 协议层启动中...")
	if err := mgr.Start(); err != nil {
		logger.Error("启动失败", "error", err)
		os.Exit(1)
	}

	logger.Info("========================================")
	logger.Info("LingVoice 协议层已启动")
	logger.Info("========================================")
	if *enableWS {
		logger.Info("WebSocket:  ws://localhost"+*wsAddr+*wsPath)
	}
	if *enableSIP {
		logger.Info("SIP:        udp://"+*sipAddr)
	}
	if *enableRTC {
		logger.Info("WebRTC:     ws://localhost"+*webrtcAddr)
	}
	logger.Info("========================================")
	logger.Info("等待连接...")

	// 等待退出信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Info("正在关闭...")
	mgr.Close()
	time.Sleep(200 * time.Millisecond) // 让 goroutine 退出
	logger.Info("已关闭")
}
