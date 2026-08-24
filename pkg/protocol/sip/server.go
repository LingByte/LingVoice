package sip

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"

	"github.com/google/uuid"
)

// Config SIP 服务配置
type Config struct {
	Addr     string // 监听地址，如 "0.0.0.0:5060"
	Realm    string // SIP 鉴权 realm，如 "lingvoice"
	// AuthFunc 自定义鉴权函数，返回 true 表示通过。
	// 为 nil 则不鉴权（开发模式）。
	AuthFunc func(username, realm string) (secret string, ok bool)
	// RouteFunc 路由函数：根据 To 返回目标地址。
	// 为 nil 则默认拒绝。
	RouteFunc func(to string) (target string, ok bool)
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Addr:  "0.0.0.0:5060",
		Realm: "lingvoice",
	}
}

// Server SIP 协议服务端
type Server struct {
	config   Config
	handler  common.EventHandler
	sessions sync.Map // map[string]*Session
	server   *sipgo.Server
	logger   *slog.Logger
}

// Session SIP 会话
type Session struct {
	id        string
	callID    string
	from      string
	to        string
	server    *sipgo.Server
	handler   common.EventHandler
	createdAt time.Time
	mu        sync.Mutex
	closed    bool
}

// NewServer 创建 SIP 服务
func NewServer(config Config, handler common.EventHandler, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		config:  config,
		handler: handler,
		logger:  logger.With("component", "sip-server"),
	}

	// 创建 sipgo UserAgent + Server
	ua, err := sipgo.NewUA()
	if err != nil {
		return nil, fmt.Errorf("sipgo new UA: %w", err)
	}
	srv, err := sipgo.NewServer(ua)
	if err != nil {
		return nil, fmt.Errorf("sipgo new server: %w", err)
	}
	s.server = srv

	// 注册 SIP 方法处理器
	srv.OnInvite(s.onInvite)
	srv.OnBye(s.onBye)
	srv.OnRegister(s.onRegister)
	srv.OnOptions(s.onOptions)
	srv.OnAck(s.onAck)
	srv.OnCancel(s.onCancel)

	return s, nil
}

// Start 启动 SIP 服务（UDP）
func (s *Server) Start() error {
	host, portStr, err := net.SplitHostPort(s.config.Addr)
	if err != nil {
		return fmt.Errorf("invalid addr %q: %w", s.config.Addr, err)
	}
	port, _ := strconv.Atoi(portStr)

	s.logger.Info("sip server starting", "addr", s.config.Addr, "realm", s.config.Realm)
	return s.server.ListenAndServe(context.Background(), "udp", fmt.Sprintf("%s:%d", host, port))
}

// StartTCP 启动 SIP 服务（TCP）
func (s *Server) StartTCP() error {
	s.logger.Info("sip server starting (TCP)", "addr", s.config.Addr)
	return s.server.ListenAndServe(context.Background(), "tcp", s.config.Addr)
}

// Close 关闭服务
func (s *Server) Close() error {
	return s.server.Close()
}

// GetSession 获取会话
func (s *Server) GetSession(id string) (*Session, bool) {
	v, ok := s.sessions.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*Session), true
}

// --- SIP 事件处理 ---

func (s *Server) onInvite(req *sip.Request, tx sip.ServerTransaction) {
	callID := string(*req.CallID())
	from := req.From().Address.String()
	to := req.To().Address.String()

	s.logger.Info("sip INVITE", "callID", callID, "from", from, "to", to)

	// 鉴权
	if s.config.AuthFunc != nil {
		if !s.checkAuth(req) {
			// 发 401 挑战
			resp := sip.NewResponseFromRequest(req, 401, "Unauthorized", nil)
			resp.AppendHeader(sip.NewHeader("WWW-Authenticate",
				fmt.Sprintf(`Digest realm="%s", nonce="%s", algorithm=MD5`,
					s.config.Realm, uuid.NewString())))
			_ = tx.Respond(resp)
			return
		}
	}

	// 路由
	if s.config.RouteFunc != nil {
		target, ok := s.config.RouteFunc(to)
		if !ok {
			resp := sip.NewResponseFromRequest(req, 404, "Not Found", nil)
			_ = tx.Respond(resp)
			return
		}
		_ = target // 后续用于转发或转接
	}

	// 创建会话
	sessionID := callID
	session := &Session{
		id:        sessionID,
		callID:    callID,
		from:      from,
		to:        to,
		server:    s.server,
		handler:   s.handler,
		createdAt: time.Now(),
	}
	s.sessions.Store(sessionID, session)

	// 通知上层：来电
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolSIP,
		SessionID: sessionID,
		From:      from,
		To:        to,
		Timestamp: time.Now(),
	})

	// 发 180 Ringing
	resp := sip.NewResponseFromRequest(req, 180, "Ringing", nil)
	_ = tx.Respond(resp)

	// 等待上层决策（Answer/Reject）
	// 上层通过 SendCommand 下发决策
	// 这里先发 200 OK（简化：自动接听）
	// 实际应由上层调用 session.SendCommand(CmdAnswer) 触发
	go s.autoAnswer(req, tx, sessionID)
}

// autoAnswer 自动接听（简化版，后续改为等上层决策）
func (s *Server) autoAnswer(req *sip.Request, tx sip.ServerTransaction, sessionID string) {
	// 给上层一点时间决策
	time.Sleep(100 * time.Millisecond)

	session, ok := s.GetSession(sessionID)
	if !ok {
		return
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	// 从 SDP 提取音频信息
	audio := s.parseSDP(req.Body())
	if audio == nil {
		audio = &common.AudioMedia{
			Codec:           common.CodecPCMU,
			SampleRate:      8000,
			Channels:        1,
			FrameDurationMs: 20,
		}
	}

	// 发 200 OK
	resp := sip.NewResponseFromRequest(req, 200, "OK", nil)
	// 添加 Contact header
	resp.AppendHeader(sip.NewHeader("Contact", fmt.Sprintf("<sip:%s>", s.config.Addr)))
	_ = tx.Respond(resp)

	// 通知上层：接听 + 媒体就绪
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventAnswered,
		Protocol:  common.ProtocolSIP,
		SessionID: sessionID,
		From:      session.from,
		To:        session.to,
		Timestamp: time.Now(),
	})
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventMediaReady,
		Protocol:  common.ProtocolSIP,
		SessionID: sessionID,
		Media:     &common.MediaDescription{Audio: audio},
		Timestamp: time.Now(),
	})
}

func (s *Server) onBye(req *sip.Request, tx sip.ServerTransaction) {
	callID := string(*req.CallID())
	s.logger.Info("sip BYE", "callID", callID)

	s.sessions.Delete(callID)

	// 200 OK
	resp := sip.NewResponseFromRequest(req, 200, "OK", nil)
	_ = tx.Respond(resp)

	// 通知上层：挂断
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventHangup,
		Protocol:  common.ProtocolSIP,
		SessionID: callID,
		Timestamp: time.Now(),
	})
}

func (s *Server) onRegister(req *sip.Request, tx sip.ServerTransaction) {
	from := req.From().Address.String()
	s.logger.Info("sip REGISTER", "from", from, "contact", getContact(req))

	// 鉴权
	if s.config.AuthFunc != nil {
		if !s.checkAuth(req) {
			resp := sip.NewResponseFromRequest(req, 401, "Unauthorized", nil)
			resp.AppendHeader(sip.NewHeader("WWW-Authenticate",
				fmt.Sprintf(`Digest realm="%s", nonce="%s", algorithm=MD5`,
					s.config.Realm, uuid.NewString())))
			_ = tx.Respond(resp)
			return
		}
	}

	// 200 OK
	resp := sip.NewResponseFromRequest(req, 200, "OK", nil)
	_ = tx.Respond(resp)
}

func (s *Server) onOptions(req *sip.Request, tx sip.ServerTransaction) {
	resp := sip.NewResponseFromRequest(req, 200, "OK", nil)
	_ = tx.Respond(resp)
}

func (s *Server) onAck(req *sip.Request, tx sip.ServerTransaction) {
	// ACK 不需要响应
}

func (s *Server) onCancel(req *sip.Request, tx sip.ServerTransaction) {
	callID := string(*req.CallID())
	s.logger.Info("sip CANCEL", "callID", callID)
	s.sessions.Delete(callID)

	resp := sip.NewResponseFromRequest(req, 200, "OK", nil)
	_ = tx.Respond(resp)

	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventHangup,
		Protocol:  common.ProtocolSIP,
		SessionID: callID,
		Timestamp: time.Now(),
	})
}

// --- Session 方法 ---

func (sess *Session) ID() string                    { return sess.id }
func (sess *Session) Protocol() common.ProtocolType { return common.ProtocolSIP }

func (sess *Session) SendCommand(cmd common.ProtocolCommand) error {
	switch cmd.Type {
	case common.CmdHangup:
		return sess.hangup()
	case common.CmdReject:
		return sess.reject(cmd.Reason)
	default:
		return nil
	}
}

// SendMediaFrame SIP 不通过此接口发媒体帧，RTP 媒体由 Rust/媒体面处理
func (sess *Session) SendMediaFrame(frame common.MediaFrame) error {
	return nil // SIP 媒体走 RTP，不通过 Go 协议层
}

func (sess *Session) Close() error {
	return sess.hangup()
}

func (sess *Session) hangup() error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.closed {
		return nil
	}
	sess.closed = true
	// BYE 由 sipgo dialog 管理，这里简化处理
	return nil
}

func (sess *Session) reject(reason string) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.closed = true
	return nil
}

// --- 辅助函数 ---

func (s *Server) checkAuth(req *sip.Request) bool {
	authHeader := req.GetHeader("Authorization")
	if authHeader == nil {
		return false
	}

	// 简化鉴权：解析 Authorization header
	// 实际应做 digest 校验
	authVal := authHeader.Value()
	username := parseAuthParam(authVal, "username")
	if username == "" {
		return false
	}

	secret, ok := s.config.AuthFunc(username, s.config.Realm)
	_ = secret // 简化：有 secret 即通过
	return ok
}

func (s *Server) parseSDP(body []byte) *common.AudioMedia {
	if len(body) == 0 {
		return nil
	}
	sdpStr := string(body)
	// 简化 SDP 解析：找 rtpmap 行
	// a=rtpmap:96 opus/48000/2
	// a=rtpmap:0 PCMU/8000
	lines := splitLines(sdpStr)
	for _, line := range lines {
		if startsWith(line, "a=rtpmap:") {
			// 解析 payload type 和 codec
			rest := line[9:] // 去掉 "a=rtpmap:"
			parts := splitSpace(rest)
			if len(parts) >= 2 {
				codecStr := parts[1]
				// codecStr 格式: opus/48000/2 或 PCMU/8000
				codecParts := splitSlash(codecStr)
				if len(codecParts) >= 2 {
					codecName := codecParts[0]
					codec, err := common.CodecFromString(codecName)
					if err != nil {
						continue
					}
					sr := parseUint(codecParts[1])
					ch := uint16(1)
					if len(codecParts) >= 3 {
						ch = uint16(parseUint(codecParts[2]))
					}
					return &common.AudioMedia{
						Codec:           codec,
						SampleRate:      uint32(sr),
						Channels:        ch,
						FrameDurationMs: 20,
					}
				}
			}
		}
	}
	return nil
}

func getContact(req *sip.Request) string {
	contact := req.Contact()
	if contact == nil {
		return ""
	}
	return contact.Address.String()
}

// --- 简易字符串工具（避免引入额外依赖）---

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func splitSpace(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			if i > start {
				parts = append(parts, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		parts = append(parts, s[start:])
	}
	return parts
}

func splitSlash(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		parts = append(parts, s[start:])
	}
	return parts
}

func parseUint(s string) uint {
	var n uint
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			break
		}
		n = n*10 + uint(s[i]-'0')
	}
	return n
}

func parseAuthParam(authVal, param string) string {
	prefix := param + "="
	idx := indexOf(authVal, prefix)
	if idx < 0 {
		return ""
	}
	rest := authVal[idx+len(prefix):]
	// 去掉引号
	if len(rest) > 0 && rest[0] == '"' {
		end := indexOf(rest[1:], "\"")
		if end >= 0 {
			return rest[1 : 1+end]
		}
	}
	// 非引号值，取到逗号或行尾
	end := indexOf(rest, ",")
	if end >= 0 {
		return rest[:end]
	}
	return rest
}

// 确保 context 被使用（sipgo 可能需要）
var _ = context.Background
