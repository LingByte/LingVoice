// Command sip-rust-demo 启动 SIP 信令服务 + RTP bridge + Rust 媒体节点，
// 演示通过 SIP 协议接听电话并将 RTP 音频桥接到 Rust 媒体节点。
//
// SIP 的特殊性：信令（INVITE/BYE）和媒体（RTP/UDP）是分离的。
// sipgo 只处理信令，RTP 媒体需要单独的 UDP socket 收发。
//
// 本 demo 实现：
//   - SIP 信令：接听来电，从 SDP 解析对端 RTP 地址
//   - RTP bridge：开本地 UDP socket 收对端 RTP → 推到 Rust
//   - PullRtp：从 Rust 拉其他 participant 的音频 → 发回 SIP 对端
//
// 用法：
//
//	1. 先启动 Rust 媒体节点：
//	  cd rust-media && cargo run -p media-node
//
//	2. 启动 SIP demo：
//	  go run ./cmd/sip-rust-demo
//
//	3. 用 SIP 客户端（如 Zoiper/Linphone）拨打：
//	  sip:lingvoice@localhost:5060
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/LingByte/LingVoice/pkg/media/rustbridge"
	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/LingVoice/pkg/protocol/sip"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

// rustHandler 实现 EventHandler + SIP RTP bridge
type rustHandler struct {
	log    *zap.Logger
	bridge *rustbridge.Client
	srv    *sip.Server

	mu       sync.Mutex
	sessions map[string]*sessionState
}

type sessionState struct {
	sessionID  string
	created    bool
	rtpConn    *RTPBridge
	pullCancel context.CancelFunc
	codec      common.CodecType
	sampleRate uint32
}

// RTPBridge 管理 SIP 的 RTP UDP 收发
type RTPBridge struct {
	localPort  int
	remoteAddr *net.UDPAddr
	conn       *net.UDPConn
}

func newRTPBridge(localPort int) (*RTPBridge, error) {
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: localPort}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen RTP UDP: %w", err)
	}
	return &RTPBridge{
		localPort: localPort,
		conn:      conn,
	}, nil
}

func (b *RTPBridge) SetRemote(ip string, port int) error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", ip, port))
	if err != nil {
		return err
	}
	b.remoteAddr = addr
	return nil
}

// ReadRTP 读一个 RTP 包，返回 payload
func (b *RTPBridge) ReadRTP(buf []byte) (payload []byte, seq uint16, timestamp uint32, ssrc uint32, marker bool, err error) {
	n, _, err := b.conn.ReadFromUDP(buf)
	if err != nil {
		return nil, 0, 0, 0, false, err
	}
	if n < 12 {
		return nil, 0, 0, 0, false, fmt.Errorf("RTP packet too short: %d", n)
	}
	// RTP 头解析
	header := buf[:12]
	version := (header[0] >> 6) & 0x03
	if version != 2 {
		return nil, 0, 0, 0, false, fmt.Errorf("not RTP v2")
	}
	hasPadding := (header[0] >> 5) & 0x01
	hasExtension := (header[0] >> 4) & 0x01
	cc := int(header[0] & 0x0f)
	marker = (header[1] >> 7) & 0x01 == 1
	seq = binary.BigEndian.Uint16(header[2:4])
	timestamp = binary.BigEndian.Uint32(header[4:8])
	ssrc = binary.BigEndian.Uint32(header[8:12])

	headerLen := 12 + cc*4
	if hasExtension == 1 {
		if headerLen+4 > n {
			return nil, 0, 0, 0, false, fmt.Errorf("extension truncated")
		}
		extLen := int(binary.BigEndian.Uint16(buf[headerLen+2:headerLen+4]))
		headerLen += 4 + extLen * 4
	}
	payloadStart := headerLen
	payloadEnd := n
	if hasPadding == 1 && payloadEnd > payloadStart {
		paddingLen := int(buf[payloadEnd-1])
		if paddingLen <= payloadEnd-payloadStart {
			payloadEnd -= paddingLen
		}
	}
	return buf[payloadStart:payloadEnd], seq, timestamp, ssrc, marker, nil
}

// WriteRTP 发送 RTP 包到对端
func (b *RTPBridge) WriteRTP(payload []byte, seq uint16, timestamp uint32, ssrc uint32, payloadType byte, marker bool) error {
	if b.remoteAddr == nil {
		return fmt.Errorf("remote address not set")
	}
	header := make([]byte, 12)
	header[0] = 0x80 // V=2, P=0, X=0, CC=0
	if marker {
		header[1] = 0x80 | payloadType
	} else {
		header[1] = payloadType
	}
	binary.BigEndian.PutUint16(header[2:4], seq)
	binary.BigEndian.PutUint32(header[4:8], timestamp)
	binary.BigEndian.PutUint32(header[8:12], ssrc)

	packet := append(header, payload...)
	_, err := b.conn.WriteToUDP(packet, b.remoteAddr)
	return err
}

func (b *RTPBridge) Close() error {
	return b.conn.Close()
}

func newRustHandler(log *zap.Logger, bridge *rustbridge.Client, srv *sip.Server) *rustHandler {
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
		ss = &sessionState{sessionID: sessionID}
		h.sessions[sessionID] = ss
	}
	return ss
}

func (h *rustHandler) OnEvent(event common.ProtocolEvent) error {
	switch event.Type {
	case common.EventIncomingCall:
		h.log.Info(">> SIP 来电",
			zap.String("session", event.SessionID),
			zap.String("from", event.From),
			zap.String("to", event.To))

		ss := h.getOrCreateSession(event.SessionID)

		// 在 Rust 创建 session
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.bridge.CreateSession(ctx, event.SessionID, "sip-room", ""); err != nil {
			h.log.Error("rust CreateSession failed", zap.Error(err))
		} else {
			ss.created = true
		}
		cancel()

		// 开本地 RTP 端口（从 10000 开始递增）
		rtpPort := 10000 + int(hashSession(event.SessionID)%1000)
		rtpBridge, err := newRTPBridge(rtpPort)
		if err != nil {
			h.log.Error("RTP bridge listen failed", zap.Error(err))
			return nil
		}
		ss.rtpConn = rtpBridge
		h.log.Info("RTP bridge opened",
			zap.String("session", event.SessionID),
			zap.Int("localPort", rtpPort))

		// 设置本地 RTP 端口到 SIP session（用于 SDP answer）并自动接听
		if sess, ok := h.srv.GetSession(event.SessionID); ok {
			sess.SetLocalRtpPort(rtpPort)
			_ = sess.SendCommand(common.ProtocolCommand{Type: common.CmdAnswer})
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
		ss.codec = event.Track.Codec
		ss.sampleRate = event.Track.SampleRate

		// 在 Rust 注册 track
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.bridge.AddTrack(ctx, event.SessionID, "sip-ep", *event.Track); err != nil {
			h.log.Error("rust AddTrack failed", zap.Error(err))
			cancel()
			return nil
		}
		cancel()

		// 开 push stream（RTP → Rust）
		pushCtx, _ := context.WithCancel(context.Background())
		if err := h.bridge.StartPushRtp(pushCtx, event.SessionID, event.Track.ID); err != nil {
			h.log.Error("StartPushRtp failed", zap.Error(err))
			return nil
		}

		// 开 pull loop（Rust → RTP → SIP 对端）
		h.startPullLoop(event.SessionID, event.Track.ID, ss)

		// 开 RTP 读循环（SIP 对端 → Rust）
		if ss.rtpConn != nil {
			go h.rtpReadLoop(event.SessionID, event.Track.ID, ss)
		}

	case common.EventHangup:
		h.log.Info(">> 挂断", zap.String("session", event.SessionID))
		h.cleanupSession(event.SessionID)
	}
	return nil
}

func (h *rustHandler) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	// SIP 不通过 OnMediaFrame 传媒体，RTP 直接在 rtpReadLoop 处理
	return nil
}

func (h *rustHandler) OnData(sessionID string, msg common.DataMessage) error {
	return nil
}

// rtpReadLoop 从 SIP 对端读 RTP → 推到 Rust
func (h *rustHandler) rtpReadLoop(sessionID string, trackID common.TrackID, ss *sessionState) {
	buf := make([]byte, 1500)
	for {
		payload, seq, timestamp, ssrc, marker, err := ss.rtpConn.ReadRTP(buf)
		if err != nil {
			h.log.Debug("RTP read end", zap.String("session", sessionID), zap.Error(err))
			return
		}
		frame := common.MediaFrame{
			Type:      common.FrameAudio,
			Codec:     ss.codec,
			Payload:   payload,
			Sequence:  seq,
			Timestamp: timestamp,
			SSRC:      ssrc,
			Marker:    marker,
			SampleRate: ss.sampleRate,
		}
		if err := h.bridge.PushRtpPacket(sessionID, trackID, frame); err != nil {
			h.log.Debug("push rtp failed", zap.Error(err))
		}
	}
}

func (h *rustHandler) startPullLoop(sessionID string, trackID common.TrackID, ss *sessionState) {
	ctx, cancel := context.WithCancel(context.Background())
	ss.pullCancel = cancel

	err := h.bridge.StartPullRtp(ctx, sessionID, trackID, common.TrackAudio, ss.codec, func(frame common.MediaFrame) error {
		if ss.rtpConn == nil {
			return fmt.Errorf("rtp bridge not ready")
		}
		// 从 Rust 收到其他 participant 的音频，发回 SIP 对端
		payloadType := codecToPayloadType(ss.codec)
		return ss.rtpConn.WriteRTP(frame.Payload, frame.Sequence, frame.Timestamp, frame.SSRC, payloadType, frame.Marker)
	})
	if err != nil {
		h.log.Error("StartPullRtp failed",
			zap.String("session", sessionID),
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

	if ss.pullCancel != nil {
		ss.pullCancel()
	}
	if ss.rtpConn != nil {
		_ = ss.rtpConn.Close()
	}
	_ = h.bridge.ClosePushRtp(sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = h.bridge.DestroySession(ctx, sessionID)
}

// codecToPayloadType 常见 SIP 编解码的 RTP payload type
func codecToPayloadType(codec common.CodecType) byte {
	switch codec {
	case common.CodecPCMU:
		return 0
	case common.CodecPCMA:
		return 8
	case common.CodecOpus:
		return 111 // 动态
	default:
		return 0
	}
}

// hashSession 简单 hash sessionID 到端口偏移
func hashSession(s string) uint32 {
	var h uint32
	for _, c := range s {
		h = h*31 + uint32(c)
	}
	return h
}

func main() {
	var (
		addr     = flag.String("addr", "0.0.0.0:5060", "SIP 服务监听地址")
		rustAddr = flag.String("rust", "127.0.0.1:50051", "Rust 媒体节点 gRPC 地址")
	)
	flag.Parse()

	_ = logger.Init(&logger.LogConfig{
		Level:    "debug",
		Filename: "logs/sip-rust-demo.log",
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

	// 创建 SIP 服务
	config := sip.DefaultConfig()
	config.Addr = *addr
	config.AuthFunc = nil // 开发模式，不鉴权
	config.RouteFunc = func(to string) (string, bool) {
		return "", true // 接受所有路由
	}

	handler := newRustHandler(log, bridge, nil)
	srv, err := sip.NewServer(config, handler, log)
	if err != nil {
		log.Fatal("SIP 服务创建失败", zap.Error(err))
	}
	handler.srv = srv

	// 启动 SIP 服务
	go func() {
		if err := srv.Start(); err != nil {
			log.Fatal("SIP 服务启动失败", zap.Error(err))
		}
	}()

	// 信号处理
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Printf("\n")
	fmt.Printf("╔══════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║  LingVoice SIP + Rust Media Demo                         ║\n")
	fmt.Printf("╚══════════════════════════════════════════════════════════╝\n")
	fmt.Printf("\n")
	fmt.Printf("  SIP 监听:    %s (UDP)\n", *addr)
	fmt.Printf("  Rust 媒体:   %s\n", *rustAddr)
	fmt.Printf("  Room:        sip-room\n")
	fmt.Printf("  RTP 端口:    10000-10999 (动态分配)\n")
	fmt.Printf("\n")
	fmt.Printf("  媒体链路:\n")
	fmt.Printf("    SIP 对端 → RTP UDP → Go RTP bridge → gRPC PushRtp → Rust\n")
	fmt.Printf("    Rust → gRPC PullRtp → Go → RTP UDP → SIP 对端\n")
	fmt.Printf("\n")
	fmt.Printf("  测试方法:\n")
	fmt.Printf("    用 SIP 客户端（Zoiper/Linphone）拨打 sip:lingvoice@localhost:5060\n")
	fmt.Printf("\n")
	fmt.Printf("  按 Ctrl+C 退出\n")
	fmt.Printf("\n")

	log.Info("sip-rust-demo 启动", zap.String("addr", *addr), zap.String("rust", *rustAddr))

	<-sigCh
	log.Info("收到退出信号，正在关闭...")
	_ = srv.Close()
}

// ensure strings is used
var _ = strings.Contains
