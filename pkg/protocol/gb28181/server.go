package gb28181

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

// Config GB28181 SIP 服务配置
type Config struct {
	Addr     string // SIP 监听地址，如 ":5060"
	ServerID string // 本端 SIP server ID（域 ID），如 "34020000002000000001"
	Realm    string // SIP 域，默认与 ServerID 同
}

// DefaultConfig 默认配置（端口 5060）
func DefaultConfig() Config {
	return Config{
		Addr:     ":5060",
		ServerID: "34020000002000000001",
		Realm:    "3402000000",
	}
}

// Device 注册的 GB28181 设备
type Device struct {
	ID           string // 设备 ID（20 位国标编码）
	Remote       *net.UDPAddr
	Contact      string
	RegisteredAt time.Time
	LastActive   time.Time
	Expires      int
}

// Server GB28181 SIP 服务（接收设备注册 + INVITE 推流模式）
type Server struct {
	config  Config
	handler common.EventHandler
	log     *zap.Logger
	conn    *net.UDPConn
	closed  atomic.Bool

	mu       sync.Mutex
	devices  map[string]*Device // key=deviceID
	sessions sync.Map           // map[callID]*mediaSession
}

// mediaSession 一个 INVITE 协商出的媒体会话。
type mediaSession struct {
	callID    string
	deviceID  string
	receiver  *PSReceiver
	createdAt time.Time
}

// NewServer 创建 GB28181 SIP 服务
func NewServer(config Config, handler common.EventHandler, log *zap.Logger) *Server {
	if log == nil {
		log = logger.Lg
	}
	if config.ServerID == "" {
		config.ServerID = "34020000002000000001"
	}
	if config.Realm == "" {
		config.Realm = "3402000000"
	}
	return &Server{
		config:  config,
		handler: handler,
		log:     log.With(zap.String("component", "gb28181-server")),
		devices: make(map[string]*Device),
	}
}

// Start 启动 SIP UDP 监听
func (s *Server) Start() error {
	addr, err := net.ResolveUDPAddr("udp", s.config.Addr)
	if err != nil {
		return fmt.Errorf("gb28181 sip resolve addr %s: %w", s.config.Addr, err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("gb28181 sip listen %s: %w", s.config.Addr, err)
	}
	s.conn = conn
	s.log.Info("gb28181 sip server starting", zap.String("addr", s.config.Addr))
	go s.readLoop()
	return nil
}

// Close 关闭服务
func (s *Server) Close() error {
	s.closed.Store(true)
	// 关闭所有 PS 接收器
	s.sessions.Range(func(_, v any) bool {
		ms := v.(*mediaSession)
		_ = ms.receiver.Close()
		return true
	})
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

// GetDevice 获取已注册设备
func (s *Server) GetDevice(id string) (*Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[id]
	return d, ok
}

// Devices 返回所有已注册设备
func (s *Server) Devices() []*Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Device, 0, len(s.devices))
	for _, d := range s.devices {
		out = append(out, d)
	}
	return out
}

// readLoop 读取 UDP SIP 消息
func (s *Server) readLoop() {
	buf := make([]byte, 65536)
	for {
		if s.closed.Load() {
			return
		}
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if s.closed.Load() {
				return
			}
			s.log.Error("gb28181 sip read", zap.Error(err))
			continue
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		s.handleMessage(data, addr)
	}
}

// handleMessage 处理收到的 SIP 消息
func (s *Server) handleMessage(data []byte, addr *net.UDPAddr) {
	// 响应消息忽略（本端是 server，不主动发 INVITE）
	if IsSipResponse(data) {
		s.log.Debug("gb28181 sip response ignored", zap.String("remote", addr.String()))
		return
	}

	req, err := ParseSipRequest(data)
	if err != nil {
		s.log.Debug("gb28181 sip parse request", zap.String("remote", addr.String()), zap.Error(err))
		return
	}

	s.log.Debug("gb28181 sip request",
		zap.String("remote", addr.String()),
		zap.String("method", req.Method),
		zap.String("call-id", req.CallID),
		zap.String("cseq", req.CSeq))

	switch req.Method {
	case MethodRegister:
		s.handleRegister(req, addr)
	case MethodMessage:
		s.handleMessageMethod(req, addr)
	case MethodInvite:
		s.handleInvite(req, addr)
	case MethodAck:
		s.handleAck(req, addr)
	case MethodBye:
		s.handleBye(req, addr)
	case MethodOptions:
		s.handleOptions(req, addr)
	default:
		s.sendResponse(req, addr, StatusNotImplemented)
	}
}

// sendResponse 发送一个简单 SIP 响应
func (s *Server) sendResponse(req *SipRequest, addr *net.UDPAddr, status int) {
	resp := newSipResponse(status)
	resp.Via = req.Via
	resp.From = req.From
	resp.To = req.To
	resp.CallID = req.CallID
	resp.CSeq = req.CSeq
	if status == StatusOK {
		// 生成 To tag
		resp.ToTag = strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	_ = s.writeTo(addr, resp)
}

// writeTo 序列化并发送响应
func (s *Server) writeTo(addr *net.UDPAddr, resp *SipResponse) error {
	var buf []byte
	// 用 strings.Builder 写入内存
	var sb stringBuilder
	if err := resp.Render(&sb); err != nil {
		return err
	}
	buf = sb.Bytes()
	_, err := s.conn.WriteToUDP(buf, addr)
	return err
}

// stringBuilder 简单包装，实现 io.Writer
type stringBuilder struct {
	buf []byte
}

func (b *stringBuilder) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *stringBuilder) Bytes() []byte { return b.buf }

// ─── REGISTER ────────────────────────────────────────────────────────────────

func (s *Server) handleRegister(req *SipRequest, addr *net.UDPAddr) {
	deviceID := extractUser(req.From)
	if deviceID == "" {
		s.sendResponse(req, addr, StatusBadRequest)
		return
	}

	expires := 3600
	if v, ok := req.Headers["expires"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			expires = n
		}
	}

	s.mu.Lock()
	dev := &Device{
		ID:           deviceID,
		Remote:       addr,
		Contact:      req.Contact,
		RegisteredAt: time.Now(),
		LastActive:   time.Now(),
		Expires:      expires,
	}
	s.devices[deviceID] = dev
	s.mu.Unlock()

	s.log.Info("gb28181 device registered",
		zap.String("device", deviceID),
		zap.String("remote", addr.String()),
		zap.Int("expires", expires))

	// 通知上层
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolGB28181,
		SessionID: req.CallID,
		From:      deviceID,
		To:        s.config.ServerID,
		Timestamp: time.Now(),
	})

	s.sendResponse(req, addr, StatusOK)
}

// ─── MESSAGE ─────────────────────────────────────────────────────────────────

func (s *Server) handleMessageMethod(req *SipRequest, addr *net.UDPAddr) {
	// 设备心跳 / 目录查询等，回 200 OK
	deviceID := extractUser(req.From)
	s.mu.Lock()
	if dev, ok := s.devices[deviceID]; ok {
		dev.LastActive = time.Now()
	}
	s.mu.Unlock()

	s.log.Debug("gb28181 message",
		zap.String("device", deviceID),
		zap.Int("body-len", len(req.Body)))

	s.sendResponse(req, addr, StatusOK)
}

// ─── INVITE ──────────────────────────────────────────────────────────────────

func (s *Server) handleInvite(req *SipRequest, addr *net.UDPAddr) {
	deviceID := extractUser(req.From)
	if deviceID == "" {
		s.sendResponse(req, addr, StatusBadRequest)
		return
	}

	// 解析 SDP
	var sdp *SdpDescription
	if len(req.Body) > 0 {
		var err error
		sdp, err = ParseSDP(req.Body)
		if err != nil {
			s.log.Error("gb28181 invite sdp parse", zap.Error(err))
			s.sendResponse(req, addr, StatusBadRequest)
			return
		}
	} else {
		s.sendResponse(req, addr, StatusBadRequest)
		return
	}

	// 分配 PS 接收端口（动态）
	receiver := NewPSReceiver(0, req.CallID, deviceID, s.handler, s.log)
	if err := receiver.Start(); err != nil {
		s.log.Error("gb28181 ps receiver start", zap.Error(err))
		s.sendResponse(req, addr, StatusServerInternalError)
		return
	}

	ms := &mediaSession{
		callID:    req.CallID,
		deviceID:  deviceID,
		receiver:  receiver,
		createdAt: time.Now(),
	}
	s.sessions.Store(req.CallID, ms)

	s.log.Info("gb28181 invite: allocated ps port",
		zap.String("device", deviceID),
		zap.String("call-id", req.CallID),
		zap.Int("ps-port", receiver.LocalPort()),
		zap.String("ssrc", sdp.SSRC))

	// 构造 SDP answer
	localIP := s.localIPFor(addr)
	answer := BuildSdpAnswer(SdpAnswerConfig{
		LocalIP:   localIP,
		LocalPort: receiver.LocalPort(),
		SSRC:      sdp.SSRC,
		DeviceID:  s.config.ServerID,
		Subject:   sdp.Subject,
	})

	// 发送 200 OK + SDP
	resp := newSipResponse(StatusOK)
	resp.Via = req.Via
	resp.From = req.From
	resp.To = req.To
	resp.ToTag = strconv.FormatInt(time.Now().UnixNano(), 16)
	resp.CallID = req.CallID
	resp.CSeq = req.CSeq
	resp.SetBody([]byte(answer), "application/sdp")
	_ = s.writeTo(addr, resp)
}

// handleAck 处理 ACK（INVITE 确认）。收到 ACK 后媒体会话正式建立。
func (s *Server) handleAck(req *SipRequest, addr *net.UDPAddr) {
	if v, ok := s.sessions.Load(req.CallID); ok {
		ms := v.(*mediaSession)
		s.log.Info("gb28181 invite acked, media session established",
			zap.String("call-id", req.CallID),
			zap.String("device", ms.deviceID),
			zap.Int("ps-port", ms.receiver.LocalPort()))

		// 通知上层媒体就绪
		s.handler.OnEvent(common.ProtocolEvent{
			Type:      common.EventAnswered,
			Protocol:  common.ProtocolGB28181,
			SessionID: req.CallID,
			From:      ms.deviceID,
			To:        s.config.ServerID,
			Timestamp: time.Now(),
		})
	}
}

// handleBye 处理 BYE（停止媒体流）
func (s *Server) handleBye(req *SipRequest, addr *net.UDPAddr) {
	if v, ok := s.sessions.LoadAndDelete(req.CallID); ok {
		ms := v.(*mediaSession)
		_ = ms.receiver.Close()
		s.log.Info("gb28181 bye: media session closed",
			zap.String("call-id", req.CallID),
			zap.String("device", ms.deviceID))

		s.handler.OnEvent(common.ProtocolEvent{
			Type:      common.EventHangup,
			Protocol:  common.ProtocolGB28181,
			SessionID: req.CallID,
			From:      ms.deviceID,
			To:        s.config.ServerID,
			Timestamp: time.Now(),
		})
	}
	s.sendResponse(req, addr, StatusOK)
}

// handleOptions 处理 OPTIONS（能力查询）
func (s *Server) handleOptions(req *SipRequest, addr *net.UDPAddr) {
	resp := newSipResponse(StatusOK)
	resp.Via = req.Via
	resp.From = req.From
	resp.To = req.To
	resp.ToTag = strconv.FormatInt(time.Now().UnixNano(), 16)
	resp.CallID = req.CallID
	resp.CSeq = req.CSeq
	resp.Headers["Server"] = "LingVoice-GB28181/1.0"
	_ = s.writeTo(addr, resp)
}

// localIPFor 返回与对端通信的本端 IP（用于 SDP answer 的 c= 行）。
func (s *Server) localIPFor(remote *net.UDPAddr) string {
	// 优先用本端 UDP conn 的本地地址
	if s.conn != nil {
		if laddr, ok := s.conn.LocalAddr().(*net.UDPAddr); ok {
			// 如果监听 0.0.0.0，用对端网段推断
			if laddr.IP.IsUnspecified() {
				if ip := pickLocalIPFor(remote.IP); ip != "" {
					return ip
				}
			}
			return laddr.IP.String()
		}
	}
	return "127.0.0.1"
}

// pickLocalIPFor 选择与目标 IP 同网段的本端 IP。
func pickLocalIPFor(remote net.IP) string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil && remote.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}
