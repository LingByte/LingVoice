// Command srt-rust-demo 启动 SRT 接收服务 + Rust 媒体节点，
// 演示通过 SRT 协议接收推流并转发到 Rust 媒体节点。
//
// SRT caller → Go (listener) → Rust room 路由 → 其他协议客户端拉取。
// 当前只实现接收端（推流入站）。
//
// 用法：
//
//	1. 先启动 Rust 媒体节点：
//	  cd rust-media && cargo run -p media-node
//
//	2. 启动 SRT demo：
//	  go run ./cmd/srt-rust-demo
//
//	3. 用 FFmpeg 推流（TS-over-SRT）：
//	  ffmpeg -re -i test.mp4 -c copy -f mpegts 'srt://localhost:9000?streamid=test'
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/LingByte/LingVoice/pkg/media/rustbridge"
	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/LingVoice/pkg/protocol/srt"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

// rustHandler 实现 EventHandler，将 SRT 推流桥接到 Rust 媒体节点
type rustHandler struct {
	log    *zap.Logger
	bridge *rustbridge.Client

	mu       sync.Mutex
	sessions map[string]*sessionState
}

type sessionState struct {
	sessionID string
	created   bool
	tracks    map[common.TrackID]*trackState
}

type trackState struct {
	kind        common.TrackKind
	codec       common.CodecType
	pushStarted bool
}

func newRustHandler(log *zap.Logger, bridge *rustbridge.Client) *rustHandler {
	return &rustHandler{
		log:      log,
		bridge:   bridge,
		sessions: make(map[string]*sessionState),
	}
}

func (h *rustHandler) getOrCreateSession(sessionID string) *sessionState {
	h.mu.Lock()
	defer h.mu.Unlock()
	ss, ok := h.sessions[sessionID]
	if !ok {
		ss = &sessionState{
			sessionID: sessionID,
			tracks:    make(map[common.TrackID]*trackState),
		}
		h.sessions[sessionID] = ss
	}
	return ss
}

func (h *rustHandler) OnEvent(event common.ProtocolEvent) error {
	switch event.Type {
	case common.EventIncomingCall:
		h.log.Info(">> SRT 连接",
			zap.String("session", event.SessionID),
			zap.String("from", event.From),
			zap.String("stream", event.To))
		ss := h.getOrCreateSession(event.SessionID)
		if !ss.created {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := h.bridge.CreateSession(ctx, event.SessionID, "demo-room", ""); err != nil {
				h.log.Error("rust CreateSession failed", zap.Error(err))
			} else {
				ss.created = true
				h.log.Info("rust session created",
					zap.String("session", event.SessionID),
					zap.String("room", "demo-room"))
			}
			cancel()
		}

	case common.EventTrackAdded:
		if event.Track == nil {
			return nil
		}
		h.log.Info(">> 轨道就绪",
			zap.String("session", event.SessionID),
			zap.String("track", string(event.Track.ID)),
			zap.String("kind", event.Track.Kind.String()),
			zap.String("codec", event.Track.Codec.String()))

		ss := h.getOrCreateSession(event.SessionID)

		// 在 Rust 注册 track
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.bridge.AddTrack(ctx, event.SessionID, "srt-ep", *event.Track); err != nil {
			h.log.Error("rust AddTrack failed", zap.Error(err))
			cancel()
			return nil
		}
		cancel()

		ts := &trackState{
			kind:  event.Track.Kind,
			codec: event.Track.Codec,
		}
		ss.tracks[event.Track.ID] = ts

		// 开启 push stream（Go → Rust）
		pushCtx, _ := context.WithCancel(context.Background())
		if err := h.bridge.StartPushRtp(pushCtx, event.SessionID, event.Track.ID); err != nil {
			h.log.Error("StartPushRtp failed", zap.Error(err))
			return nil
		}
		ts.pushStarted = true

	case common.EventAnswered:
		h.log.Info(">> 握手完成", zap.String("session", event.SessionID))

	case common.EventHangup:
		h.log.Info(">> 挂断", zap.String("session", event.SessionID))
		h.cleanupSession(event.SessionID)
	}
	return nil
}

func (h *rustHandler) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	if err := h.bridge.PushRtpPacket(sessionID, trackID, frame); err != nil {
		h.log.Debug("push rtp packet failed",
			zap.String("session", sessionID),
			zap.String("track", string(trackID)),
			zap.Error(err))
	}
	return nil
}

func (h *rustHandler) OnData(sessionID string, msg common.DataMessage) error {
	return nil
}

func (h *rustHandler) cleanupSession(sessionID string) {
	h.mu.Lock()
	if _, ok := h.sessions[sessionID]; !ok {
		h.mu.Unlock()
		return
	}
	delete(h.sessions, sessionID)
	h.mu.Unlock()

	_ = h.bridge.ClosePushRtp(sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.bridge.DestroySession(ctx, sessionID); err != nil {
		h.log.Debug("rust DestroySession",
			zap.String("session", sessionID),
			zap.Error(err))
	}
}

func main() {
	var (
		addr     = flag.String("addr", ":9000", "SRT 服务监听地址")
		rustAddr = flag.String("rust", "127.0.0.1:50051", "Rust 媒体节点 gRPC 地址")
	)
	flag.Parse()

	_ = logger.Init(&logger.LogConfig{
		Level:    "debug",
		Filename: "logs/srt-rust-demo.log",
		MaxSize:  100,
		MaxAge:   30,
		Daily:    true,
	}, "dev")
	defer logger.Sync()

	log := logger.Lg

	// 连接 Rust 媒体节点
	bridge, err := rustbridge.NewClient(*rustAddr, log)
	if err != nil {
		log.Fatal("连接 Rust 媒体节点失败", zap.String("rust", *rustAddr), zap.Error(err))
	}
	defer bridge.Close()

	// 创建 SRT 服务
	config := srt.Config{Addr: *addr}

	handler := newRustHandler(log, bridge)
	srv := srt.NewServer(config, handler, log)

	// 启动 SRT 服务
	if err := srv.Start(); err != nil {
		log.Fatal("SRT 服务启动失败", zap.Error(err))
	}

	// 信号处理
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Printf("\n")
	fmt.Printf("╔══════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║  LingVoice SRT + Rust Media Demo                         ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════╝\n")
	fmt.Printf("\n")
	fmt.Printf("  SRT 端点:    srt://localhost%s?streamid={streamKey}\n", *addr)
	fmt.Printf("  Rust 媒体:   %s\n", *rustAddr)
	fmt.Printf("  Room:        demo-room\n")
	fmt.Printf("\n")
	fmt.Printf("  媒体链路 (推流):\n")
	fmt.Printf("    FFmpeg/OBS → SRT HSv5 握手 → Go 解析 TS/RTP\n")
	fmt.Printf("    → gRPC PushRtp → Rust room 路由 → 其他协议拉取\n")
	fmt.Printf("\n")
	fmt.Printf("  测试方法:\n")
	fmt.Printf("    ffmpeg -re -i test.mp4 -c copy -f mpegts 'srt://localhost:9000?streamid=test'\n")
	fmt.Printf("\n")
	fmt.Printf("  按 Ctrl+C 退出\n")
	fmt.Printf("\n")

	log.Info("srt-rust-demo 启动", zap.String("addr", *addr), zap.String("rust", *rustAddr))

	<-sigCh
	log.Info("收到退出信号，正在关闭...")
	_ = srv.Close()
}
