// Command webrtc-rust-demo 启动 WebRTC 信令服务器 + Rust 媒体节点，
// 演示完整的 A→Rust→B 音频转发链路。
//
// 用法：
//
//	1. 先启动 Rust 媒体节点：
//	  cd rust-media && cargo run -p media-node
//
//	2. 再启动本 demo：
//	  go run ./cmd/webrtc-rust-demo
//
//	3. 用两个浏览器打开 http://localhost:8081：
//	  - 浏览器 A：发布音频（麦克风）
//	  - 浏览器 B：订阅音频（耳机）
//	  - A 的音频 → Go/Pion → gRPC PushRtp → Rust → gRPC PullRtp → Go/Pion → B
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/LingByte/LingVoice/pkg/media/rustbridge"
	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/LingVoice/pkg/protocol/webrtc"
	"github.com/LingByte/ling-base/common/logger"
	mediav1 "github.com/LingByte/LingVoice/proto/media/v1"
	pionwebrtc "github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}

// rustHandler 实现 protocol.EventHandler，将媒体帧桥接到 Rust 媒体节点
type rustHandler struct {
	log    *zap.Logger
	bridge *rustbridge.Client
	srv    *webrtc.Server

	// 跟踪每个 session 对应的 Rust session 状态
	mu       sync.Mutex
	sessions map[string]*sessionState
}

type sessionState struct {
	sessionID string
	created   bool
	// per-track push/pull 管理（支持音频+视频多 track）
	tracks map[common.TrackID]*trackState
}

type trackState struct {
	kind        common.TrackKind
	codec       common.CodecType
	pushStarted bool
	pushCancel  context.CancelFunc
	pullStarted bool
	pullCancel  context.CancelFunc
	subTrackID  common.TrackID // subscriber 侧对应的 track（用于写回）
}

func newRustHandler(log *zap.Logger, bridge *rustbridge.Client, srv *webrtc.Server) *rustHandler {
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

func (h *rustHandler) getOrCreateTrack(ss *sessionState, trackID common.TrackID, kind common.TrackKind, codec common.CodecType) *trackState {
	ts, ok := ss.tracks[trackID]
	if !ok {
		ts = &trackState{kind: kind, codec: codec}
		ss.tracks[trackID] = ts
	}
	return ts
}

func (h *rustHandler) OnEvent(event common.ProtocolEvent) error {
	switch event.Type {
	case common.EventIncomingCall:
		h.log.Info(">> 来电",
			zap.String("session", event.SessionID),
			zap.String("from", event.From))
		// 在 Rust 创建 session，所有 participant 加入同一个 room
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

	case common.EventAnswered:
		h.log.Info(">> ICE 连接成功", zap.String("session", event.SessionID))

	case common.EventTrackAdded:
		if event.Track != nil && event.Track.Direction == common.TrackRecv {
			// Publisher 的轨道就绪（音频或视频）
			kind := event.Track.Kind
			if kind != common.TrackAudio && kind != common.TrackVideo {
				break
			}

			ss := h.getOrCreateSession(event.SessionID)
			ts := h.getOrCreateTrack(ss, event.Track.ID, kind, event.Track.Codec)

			h.log.Info(">> 发布者轨道就绪",
				zap.String("session", event.SessionID),
				zap.String("trackID", string(event.Track.ID)),
				zap.String("kind", kind.String()),
				zap.String("codec", event.Track.Codec.String()))

			// 在 Rust 注册 track
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := h.bridge.AddTrack(ctx, event.SessionID, event.SessionID, *event.Track); err != nil {
				h.log.Error("rust AddTrack failed", zap.Error(err))
			}
			cancel()

			// 启动 PushRtp 流（把自己的媒体推给 Rust）
			if !ts.pushStarted {
				ctx2, cancel2 := context.WithCancel(context.Background())
				if err := h.bridge.StartPushRtp(ctx2, event.SessionID, event.Track.ID); err != nil {
					h.log.Error("rust StartPushRtp failed", zap.Error(err))
				} else {
					ts.pushStarted = true
					ts.pushCancel = cancel2
					h.log.Info("rust push rtp stream started",
						zap.String("session", event.SessionID),
						zap.String("track", string(event.Track.ID)),
						zap.String("kind", kind.String()))
				}
			}

			// 启动 PullRtp 流（从 Rust 拉同 room 其他人的同 kind 媒体）
			if !ts.pullStarted {
				ts.pullStarted = true
				go h.startPullLoop(event.SessionID, event.Track.ID, kind, event.Track.Codec)
			}
		}

	case common.EventTrackRemoved:
		h.log.Info(">> 轨道移除", zap.String("session", event.SessionID))

	case common.EventHangup:
		h.log.Info(">> 挂断", zap.String("session", event.SessionID))
		// 取消所有 track 的 push/pull
		h.mu.Lock()
		if ss, ok := h.sessions[event.SessionID]; ok {
			for _, ts := range ss.tracks {
				if ts.pushCancel != nil {
					ts.pushCancel()
				}
				if ts.pullCancel != nil {
					ts.pullCancel()
				}
			}
			delete(h.sessions, event.SessionID)
		}
		h.mu.Unlock()
		// 清理 Rust session
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.bridge.DestroySession(ctx, event.SessionID); err != nil {
			h.log.Error("rust DestroySession failed", zap.Error(err))
		}
		cancel()

	case common.EventError:
		h.log.Error(">> 错误", zap.String("session", event.SessionID), zap.Error(event.Err))
	}
	return nil
}

func (h *rustHandler) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	// 将 RTP 帧推送到 Rust 媒体节点
	if err := h.bridge.PushRtpPacket(sessionID, trackID, frame); err != nil {
		// 非致命：偶尔推送失败不中断
		h.log.Debug("push rtp packet failed",
			zap.String("session", sessionID),
			zap.Error(err))
	}
	return nil
}

func (h *rustHandler) OnData(sessionID string, msg common.DataMessage) error {
	h.log.Info("<< 数据通道消息",
		zap.String("session", sessionID),
		zap.String("channel", msg.Channel),
		zap.Int("len", len(msg.Data)))
	return nil
}

// startPullLoop 为每个 track 启动 PullRtp 流，将 Rust 转发的其他 participant 媒体写回 subscriber。
//
// 流程：
//  1. 给本 session 的 subscriber 添加一个同 kind 的 track（音频或视频）
//  2. 从 Rust pull 自己的 publisher track（Rust 会把同 room 其他人的包转发到这里）
//  3. 收到包后通过 SendMediaFrame 写到 subscriber track
func (h *rustHandler) startPullLoop(sessionID string, pubTrackID common.TrackID, kind common.TrackKind, codec common.CodecType) {
	if h.srv == nil {
		h.log.Error("srv not set, cannot start pull loop")
		return
	}

	sess, ok := h.srv.GetSession(sessionID)
	if !ok {
		h.log.Error("session not found for pull loop", zap.String("session", sessionID))
		return
	}

	// 根据 kind 构造 subscriber track 配置
	trackCfg := common.TrackConfig{
		Kind:     kind,
		Codec:    codec,
		Label:    fmt.Sprintf("sub-%s-%s", kind.String(), pubTrackID),
		StreamID: sessionID,
	}
	if kind == common.TrackAudio {
		trackCfg.SampleRate = 48000
		trackCfg.Channels = 2
	} else {
		// 视频固定 90kHz clock rate（所有标准 WebRTC 视频编解码器一致）
		trackCfg.SampleRate = 90000
	}

	// 给 subscriber 添加一个 track（浏览器会收到这个 track 的媒体）
	subTrackID, err := sess.AddTrack(trackCfg)
	if err != nil {
		h.log.Error("add subscriber track failed",
			zap.String("session", sessionID),
			zap.String("kind", kind.String()),
			zap.Error(err))
		return
	}

	// 记录 subTrackID
	ss := h.getOrCreateSession(sessionID)
	if ts, ok := ss.tracks[pubTrackID]; ok {
		ts.subTrackID = subTrackID
	}

	h.log.Info("subscriber track added for pull",
		zap.String("session", sessionID),
		zap.String("kind", kind.String()),
		zap.String("subTrackID", string(subTrackID)),
		zap.String("pubTrackID", string(pubTrackID)))

	// 从 Rust pull 自己的 publisher track
	// Rust 把同 room 其他 session push 的包转发到本 session track 的 broadcast
	ctx, cancel := context.WithCancel(context.Background())
	if ts, ok := ss.tracks[pubTrackID]; ok {
		ts.pullCancel = cancel
	}

	err = h.bridge.StartPullRtp(ctx, sessionID, pubTrackID, kind, codec, func(frame common.MediaFrame) error {
		// 从 Rust 收到其他 participant 的 RTP，写到 subscriber track
		return sess.SendMediaFrame(subTrackID, frame)
	})
	if err != nil {
		h.log.Error("StartPullRtp failed",
			zap.String("session", sessionID),
			zap.String("kind", kind.String()),
			zap.Error(err))
	}
}

func main() {
	var (
		addr       = flag.String("addr", ":8081", "WebRTC 信令监听地址")
		path       = flag.String("path", "/webrtc/signal", "信令路径")
		stun       = flag.String("stun", "stun:stun.l.google.com:19302", "STUN 服务器")
		rustAddr   = flag.String("rust", "localhost:50051", "Rust 媒体节点 gRPC 地址")
		statsInt   = flag.Int("stats-interval", 5, "QoS 统计打印间隔（秒），0=禁用")
	)
	flag.Parse()

	_ = logger.Init(&logger.LogConfig{
		Level:    "debug",
		Filename: "logs/webrtc-rust-demo.log",
		MaxSize:  100,
		MaxAge:   30,
		Daily:    true,
	}, "dev")
	defer logger.Sync()

	log := logger.Lg

	// 1. 连接 Rust 媒体节点
	log.Info("connecting to rust media node", zap.String("addr", *rustAddr))
	bridge, err := rustbridge.NewClient(*rustAddr, log)
	if err != nil {
		log.Fatal("failed to connect rust media node", zap.Error(err))
	}
	defer bridge.Close()

	// 等待 Rust 节点就绪
	if err := bridge.WaitForReady(10 * time.Second); err != nil {
		log.Fatal("rust media node not ready", zap.Error(err))
	}
	log.Info("connected to rust media node")

	// 2. 创建 WebRTC 服务器
	// 注意：handler 和 srv 循环依赖，先创建 handler 再回填 srv
	handler := newRustHandler(log, bridge, nil)

	cfg := webrtc.DefaultConfig()
	cfg.Addr = *addr
	cfg.Path = *path
	cfg.ICEServers = []pionwebrtc.ICEServer{{URLs: []string{*stun}}}

	srv := webrtc.NewServer(cfg, handler, log)
	handler.srv = srv

	// 3. 启动 QoS 监控
	if *statsInt > 0 {
		go qosMonitor(srv, bridge, log, time.Duration(*statsInt)*time.Second)
	}

	// 4. 启动 WebRTC 服务器
	go func() {
		if err := srv.Start(); err != nil {
			log.Error("webrtc server stopped", zap.Error(err))
			os.Exit(1)
		}
	}()

	// 5. 启动事件订阅（从 Rust 接收 VAD/DTMF 等事件）
	go func() {
		ctx := context.Background()
		if err := bridge.StartEvents(ctx, "", func(event *mediav1.MediaEvent) {
			log.Info("rust event",
				zap.String("session", event.SessionId),
				zap.String("type", fmt.Sprintf("%T", event.Event)))
		}); err != nil {
			log.Warn("StartEvents failed", zap.Error(err))
		}
	}()

	log.Info("WebRTC + Rust demo server started")
	fmt.Printf("\n")
	fmt.Printf("========================================\n")
	fmt.Printf("  LingVoice WebRTC + Rust Media Demo\n")
	fmt.Printf("========================================\n")
	fmt.Printf("\n")
	fmt.Printf("  WebRTC 信令: http://%s\n", normalizeAddr(*addr))
	fmt.Printf("  WebSocket:   ws://%s%s\n", normalizeAddr(*addr), *path)
	fmt.Printf("  Rust 媒体:   %s\n", *rustAddr)
	fmt.Printf("  STUN:        %s\n", *stun)
	fmt.Printf("\n")
	fmt.Printf("  媒体链路 (音频+视频):\n")
	fmt.Printf("    浏览器A → Go/Pion → gRPC PushRtp → Rust room 路由 → gRPC PullRtp → Go/Pion → 浏览器B\n")
	fmt.Printf("\n")
	fmt.Printf("  按 Ctrl+C 退出\n")
	fmt.Printf("\n")

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Info("shutting down")
}

func qosMonitor(srv *webrtc.Server, bridge *rustbridge.Client, log *zap.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		count := 0
		srv.RangeSessions(func(sess *webrtc.Session) bool {
			count++
			stats := sess.QoS()
			log.Info("QoS",
				zap.String("session", sess.ID()),
				zap.Duration("duration", stats.Duration),
				zap.Int("tracks", len(stats.Tracks)),
			)
			return true
		})
		if count > 0 {
			log.Info("QoS monitor", zap.Int("activeSessions", count))
		}
	}
}
