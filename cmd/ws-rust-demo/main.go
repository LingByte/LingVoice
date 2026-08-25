// Command ws-rust-demo 启动 WebSocket 语音服务 + Rust 媒体节点，
// 演示完整的 WebSocket 音频转发 + 文本消息链路。
//
// 用法：
//
//	1. 先启动 Rust 媒体节点：
//	  cd rust-media && cargo run -p media-node
//
//	2. 再启动本 demo：
//	  go run ./cmd/ws-rust-demo
//
//	3. 用浏览器打开 http://localhost:8082
//	  - 客户端 A：麦克风录音 → WS 二进制帧 → Go → Rust → Go → WS 二进制帧 → 客户端 B 播放
//	  - 客户端 A：输入文本 → WS JSON chat → Go → 广播给同 room 所有客户端
//	  - 反向同理
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/LingByte/LingVoice/pkg/media/rustbridge"
	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/LingVoice/pkg/protocol/ws"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}

// rustHandler 实现 protocol.EventHandler，将 WS 媒体帧桥接到 Rust 媒体节点
type rustHandler struct {
	log    *zap.Logger
	bridge *rustbridge.Client
	srv    *ws.Server

	mu       sync.Mutex
	sessions map[string]*sessionState
}

type sessionState struct {
	sessionID string
	created   bool
	// per-track push/pull 管理
	tracks map[common.TrackID]*trackState
}

type trackState struct {
	kind        common.TrackKind
	codec       common.CodecType
	sampleRate  uint32
	channels    uint16
	pushStarted bool
	pullCancel  context.CancelFunc
}

func newRustHandler(log *zap.Logger, bridge *rustbridge.Client, srv *ws.Server) *rustHandler {
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
		h.log.Info(">> WS 连接",
			zap.String("session", event.SessionID),
			zap.String("from", event.From))
		// 在 Rust 创建 session，所有 participant 加入同一个 room
		ss := h.getOrCreateSession(event.SessionID)
		if !ss.created {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := h.bridge.CreateSession(ctx, event.SessionID, "ws-room", ""); err != nil {
				h.log.Error("rust CreateSession failed", zap.Error(err))
			} else {
				ss.created = true
				h.log.Info("rust session created",
					zap.String("session", event.SessionID),
					zap.String("room", "ws-room"))
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
		ts := &trackState{
			kind:       event.Track.Kind,
			codec:      event.Track.Codec,
			sampleRate: event.Track.SampleRate,
			channels:   event.Track.Channels,
		}
		ss.tracks[event.Track.ID] = ts

		// 在 Rust 注册 track
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.bridge.AddTrack(ctx, event.SessionID, "ws-ep", *event.Track); err != nil {
			h.log.Error("rust AddTrack failed", zap.Error(err))
			cancel()
			return nil
		}
		cancel()
		h.log.Info("rust track registered",
			zap.String("session", event.SessionID),
			zap.String("track", string(event.Track.ID)))

		// 开启 push stream（Go → Rust）
		pushCtx, pushCancel := context.WithCancel(context.Background())
		if err := h.bridge.StartPushRtp(pushCtx, event.SessionID, event.Track.ID); err != nil {
			h.log.Error("StartPushRtp failed", zap.Error(err))
			pushCancel()
			return nil
		}
		ts.pushStarted = true
		_ = pushCancel // push stream 靠 ClosePushRtp 关闭，cancel 留作未来用

		// 开启 pull loop（Rust → Go → WS 客户端）
		h.startPullLoop(event.SessionID, event.Track.ID, ts)

	case common.EventHangup:
		h.log.Info(">> 挂断", zap.String("session", event.SessionID))
		h.cleanupSession(event.SessionID)
	}
	return nil
}

// frameCounter 用于周期性日志（避免每帧都打日志）
var frameCounter sync.Map // sessionID → *atomic.Uint64

func (h *rustHandler) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	// 周期性日志：每 100 帧打一次
	var counter *atomic.Uint64
	if v, ok := frameCounter.Load(sessionID); ok {
		counter = v.(*atomic.Uint64)
	} else {
		counter = &atomic.Uint64{}
		frameCounter.Store(sessionID, counter)
	}
	n := counter.Add(1)
	if n%100 == 1 {
		h.log.Info("<< 媒体帧",
			zap.String("session", sessionID),
			zap.String("track", string(trackID)),
			zap.Uint64("frameNum", n),
			zap.Int("payloadLen", len(frame.Payload)),
			zap.String("codec", frame.Codec.String()))
	}

	// 将音频帧推送到 Rust 媒体节点
	if err := h.bridge.PushRtpPacket(sessionID, trackID, frame); err != nil {
		h.log.Debug("push rtp packet failed",
			zap.String("session", sessionID),
			zap.String("track", string(trackID)),
			zap.Error(err))
	}
	return nil
}

func (h *rustHandler) OnData(sessionID string, msg common.DataMessage) error {
	h.log.Info("<< 文本消息",
		zap.String("session", sessionID),
		zap.String("channel", msg.Channel),
		zap.String("data", string(msg.Data)))

	// 当前：echo 回发送者 + 广播给同 room 其他客户端
	// 未来：这里接 ASR → LLM → TTS 管线
	reply := fmt.Sprintf("[echo] %s", string(msg.Data))

	// 回复发送者
	if sess, ok := h.srv.GetSession(sessionID); ok {
		_ = sess.SendData(msg.Channel, []byte(reply))
	}

	// 广播给同 room 其他 session（文本消息群聊）
	h.mu.Lock()
	for sid := range h.sessions {
		if sid == sessionID {
			continue
		}
		if sess, ok := h.srv.GetSession(sid); ok {
			_ = sess.SendData(msg.Channel, []byte(fmt.Sprintf("[来自 %s] %s", sessionID[:8], string(msg.Data))))
		}
	}
	h.mu.Unlock()

	return nil
}

// startPullLoop 从 Rust 拉取其他 participant 的媒体，写回 WS 客户端
func (h *rustHandler) startPullLoop(sessionID string, pubTrackID common.TrackID, ts *trackState) {
	sess, ok := h.srv.GetSession(sessionID)
	if !ok {
		h.log.Error("session not found for pull loop", zap.String("session", sessionID))
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	ts.pullCancel = cancel

	var pullCount atomic.Uint64
	err := h.bridge.StartPullRtp(ctx, sessionID, pubTrackID, ts.kind, ts.codec, func(frame common.MediaFrame) error {
		n := pullCount.Add(1)
		if n%100 == 1 {
			h.log.Info(">> 拉取到媒体帧",
				zap.String("session", sessionID),
				zap.String("track", string(pubTrackID)),
				zap.Uint64("frameNum", n),
				zap.Int("payloadLen", len(frame.Payload)))
		}
		// 从 Rust 收到其他 participant 的媒体，写到 WS 客户端
		return sess.SendMediaFrame(pubTrackID, frame)
	})
	if err != nil {
		h.log.Error("StartPullRtp failed",
			zap.String("session", sessionID),
			zap.String("track", string(pubTrackID)),
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

	// 取消所有 pull loop
	for _, ts := range ss.tracks {
		if ts.pullCancel != nil {
			ts.pullCancel()
		}
	}

	// 关闭 push stream
	_ = h.bridge.ClosePushRtp(sessionID)

	// 销毁 Rust session（NotFound 是正常的，说明已清理）
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.bridge.DestroySession(ctx, sessionID); err != nil {
		// 降级为 debug：session 可能已被清理
		h.log.Debug("rust DestroySession (expected if already gone)",
			zap.String("session", sessionID),
			zap.Error(err))
	}
}

func main() {
	var (
		addr      = flag.String("addr", ":8082", "WebSocket 服务监听地址")
		path      = flag.String("path", "/ws/voice", "WebSocket 路径")
		rustAddr  = flag.String("rust", "127.0.0.1:50051", "Rust 媒体节点 gRPC 地址")
		staticDir = flag.String("static", "cmd/ws-rust-demo/static", "静态文件目录")
	)
	flag.Parse()

	_ = logger.Init(&logger.LogConfig{
		Level:    "debug",
		Filename: "logs/ws-rust-demo.log",
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

	// 创建 WS 服务
	config := ws.Config{
		Addr:            *addr,
		Path:            *path,
		AudioCodecs:     []string{"opus", "pcmu", "pcma", "pcm16"},
		AudioSampleRate: 48000,
		AudioChannels:   1,
		AudioFrameMs:    20,
	}

	handler := newRustHandler(log, bridge, nil)
	srv := ws.NewServer(config, handler, log)
	handler.srv = srv

	// 启动 HTTP 服务（WS + 静态页面）
	mux := http.NewServeMux()
	mux.Handle(*path, srv.Handler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			http.ServeFile(w, r, filepath.Join(*staticDir, "index.html"))
			return
		}
		http.FileServer(http.Dir(*staticDir)).ServeHTTP(w, r)
	})

	// 健康检查
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if _, err := bridge.HealthCheck(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "rust media node unavailable: %v\n", err)
			return
		}
		fmt.Fprintf(w, "ok\n")
	})

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
	fmt.Printf("║  LingVoice WebSocket + Rust Media Demo                  ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════╝\n")
	fmt.Printf("\n")
	fmt.Printf("  测试页面:    http://%s\n", normalizeAddr(*addr))
	fmt.Printf("  WebSocket:   ws://%s%s\n", normalizeAddr(*addr), *path)
	fmt.Printf("  Rust 媒体:   %s\n", *rustAddr)
	fmt.Printf("  Room:        ws-room\n")
	fmt.Printf("\n")
	fmt.Printf("  媒体链路 (音频):\n")
	fmt.Printf("    浏览器A → WS binary → Go → gRPC PushRtp → Rust room 路由 → gRPC PullRtp → Go → WS binary → 浏览器B\n")
	fmt.Printf("\n")
	fmt.Printf("  文本链路:\n")
	fmt.Printf("    浏览器A → WS JSON chat → Go OnData → echo + 广播 → WS JSON chat → 浏览器B\n")
	fmt.Printf("\n")
	fmt.Printf("  按 Ctrl+C 退出\n")
	fmt.Printf("\n")

	log.Info("ws-rust-demo 启动", zap.String("addr", *addr), zap.String("rust", *rustAddr))
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal("HTTP 服务失败", zap.Error(err))
	}
}
