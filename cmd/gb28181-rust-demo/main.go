// Command gb28181-rust-demo 启动 GB28181 SIP 监听 + PS 接收 + Rust 媒体节点，
// 演示通过 GB28181 协议接收设备推流，解析 PS 流并桥接到 Rust 媒体节点。
//
// GB28181 IPC → Go (SIP 监听) → PS 流解析 → H.264/G.711 → gRPC PushRtp → Rust
//
// 用法：
//
//  1. 先启动 Rust 媒体节点：
//     cd rust-media && cargo run -p media-node
//
//  2. 启动 demo：
//     go run ./cmd/gb28181-rust-demo
//
//  3. 用 GB28181 设备 / 模拟器向本端 5060 端口发送 REGISTER，
//     随后 INVITE 推送 PS 流。
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
	"github.com/LingByte/LingVoice/pkg/protocol/gb28181"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

// rustHandler 实现 EventHandler，将 GB28181 媒体帧桥接到 Rust
type rustHandler struct {
	log    *zap.Logger
	bridge *rustbridge.Client

	mu       sync.Mutex
	sessions map[string]*sessionState
}

type sessionState struct {
	sessionID string
	created   bool
	tracks    map[string]*trackState
}

type trackState struct {
	trackID   common.TrackID
	pushCtx   context.Context
	pushCncl  context.CancelFunc
	started   bool
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
			tracks:    make(map[string]*trackState),
		}
		h.sessions[sessionID] = ss
	}
	return ss
}

func (h *rustHandler) OnEvent(event common.ProtocolEvent) error {
	switch event.Type {
	case common.EventIncomingCall:
		h.log.Info(">> GB28181 设备注册/来电",
			zap.String("session", event.SessionID),
			zap.String("device", event.From),
			zap.String("server", event.To))

		ss := h.getOrCreateSession(event.SessionID)
		if !ss.created {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := h.bridge.CreateSession(ctx, event.SessionID, "gb28181-room", ""); err != nil {
				h.log.Error("rust CreateSession failed", zap.Error(err))
			} else {
				ss.created = true
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
		if err := h.bridge.AddTrack(ctx, event.SessionID, "gb28181-ep", *event.Track); err != nil {
			h.log.Error("rust AddTrack failed", zap.Error(err))
			cancel()
			return nil
		}
		cancel()

		// 开启 push stream（Go → Rust）
		pushCtx, pushCncl := context.WithCancel(context.Background())
		if err := h.bridge.StartPushRtp(pushCtx, event.SessionID, event.Track.ID); err != nil {
			h.log.Error("StartPushRtp failed", zap.Error(err))
			pushCncl()
			return nil
		}

		ss.tracks[string(event.Track.ID)] = &trackState{
			trackID:  event.Track.ID,
			pushCtx:  pushCtx,
			pushCncl: pushCncl,
			started:  true,
		}

	case common.EventAnswered:
		h.log.Info(">> 媒体会话建立（INVITE ACK）",
			zap.String("session", event.SessionID),
			zap.String("device", event.From))

	case common.EventHangup:
		h.log.Info(">> 挂断（BYE）",
			zap.String("session", event.SessionID),
			zap.String("device", event.From))
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
	ss, ok := h.sessions[sessionID]
	if !ok {
		h.mu.Unlock()
		return
	}
	delete(h.sessions, sessionID)
	h.mu.Unlock()

	// 取消所有 track 的 push context
	for _, ts := range ss.tracks {
		if ts.pushCncl != nil {
			ts.pushCncl()
		}
	}

	_ = h.bridge.ClosePushRtp(sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.bridge.DestroySession(ctx, sessionID); err != nil {
		h.log.Debug("rust DestroySession", zap.String("session", sessionID), zap.Error(err))
	}
}

func main() {
	var (
		addr     = flag.String("addr", ":5060", "GB28181 SIP 监听地址")
		serverID = flag.String("id", "34020000002000000001", "本端 SIP server ID")
		rustAddr = flag.String("rust", "127.0.0.1:50051", "Rust 媒体节点 gRPC 地址")
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

	// 连接 Rust 媒体节点
	bridge, err := rustbridge.NewClient(*rustAddr, log)
	if err != nil {
		log.Fatal("连接 Rust 媒体节点失败", zap.String("rust", *rustAddr), zap.Error(err))
	}
	defer bridge.Close()

	config := gb28181.Config{
		Addr:     *addr,
		ServerID: *serverID,
		Realm:    "3402000000",
	}

	handler := newRustHandler(log, bridge)
	srv := gb28181.NewServer(config, handler, log)

	if err := srv.Start(); err != nil {
		log.Fatal("GB28181 SIP 服务启动失败", zap.Error(err))
	}

	// 信号处理
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Printf("\n")
	fmt.Printf("╔══════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║  LingVoice GB28181 + Rust Media Demo                     ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════╝\n")
	fmt.Printf("\n")
	fmt.Printf("  SIP 监听:    udp%s\n", *addr)
	fmt.Printf("  Server ID:   %s\n", *serverID)
	fmt.Printf("  Rust 媒体:   %s\n", *rustAddr)
	fmt.Printf("  Room:        gb28181-room\n")
	fmt.Printf("\n")
	fmt.Printf("  媒体链路 (推流):\n")
	fmt.Printf("    IPC → SIP REGISTER → Go 注册设备\n")
	fmt.Printf("    IPC → SIP INVITE (SDP) → Go 分配 PS 端口 → 200 OK\n")
	fmt.Printf("    IPC → UDP PS 流 → Go 解析 PES → H.264/G.711 → gRPC PushRtp → Rust\n")
	fmt.Printf("\n")
	fmt.Printf("  测试方法:\n")
	fmt.Printf("    用 GB28181 设备/模拟器向本端发送 REGISTER + INVITE\n")
	fmt.Printf("\n")
	fmt.Printf("  按 Ctrl+C 退出\n")
	fmt.Printf("\n")

	log.Info("gb28181-rust-demo 启动",
		zap.String("addr", *addr),
		zap.String("server-id", *serverID),
		zap.String("rust", *rustAddr))

	<-sigCh
	log.Info("收到退出信号，正在关闭...")
	_ = srv.Close()
}
