// Command gb28181-rust-demo 启动 GB28181 SIP 监听 + PS 接收，
// 演示通过 GB28181 协议接收设备推流并解析 PS 流。
//
// GB28181 IPC → Go (SIP 监听) → PS 流解析 → H.264 NALU 打印。
// 当前只实现接收端（监听模式）。
//
// 用法：
//
//  1. 启动 demo：
//     go run ./cmd/gb28181-rust-demo
//
//  2. 用 GB28181 设备 / 模拟器向本端 5060 端口发送 REGISTER，
//     随后 INVITE 推送 PS 流。
//
//  3. demo 会打印设备注册、INVITE 协商、PS NALU 信息。
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/LingVoice/pkg/protocol/gb28181"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

// demoHandler 实现 EventHandler，打印 GB28181 事件与媒体帧
type demoHandler struct {
	log *zap.Logger
}

func (h *demoHandler) OnEvent(event common.ProtocolEvent) error {
	switch event.Type {
	case common.EventIncomingCall:
		h.log.Info(">> GB28181 设备注册/来电",
			zap.String("session", event.SessionID),
			zap.String("device", event.From),
			zap.String("server", event.To))

	case common.EventTrackAdded:
		if event.Track == nil {
			return nil
		}
		h.log.Info(">> 轨道就绪",
			zap.String("session", event.SessionID),
			zap.String("track", string(event.Track.ID)),
			zap.String("kind", event.Track.Kind.String()),
			zap.String("codec", event.Track.Codec.String()))

	case common.EventAnswered:
		h.log.Info(">> 媒体会话建立（INVITE ACK）",
			zap.String("session", event.SessionID),
			zap.String("device", event.From))

	case common.EventHangup:
		h.log.Info(">> 挂断（BYE）",
			zap.String("session", event.SessionID),
			zap.String("device", event.From))
	}
	return nil
}

func (h *demoHandler) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	switch frame.Type {
	case common.FrameVideo:
		// 打印 H.264 NALU 信息
		naluType := gb28181.NaluType(frame.Payload)
		naluName := gb28181.NaluTypeName(frame.Payload)
		h.log.Info(">> 收到视频帧",
			zap.String("session", sessionID),
			zap.String("track", string(trackID)),
			zap.String("nalu", naluName),
			zap.Uint8("nalu_type", naluType),
			zap.Int("len", len(frame.Payload)),
			zap.Uint32("ts", frame.Timestamp),
			zap.Bool("marker", frame.Marker))
	case common.FrameAudio:
		h.log.Info(">> 收到音频帧",
			zap.String("session", sessionID),
			zap.String("track", string(trackID)),
			zap.String("codec", frame.Codec.String()),
			zap.Int("len", len(frame.Payload)),
			zap.Uint32("ts", frame.Timestamp))
	}
	return nil
}

func (h *demoHandler) OnData(sessionID string, msg common.DataMessage) error {
	return nil
}

func main() {
	var (
		addr     = flag.String("addr", ":5060", "GB28181 SIP 监听地址")
		serverID = flag.String("id", "34020000002000000001", "本端 SIP server ID")
	)
	flag.Parse()

	_ = logger.Init(&logger.LogConfig{
		Level:    "debug",
		Filename: "logs/gb28181-rust-demo.log",
		MaxSize:  100,
		MaxAge:   30,
		Daily:    true,
	}, "dev")
	defer logger.Sync()

	log := logger.Lg

	config := gb28181.Config{
		Addr:     *addr,
		ServerID: *serverID,
		Realm:    "3402000000",
	}

	handler := &demoHandler{log: log}
	srv := gb28181.NewServer(config, handler, log)

	if err := srv.Start(); err != nil {
		log.Fatal("GB28181 SIP 服务启动失败", zap.Error(err))
	}

	// 信号处理
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Printf("\n")
	fmt.Printf("╔══════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║  LingVoice GB28181 SIP + PS Demo                         ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════╝\n")
	fmt.Printf("\n")
	fmt.Printf("  SIP 监听:    udp%s\n", *addr)
	fmt.Printf("  Server ID:   %s\n", *serverID)
	fmt.Printf("\n")
	fmt.Printf("  媒体链路 (推流):\n")
	fmt.Printf("    IPC → SIP REGISTER → Go 注册设备\n")
	fmt.Printf("    IPC → SIP INVITE (SDP) → Go 分配 PS 端口 → 200 OK\n")
	fmt.Printf("    IPC → UDP PS 流 → Go 解析 PES → H.264 NALU\n")
	fmt.Printf("\n")
	fmt.Printf("  测试方法:\n")
	fmt.Printf("    用 GB28181 设备/模拟器向本端发送 REGISTER + INVITE\n")
	fmt.Printf("\n")
	fmt.Printf("  按 Ctrl+C 退出\n")
	fmt.Printf("\n")

	log.Info("gb28181-rust-demo 启动", zap.String("addr", *addr), zap.String("server-id", *serverID))

	<-sigCh
	log.Info("收到退出信号，正在关闭...")
	_ = srv.Close()
}
