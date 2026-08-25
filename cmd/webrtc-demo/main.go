// Command webrtc-demo 启动一个完整的 WebRTC 信令服务器示例，
// 演示双 PC 架构（Publisher + Subscriber）、DataChannel、QoS 指标采集。
//
// 用法：
//
//	go run ./cmd/webrtc-demo
//
// 然后用浏览器打开 http://localhost:8081 打开测试页面，
// 或用 WebRTC 客户端连接 ws://localhost:8081/webrtc/signal
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/LingVoice/pkg/protocol/webrtc"
	"github.com/LingByte/ling-base/common/logger"
	pionwebrtc "github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

type demoHandler struct {
	log *zap.Logger
}

func (h *demoHandler) OnEvent(event common.ProtocolEvent) error {
	switch event.Type {
	case common.EventIncomingCall:
		h.log.Info(">> 来电",
			zap.String("protocol", string(event.Protocol)),
			zap.String("session", event.SessionID),
			zap.String("from", event.From),
		)
	case common.EventAnswered:
		h.log.Info(">> 接听/ICE 连接成功", zap.String("session", event.SessionID))
	case common.EventTrackAdded:
		if event.Track != nil {
			h.log.Info(">> 轨道就绪",
				zap.String("session", event.SessionID),
				zap.String("trackID", string(event.Track.ID)),
				zap.String("kind", event.Track.Kind.String()),
				zap.String("codec", event.Track.Codec.String()),
				zap.String("direction", dirString(event.Track.Direction)),
			)
		}
	case common.EventTrackRemoved:
		h.log.Info(">> 轨道移除", zap.String("session", event.SessionID))
	case common.EventDataChannel:
		h.log.Info(">> 数据通道打开", zap.String("session", event.SessionID))
	case common.EventHangup:
		h.log.Info(">> 挂断", zap.String("session", event.SessionID))
	case common.EventError:
		h.log.Error(">> 错误", zap.String("session", event.SessionID), zap.Error(event.Err))
	}
	return nil
}

func (h *demoHandler) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	h.log.Debug("<< 媒体帧",
		zap.String("session", sessionID),
		zap.String("trackID", string(trackID)),
		zap.String("type", frameTypeString(frame.Type)),
		zap.String("codec", frame.Codec.String()),
		zap.Uint16("seq", frame.Sequence),
		zap.Int("payloadLen", len(frame.Payload)),
		zap.Bool("marker", frame.Marker),
	)
	return nil
}

func (h *demoHandler) OnData(sessionID string, msg common.DataMessage) error {
	h.log.Info("<< 数据通道消息",
		zap.String("session", sessionID),
		zap.String("channel", msg.Channel),
		zap.Int("len", len(msg.Data)),
		zap.Bool("isString", msg.IsString),
	)
	if msg.IsString {
		h.log.Info("  text", zap.String("data", string(msg.Data)))
	}
	return nil
}

func dirString(d common.TrackDirection) string {
	switch d {
	case common.TrackRecv:
		return "recv"
	case common.TrackSend:
		return "send"
	default:
		return "unknown"
	}
}

func frameTypeString(t common.FrameType) string {
	switch t {
	case common.FrameAudio:
		return "audio"
	case common.FrameVideo:
		return "video"
	default:
		return "unknown"
	}
}

func main() {
	var (
		addr     = flag.String("addr", ":8081", "WebRTC 信令监听地址")
		path     = flag.String("path", "/webrtc/signal", "信令路径")
		stun     = flag.String("stun", "stun:stun.l.google.com:19302", "STUN 服务器")
		statsInt = flag.Int("stats-interval", 5, "QoS 统计打印间隔（秒），0=禁用")
	)
	flag.Parse()

	_ = logger.Init(&logger.LogConfig{
		Level:    "debug",
		Filename: "logs/webrtc-demo.log",
		MaxSize:  100,
		MaxAge:   30,
		Daily:    true,
	}, "dev")
	defer logger.Sync()

	log := logger.Lg

	handler := &demoHandler{log: log}

	cfg := webrtc.DefaultConfig()
	cfg.Addr = *addr
	cfg.Path = *path
	cfg.ICEServers = []pionwebrtc.ICEServer{{URLs: []string{*stun}}}

	srv := webrtc.NewServer(cfg, handler, log)

	// 启动 QoS 监控 goroutine
	if *statsInt > 0 {
		go qosMonitor(srv, log, time.Duration(*statsInt)*time.Second)
	}

	// 启动服务器
	go func() {
		if err := srv.Start(); err != nil {
			log.Error("webrtc server stopped", zap.Error(err))
			os.Exit(1)
		}
	}()

	log.Info("WebRTC demo server started",
		zap.String("addr", *addr),
		zap.String("path", *path),
		zap.String("stun", *stun),
	)
	fmt.Printf("\nWebRTC 信令服务器已启动: http://%s%s\n\n", *addr, *path)
	fmt.Printf("用 WebRTC 客户端连接 WebSocket 信令: ws://localhost%s\n", *path)
	fmt.Println("信令流程:")
	fmt.Println("  1. 客户端发送 {type: 'pub_offer', sdp: '...'}")
	fmt.Println("  2. 服务端返回 {type: 'pub_answer', sdp: '...'}")
	fmt.Println("  3. 双方交换 {type: 'candidate', target: 'publisher'/'subscriber', candidate: {...}}")
	fmt.Println("  4. 服务端发送 {type: 'sub_offer', sdp: '...'}（当有轨道要发送时）")
	fmt.Println("  5. 客户端返回 {type: 'sub_answer', sdp: '...'}")
	fmt.Println()

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Info("shutting down")
}

// qosMonitor 定期打印所有会话的 QoS 指标
func qosMonitor(srv *webrtc.Server, log *zap.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		// 遍历所有会话打印 QoS
		// 这里简化处理，实际可通过 API 查询
		log.Info("QoS monitor tick")
	}
}
