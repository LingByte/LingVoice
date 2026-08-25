// Package main is a complete conversation demo combining HTML client + Go server.
//
// 这是一个端到端的语音对话 demo（纯协议层）：
//   1. 浏览器打开 http://localhost:7080 → 加载 HTML 客户端
//   2. 点击"连接" → WebSocket 连接到 /ws/voice
//   3. 协商音频参数（PCM16 16kHz mono）
//   4. 点击"开始通话" → 浏览器采集麦克风 → PCM16 → WebSocket 二进制帧
//   5. 服务端收到音频帧 → 统计（不做媒体处理，媒体层由 Rust 负责）
//   6. 支持文字聊天（chat 类型消息）
//
// 运行：
//   go run ./cmd/conversation-demo
//   打开浏览器访问 http://localhost:7080
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/LingByte/ling-base/common/logger"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

//go:embed static
var staticFiles embed.FS

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// --- 协议消息类型 ---

const (
	MsgOffer  = "offer"
	MsgAnswer = "answer"
	MsgStart  = "start"
	MsgReady  = "ready"
	MsgStop   = "stop"
	MsgError  = "error"
	MsgChat   = "chat"
)

type message struct {
	Type      string `json:"type"`
	Version   int    `json:"version,omitempty"`
	Session   string `json:"session,omitempty"`
	Message   string `json:"message,omitempty"`
	Code      string `json:"code,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`
	Media     *media `json:"media,omitempty"`
	Answer    *ans   `json:"answerMedia,omitempty"`
}

type media struct {
	Audio *offerAudio `json:"audio,omitempty"`
}

type offerAudio struct {
	Codecs      []string `json:"codecs"`
	SampleRate  uint32   `json:"sampleRate"`
	Channels    uint16   `json:"channels"`
	FrameMs     uint16   `json:"frameMs"`
}

type ans struct {
	Audio *ansAudio `json:"audio,omitempty"`
}

type ansAudio struct {
	Codec           string `json:"codec"`
	SampleRate      uint32 `json:"sampleRate"`
	Channels        uint16 `json:"channels"`
	FrameDurationMs uint16 `json:"frameMs"`
}

// --- Demo 会话 ---

type demoSession struct {
	id          string
	conn        *websocket.Conn
	mu          sync.Mutex
	negotiated  bool
	sampleRate  uint32
	channels    uint16
	frameMs     uint16
	frameCount  atomic.Uint64
	byteCount   atomic.Uint64
	createdAt   time.Time
}

var (
	sessions sync.Map
	log      *zap.Logger
)

func main() {
	addr := flag.String("addr", ":7080", "监听地址")
	flag.Parse()

	// 初始化 ling-base logger
	_ = logger.Init(&logger.LogConfig{
		Level:    "debug",
		Filename: "logs/conversation-demo.log",
		MaxSize:  100,
		MaxAge:   30,
		Daily:    true,
	}, "dev")
	defer logger.Sync()

	log = logger.Lg

	// 提取 static 子目录
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Error("embed static", zap.Error(err))
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("/ws/voice", handleWebSocket)

	log.Info("========================================")
	log.Info("LingVoice 对话 Demo 启动")
	log.Info("========================================")
	log.Info("打开浏览器访问", zap.String("url", fmt.Sprintf("http://localhost%s", *addr)))
	log.Info("WebSocket 端点", zap.String("url", fmt.Sprintf("ws://localhost%s/ws/voice", *addr)))
	log.Info("========================================")

	server := &http.Server{
		Addr:    *addr,
		Handler: mux,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", zap.Error(err))
			os.Exit(1)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Info("正在关闭...")
	_ = server.Close()
	log.Info("已关闭")
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error("ws upgrade", zap.Error(err))
		return
	}
	defer conn.Close()

	sessionID := uuid.NewString()
	sess := &demoSession{
		id:        sessionID,
		conn:      conn,
		createdAt: time.Now(),
	}
	sessions.Store(sessionID, sess)
	defer sessions.Delete(sessionID)

	log.Info("client connected", zap.String("session", sessionID), zap.String("remote", conn.RemoteAddr().String()))

	// 读循环
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Info("client disconnected", zap.String("session", sessionID))
			} else {
				log.Debug("read error", zap.String("session", sessionID), zap.Error(err))
			}
			return
		}

		switch msgType {
		case websocket.TextMessage:
			handleText(sess, data)
		case websocket.BinaryMessage:
			handleBinary(sess, data)
		}
	}
}

func handleText(sess *demoSession, data []byte) {
	var msg message
	if err := json.Unmarshal(data, &msg); err != nil {
		log.Error("parse message", zap.String("session", sess.id), zap.Error(err))
		return
	}

	switch msg.Type {
	case MsgOffer:
		handleOffer(sess, &msg)
	case MsgStart:
		handleStart(sess)
	case MsgStop:
		log.Info("client stop", zap.String("session", sess.id))
	case MsgChat:
		handleChat(sess, &msg)
	default:
		log.Warn("unknown message type", zap.String("session", sess.id), zap.String("type", msg.Type))
	}
}

func handleOffer(sess *demoSession, msg *message) {
	if sess.negotiated {
		sendError(sess, "already-negotiated", "session already negotiated")
		return
	}

	if msg.Version != 1 {
		sendError(sess, "version-mismatch", "server expects version 1")
		return
	}

	// 协商音频：服务端支持 pcm16
	var audio *ansAudio
	if msg.Media != nil && msg.Media.Audio != nil {
		for _, codec := range msg.Media.Audio.Codecs {
			if codec == "pcm16" || codec == "pcm" {
				sess.sampleRate = msg.Media.Audio.SampleRate
				if sess.sampleRate == 0 {
					sess.sampleRate = 16000
				}
				sess.channels = msg.Media.Audio.Channels
				if sess.channels == 0 {
					sess.channels = 1
				}
				sess.frameMs = msg.Media.Audio.FrameMs
				if sess.frameMs == 0 {
					sess.frameMs = 20
				}
				audio = &ansAudio{
					Codec:           "pcm16",
					SampleRate:      sess.sampleRate,
					Channels:        sess.channels,
					FrameDurationMs: sess.frameMs,
				}
				break
			}
		}
	}

	if audio == nil {
		sendError(sess, "no-codec", "no supported codec (server supports: pcm16)")
		return
	}

	sess.negotiated = true

	// 发送 answer
	resp := message{
		Type:   MsgAnswer,
		Answer: &ans{Audio: audio},
	}
	sendJSON(sess, resp)

	log.Info("negotiated",
		zap.String("session", sess.id),
		zap.String("codec", audio.Codec),
		zap.Uint32("sampleRate", audio.SampleRate),
		zap.Uint16("channels", audio.Channels),
		zap.Uint16("frameMs", audio.FrameDurationMs),
	)
}

func handleStart(sess *demoSession) {
	if !sess.negotiated {
		sendError(sess, "not-negotiated", "send offer first")
		return
	}

	// 回复 ready（协议层职责：通知客户端媒体通道就绪）
	// 不做任何媒体处理——回声/混音/ASR 等由媒体层（Rust）负责
	sendJSON(sess, message{Type: MsgReady, Timestamp: time.Now().UnixMilli()})
	log.Info("media ready (protocol only, no media processing)", zap.String("session", sess.id))
}

func handleChat(sess *demoSession, msg *message) {
	log.Info("chat message", zap.String("session", sess.id), zap.String("text", msg.Message))

	// 文字聊天是信令层行为，可以由协议层直接回复
	// 实际对话逻辑（LLM 等）由上层业务/媒体层处理
	reply := message{
		Type:    MsgChat,
		Message: "协议层已收到：「" + msg.Message + "」（媒体层未接入，无 AI 回复）",
	}
	sendJSON(sess, reply)
}

// 帧格式: [1B type][1B codec][4B ts][2B seq][4B sr][2B ch][payload]
const frameHeaderLen = 14

func handleBinary(sess *demoSession, data []byte) {
	if !sess.negotiated {
		return
	}
	if len(data) < frameHeaderLen {
		return
	}

	frameType := data[0]
	codec := data[1]
	payload := data[14:]

	sess.frameCount.Add(1)
	sess.byteCount.Add(uint64(len(payload)))

	// 每 50 帧打印一次统计
	// 协议层只负责收帧并统计，不做任何媒体处理（回声/转码/ASR 由媒体层负责）
	if sess.frameCount.Load()%50 == 0 {
		log.Debug("audio frame stats (no media processing)",
			zap.String("session", sess.id),
			zap.Uint8("type", frameType),
			zap.Uint8("codec", codec),
			zap.Uint64("frames", sess.frameCount.Load()),
			zap.Uint64("bytes", sess.byteCount.Load()),
			zap.Int("payloadLen", len(payload)),
		)
	}
}

func sendJSON(sess *demoSession, msg any) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	_ = sess.conn.WriteJSON(msg)
}

func sendError(sess *demoSession, code, msg string) {
	sendJSON(sess, message{Type: MsgError, Code: code, Reason: msg})
}
