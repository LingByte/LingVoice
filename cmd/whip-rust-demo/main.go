// Command whip-rust-demo 启动 WHIP 推流服务 + Rust 媒体节点，
// 演示通过 WHIP 协议接收浏览器推流并转发到 Rust 媒体节点。
//
// WHIP 是纯推流协议（client → server），不向客户端回推媒体。
// 推入的音频通过 Rust room 路由，可被同 room 的 WHEP/WS/WebRTC 客户端拉取。
//
// 用法：
//
//	1. 先启动 Rust 媒体节点：
//	  cd rust-media && cargo run -p media-node
//
//	2. 启动 WHIP demo：
//	  go run ./cmd/whip-rust-demo
//
//	3. 用浏览器 WHIP 推流（POST SDP offer 到 http://localhost:8084/whip）
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/LingByte/LingVoice/pkg/media/rustbridge"
	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/LingVoice/pkg/protocol/whip"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}

// rustHandler 实现 EventHandler，将 WHIP 推流桥接到 Rust 媒体节点
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
		h.log.Info(">> WHIP 连接",
			zap.String("session", event.SessionID),
			zap.String("from", event.From))
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
		if err := h.bridge.AddTrack(ctx, event.SessionID, "whip-ep", *event.Track); err != nil {
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

		// WHIP 是纯推流协议，不需要 PullRtp

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
		addr     = flag.String("addr", ":8084", "WHIP 服务监听地址")
		path     = flag.String("path", "/whip", "WHIP endpoint 路径")
		rustAddr = flag.String("rust", "127.0.0.1:50051", "Rust 媒体节点 gRPC 地址")
	)
	flag.Parse()

	_ = logger.Init(&logger.LogConfig{
		Level:    "debug",
		Filename: "logs/whip-rust-demo.log",
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

	// 创建 WHIP 服务
	config := whip.DefaultConfig()
	config.Addr = *addr
	config.Path = *path

	handler := newRustHandler(log, bridge)
	srv := whip.NewServer(config, handler, log)

	// 信号处理
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Info("收到退出信号，正在关闭...")
		os.Exit(0)
	}()

	fmt.Printf("\n")
	fmt.Printf("╔══════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║  LingVoice WHIP + Rust Media Demo                        ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════╝\n")
	fmt.Printf("\n")
	fmt.Printf("  WHIP 端点:   http://%s%s\n", normalizeAddr(*addr), *path)
	fmt.Printf("  Rust 媒体:   %s\n", *rustAddr)
	fmt.Printf("  Room:        demo-room (与 WHEP/WS/WebRTC 共享)\n")
	fmt.Printf("\n")
	fmt.Printf("  媒体链路 (推流):\n")
	fmt.Printf("    浏览器 → WHIP POST SDP → Pion 接收 → OnTrack → ReadRTP\n")
	fmt.Printf("    → Go OnMediaFrame → gRPC PushRtp → Rust room 路由\n")
	fmt.Printf("    → 其他协议客户端可拉取\n")
	fmt.Printf("\n")
	fmt.Printf("  测试方法:\n")
	fmt.Printf("    1. 用浏览器 WHIP 推流到本端点\n")
	fmt.Printf("    2. 用 whep-rust-demo 拉流验证\n")
	fmt.Printf("\n")
	fmt.Printf("  按 Ctrl+C 退出\n")
	fmt.Printf("\n")

	log.Info("whip-rust-demo 启动", zap.String("addr", *addr), zap.String("rust", *rustAddr))
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatal("HTTP 服务失败", zap.Error(err))
	}
}
