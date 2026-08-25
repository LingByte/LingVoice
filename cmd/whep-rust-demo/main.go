// Command whep-rust-demo 启动 WHEP 拉流服务 + Rust 媒体节点，
// 演示通过 WHEP 协议从 Rust 媒体节点拉取音频流。
//
// WHEP 是纯拉流协议（server → client），不需要客户端推流。
// 媒体来源：同 room 内其他协议（如 WebRTC/WS）推入的音频，
// Rust 路由后通过 PullRtp → Go → WHEP → 浏览器播放。
//
// 用法：
//
//	1. 先启动 Rust 媒体节点：
//	  cd rust-media && cargo run -p media-node
//
//	2. 启动 WHEP demo：
//	  go run ./cmd/whep-rust-demo
//
//	3. 用浏览器或 VLC 拉流：
//	  http://localhost:8083/whep
//	  (POST SDP offer，详见 testpage.html)
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
	"github.com/LingByte/LingVoice/pkg/protocol/whep"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}

// rustHandler 实现 EventHandler，将 Rust 媒体通过 WHEP 推给客户端
type rustHandler struct {
	log    *zap.Logger
	bridge *rustbridge.Client
	srv    *whep.Server

	mu       sync.Mutex
	sessions map[string]*sessionState
}

type sessionState struct {
	sessionID string
	created   bool
	tracks    map[common.TrackID]*trackState
}

type trackState struct {
	kind       common.TrackKind
	codec      common.CodecType
	pullCancel context.CancelFunc
}

func newRustHandler(log *zap.Logger, bridge *rustbridge.Client, srv *whep.Server) *rustHandler {
	return &rustHandler{
		log:      log,
		bridge:   bridge,
		srv:      srv,
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
		h.log.Info(">> WHEP 连接",
			zap.String("session", event.SessionID),
			zap.String("from", event.From))
		// 在 Rust 创建 session，加入 room（与其他推流者同 room）
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

		// 在 Rust 注册 track（WHEP 是发送方向，但 Rust 需要注册 track 才能 pull）
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.bridge.AddTrack(ctx, event.SessionID, "whep-ep", *event.Track); err != nil {
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

		// WHEP 是纯拉流：只开 PullRtp，不需要 PushRtp
		h.startPullLoop(event.SessionID, event.Track.ID, ts)

	case common.EventHangup:
		h.log.Info(">> 挂断", zap.String("session", event.SessionID))
		h.cleanupSession(event.SessionID)
	}
	return nil
}

// WHEP 是纯拉流协议，不接收客户端媒体
func (h *rustHandler) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	return nil
}

// WHEP 无数据通道
func (h *rustHandler) OnData(sessionID string, msg common.DataMessage) error {
	return nil
}

// startPullLoop 从 Rust 拉取其他 participant 的媒体，通过 WHEP 推给客户端
func (h *rustHandler) startPullLoop(sessionID string, trackID common.TrackID, ts *trackState) {
	sess, ok := h.srv.GetSession(sessionID)
	if !ok {
		h.log.Error("session not found for pull loop", zap.String("session", sessionID))
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	ts.pullCancel = cancel

	err := h.bridge.StartPullRtp(ctx, sessionID, trackID, ts.kind, ts.codec, func(frame common.MediaFrame) error {
		// 从 Rust 收到其他 participant 的媒体，通过 WHEP 推给客户端
		return sess.SendMediaFrame(trackID, frame)
	})
	if err != nil {
		h.log.Error("StartPullRtp failed",
			zap.String("session", sessionID),
			zap.String("track", string(trackID)),
			zap.Error(err))
	}
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

	for _, ts := range ss.tracks {
		if ts.pullCancel != nil {
			ts.pullCancel()
		}
	}

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
		addr     = flag.String("addr", ":8083", "WHEP 服务监听地址")
		path     = flag.String("path", "/whep", "WHEP endpoint 路径")
		rustAddr = flag.String("rust", "127.0.0.1:50051", "Rust 媒体节点 gRPC 地址")
	)
	flag.Parse()

	_ = logger.Init(&logger.LogConfig{
		Level:    "debug",
		Filename: "logs/whep-rust-demo.log",
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

	// 创建 WHEP 服务
	config := whep.DefaultConfig()
	config.Addr = *addr
	config.Path = *path

	handler := newRustHandler(log, bridge, nil)
	srv := whep.NewServer(config, handler, log)
	handler.srv = srv

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
	fmt.Printf("║  LingVoice WHEP + Rust Media Demo                        ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════╝\n")
	fmt.Printf("\n")
	fmt.Printf("  WHEP 端点:   http://%s%s\n", normalizeAddr(*addr), *path)
	fmt.Printf("  Rust 媒体:   %s\n", *rustAddr)
	fmt.Printf("  Room:        demo-room (与其他推流者共享)\n")
	fmt.Printf("\n")
	fmt.Printf("  媒体链路 (拉流):\n")
	fmt.Printf("    其他协议推流 → Rust room 路由 → gRPC PullRtp → Go → WHEP → 浏览器播放\n")
	fmt.Printf("\n")
	fmt.Printf("  测试方法:\n")
	fmt.Printf("    1. 先用 webrtc-rust-demo 或 ws-rust-demo 推流到 demo-room\n")
	fmt.Printf("    2. 用浏览器访问 WHEP 端点拉流\n")
	fmt.Printf("\n")
	fmt.Printf("  按 Ctrl+C 退出\n")
	fmt.Printf("\n")

	log.Info("whep-rust-demo 启动", zap.String("addr", *addr), zap.String("rust", *rustAddr))
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatal("HTTP 服务失败", zap.Error(err))
	}
}
