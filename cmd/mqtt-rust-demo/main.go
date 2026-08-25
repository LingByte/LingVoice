// Command mqtt-rust-demo 启动 MQTT 语音服务 + Rust 媒体节点，
// 演示通过 MQTT 协议进行音频转发。
//
// MQTT 协议适合 IoT 设备语音接入，通过 broker 中转消息。
// 媒体格式：自定义二进制帧（14 字节头 + payload）。
//
// 用法：
//
//	1. 先启动 MQTT broker（如 mosquitto）：
//	  docker run -p 1883:1883 eclipse-mosquitto
//
//	2. 启动 Rust 媒体节点：
//	  cd rust-media && cargo run -p media-node
//
//	3. 启动 MQTT demo：
//	  go run ./cmd/mqtt-rust-demo
//
//	4. 用 MQTT 客户端连接 broker，订阅/发布 topic：
//	  lingvoice/{sessionID}/media  — 二进制音频帧
//	  lingvoice/{sessionID}/signal — JSON 控制消息
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
	"github.com/LingByte/LingVoice/pkg/protocol/mqtt"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}

// rustHandler 实现 EventHandler，将 MQTT 媒体帧桥接到 Rust 媒体节点
type rustHandler struct {
	log    *zap.Logger
	bridge *rustbridge.Client
	srv    *mqtt.Server

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
	pushStarted bool
	pullCancel  context.CancelFunc
}

func newRustHandler(log *zap.Logger, bridge *rustbridge.Client, srv *mqtt.Server) *rustHandler {
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
		h.log.Info(">> MQTT 连接",
			zap.String("session", event.SessionID),
			zap.String("from", event.From))
		ss := h.getOrCreateSession(event.SessionID)
		if !ss.created {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := h.bridge.CreateSession(ctx, event.SessionID, "mqtt-room", ""); err != nil {
				h.log.Error("rust CreateSession failed", zap.Error(err))
			} else {
				ss.created = true
				h.log.Info("rust session created",
					zap.String("session", event.SessionID),
					zap.String("room", "mqtt-room"))
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
			zap.String("codec", event.Track.Codec.String()))

		ss := h.getOrCreateSession(event.SessionID)

		// 在 Rust 注册 track
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.bridge.AddTrack(ctx, event.SessionID, "mqtt-ep", *event.Track); err != nil {
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

		// 开启 pull loop（Rust → Go → MQTT 客户端）
		h.startPullLoop(event.SessionID, event.Track.ID, ts)

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
	h.log.Info("<< 文本消息",
		zap.String("session", sessionID),
		zap.String("channel", msg.Channel),
		zap.String("data", string(msg.Data)))
	// MQTT 协议暂不实现文本广播（MQTT 本身就是 pub/sub）
	return nil
}

func (h *rustHandler) startPullLoop(sessionID string, trackID common.TrackID, ts *trackState) {
	sess, ok := h.srv.GetSession(sessionID)
	if !ok {
		h.log.Error("session not found for pull loop", zap.String("session", sessionID))
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	ts.pullCancel = cancel

	err := h.bridge.StartPullRtp(ctx, sessionID, trackID, ts.kind, ts.codec, func(frame common.MediaFrame) error {
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
		broker   = flag.String("broker", "tcp://localhost:1883", "MQTT broker 地址")
		clientID = flag.String("client-id", "lingvoice-mqtt-demo", "MQTT 客户端 ID")
		prefix   = flag.String("prefix", "lingvoice", "MQTT topic 前缀")
		rustAddr = flag.String("rust", "localhost:50051", "Rust 媒体节点 gRPC 地址")
	)
	flag.Parse()

	_ = logger.Init(&logger.LogConfig{
		Level:    "debug",
		Filename: "logs/mqtt-rust-demo.log",
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

	// 创建 MQTT 服务
	config := mqtt.Config{
		Broker:      *broker,
		ClientID:    *clientID,
		TopicPrefix: *prefix,
		QoS:         0,
	}

	handler := newRustHandler(log, bridge, nil)
	srv := mqtt.NewServer(config, handler, log)
	handler.srv = srv

	// 启动 MQTT 服务
	if err := srv.Start(); err != nil {
		log.Fatal("MQTT 服务启动失败", zap.Error(err))
	}

	// 信号处理
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Printf("\n")
	fmt.Printf("╔══════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║  LingVoice MQTT + Rust Media Demo                        ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════╝\n")
	fmt.Printf("\n")
	fmt.Printf("  MQTT Broker: %s\n", *broker)
	fmt.Printf("  Topic 前缀:  %s\n", *prefix)
	fmt.Printf("  Rust 媒体:   %s\n", *rustAddr)
	fmt.Printf("  Room:        mqtt-room\n")
	fmt.Printf("\n")
	fmt.Printf("  媒体链路 (音频):\n")
	fmt.Printf("    IoT设备 → MQTT publish → Go → gRPC PushRtp → Rust room 路由\n")
	fmt.Printf("    → gRPC PullRtp → Go → MQTT subscribe → 其他IoT设备\n")
	fmt.Printf("\n")
	fmt.Printf("  Topic 格式:\n")
	fmt.Printf("    %s/{sessionID}/signal — JSON 控制消息\n", *prefix)
	fmt.Printf("    %s/{sessionID}/media  — 二进制音频帧\n", *prefix)
	fmt.Printf("\n")
	fmt.Printf("  按 Ctrl+C 退出\n")
	fmt.Printf("\n")

	log.Info("mqtt-rust-demo 启动",
		zap.String("broker", *broker),
		zap.String("rust", *rustAddr))

	<-sigCh
	log.Info("收到退出信号，正在关闭...")
}
