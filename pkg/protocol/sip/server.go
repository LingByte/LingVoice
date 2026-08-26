package sip

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/LingVoice/pkg/protocol/media"
	"github.com/LingByte/ling-base/common/logger"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"go.uber.org/zap"

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
	// AudioCodecs 服务端优先支持的音频编解码列表，用于 SDP 协商。
	// 为空则默认 ["pcmu", "pcma", "opus"]。
	AudioCodecs []string
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Addr:        "0.0.0.0:5060",
		Realm:       "lingvoice",
		AudioCodecs: []string{"pcmu", "pcma", "opus"},
	}
}

// Server SIP 协议服务端
type Server struct {
	config   Config
	handler  common.EventHandler
	sessions sync.Map // map[string]*Session
	server   *sipgo.Server
	log      *zap.Logger
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

	// INVITE 事务上下文（用于异步 Answer/Reject）
	inviteReq *sip.Request
	inviteTx  sip.ServerTransaction
	answered  bool
	audio     *common.AudioMedia

	// 本地 RTP 端口（由上层在 EventIncomingCall 后设置，用于 SDP answer）
	localRtpPort int
	// 本地 IP（用于 SDP answer，默认从 config.Addr 推断）
	localIP string
}

// SetLocalRtpPort 设置本地 RTP 端口，用于 SDP answer。
// 必须在 SendCommand(CmdAnswer) 之前调用。
func (sess *Session) SetLocalRtpPort(port int) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.localRtpPort = port
}

// NewServer 创建 SIP 服务
func NewServer(config Config, handler common.EventHandler, log *zap.Logger) (*Server, error) {
	if log == nil {
		log = logger.Lg
	}

	s := &Server{
		config:  config,
		handler: handler,
		log:     log.With(zap.String("component", "sip-server")),
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

	s.log.Info("sip server starting", zap.String("addr", s.config.Addr), zap.String("realm", s.config.Realm))
	return s.server.ListenAndServe(context.Background(), "udp", fmt.Sprintf("%s:%d", host, port))
}

// StartTCP 启动 SIP 服务（TCP）
func (s *Server) StartTCP() error {
	s.log.Info("sip server starting (TCP)", zap.String("addr", s.config.Addr))
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

	s.log.Info("sip INVITE", zap.String("callID", callID), zap.String("from", from), zap.String("to", to))

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
	audio := s.parseSDP(req.Body())
	if audio == nil {
		audio = &common.AudioMedia{
			Codec:           common.CodecPCMU,
			SampleRate:      8000,
			Channels:        1,
			FrameDurationMs: 20,
		}
	}
	session := &Session{
		id:        sessionID,
		callID:    callID,
		from:      from,
		to:        to,
		server:    s.server,
		handler:   s.handler,
		createdAt: time.Now(),
		inviteReq: req,
		inviteTx:  tx,
		audio:     audio,
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

	// 发 180 Ringing，等待上层通过 SendCommand(CmdAnswer) 决策
	resp := sip.NewResponseFromRequest(req, 180, "Ringing", nil)
	_ = tx.Respond(resp)
}

func (s *Server) onBye(req *sip.Request, tx sip.ServerTransaction) {
	callID := string(*req.CallID())
	s.log.Info("sip BYE", zap.String("callID", callID))

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
	s.log.Info("sip REGISTER", zap.String("from", from), zap.String("contact", getContact(req)))

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
	s.log.Info("sip CANCEL", zap.String("callID", callID))
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
	case common.CmdAnswer:
		return sess.answer()
	case common.CmdReject:
		return sess.reject(cmd.Reason)
	case common.CmdHangup:
		return sess.hangup()
	default:
		return nil
	}
}

// answer 接听：发 200 OK + SDP answer + 通知上层
func (sess *Session) answer() error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.answered || sess.closed || sess.inviteTx == nil {
		return fmt.Errorf("cannot answer: state invalid")
	}
	sess.answered = true

	// 生成 SDP answer body
	sdpBody := sess.buildSdpAnswer()

	// 发 200 OK + SDP
	resp := sip.NewResponseFromRequest(sess.inviteReq, 200, "OK", []byte(sdpBody))
	resp.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
	resp.AppendHeader(sip.NewHeader("Contact", fmt.Sprintf("<sip:lingvoice@%s>", sess.to)))
	_ = sess.inviteTx.Respond(resp)

	// 通知上层：接听 + 媒体就绪
	sess.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventAnswered,
		Protocol:  common.ProtocolSIP,
		SessionID: sess.id,
		From:      sess.from,
		To:        sess.to,
		Timestamp: time.Now(),
	})
	sess.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventTrackAdded,
		Protocol:  common.ProtocolSIP,
		SessionID: sess.id,
		Track: &common.TrackInfo{
			ID:         common.TrackID(sess.id + "/audio"),
			Kind:       common.TrackAudio,
			Direction:  common.TrackRecv,
			Codec:      sess.audio.Codec,
			SampleRate: sess.audio.SampleRate,
			Channels:   sess.audio.Channels,
		},
		Timestamp: time.Now(),
	})
	return nil
}

// buildSdpAnswer 构造 SDP answer body，包含本地 RTP 端口。
// 如果 localRtpPort 未设置，使用 0（对端无法发 RTP，但信令流程完整）。
func (sess *Session) buildSdpAnswer() string {
	localIP := sess.localIP
	if localIP == "" {
		localIP = "127.0.0.1"
	}
	rtpPort := sess.localRtpPort
	if rtpPort == 0 {
		rtpPort = 0 // 明确表示未分配
	}

	codecName := sess.audio.Codec.String()
	payloadType := codecToPayloadType(sess.audio.Codec)
	clockRate := sess.audio.SampleRate
	channels := sess.audio.Channels
	if channels == 0 {
		channels = 1
	}

	sdp := fmt.Sprintf("v=0\r\n")
	sdp += fmt.Sprintf("o=lingvoice 0 0 IN IP4 %s\r\n", localIP)
	sdp += "s=LingVoice SIP Session\r\n"
	sdp += "c=IN IP4 %s\r\n"
	sdp += "t=0 0\r\n"
	sdp += fmt.Sprintf("m=audio %d RTP/AVP %d\r\n", rtpPort, payloadType)
	sdp += fmt.Sprintf("a=rtpmap:%d %s/%d", payloadType, codecName, clockRate)
	if channels > 1 {
		sdp += fmt.Sprintf("/%d", channels)
	}
	sdp += "\r\n"
	sdp += "a=sendrecv\r\n"
	return fmt.Sprintf(sdp, localIP)
}

// codecToPayloadType 返回 SIP 编解码的 RTP payload type
func codecToPayloadType(codec common.CodecType) int {
	switch codec {
	case common.CodecPCMU:
		return 0
	case common.CodecPCMA:
		return 8
	case common.CodecOpus:
		return 111
	default:
		return 0
	}
}

// SendMediaFrame SIP 不通过此接口发媒体帧，RTP 媒体由外部处理。
// 保留方法签名以兼容 MediaSession 接口，但始终返回错误。
func (sess *Session) SendMediaFrame(trackID common.TrackID, frame common.MediaFrame) error {
	return fmt.Errorf("SIP media handled externally")
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
	// 如果还没接听就挂断，发 CANCEL 响应
	if !sess.answered && sess.inviteTx != nil {
		resp := sip.NewResponseFromRequest(sess.inviteReq, 487, "Request Terminated", nil)
		_ = sess.inviteTx.Respond(resp)
	}
	return nil
}

func (sess *Session) reject(reason string) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.closed {
		return nil
	}
	sess.closed = true
	if sess.inviteTx != nil {
		code := 486 // Busy Here
		if reason == "declined" {
			code = 603
		}
		resp := sip.NewResponseFromRequest(sess.inviteReq, code, reason, nil)
		_ = sess.inviteTx.Respond(resp)
	}
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

// parseSDP 解析 SDP 并使用 pkg/media encoder registry 协商编解码。
func (s *Server) parseSDP(body []byte) *common.AudioMedia {
	prefs := s.config.AudioCodecs
	if len(prefs) == 0 {
		prefs = []string{"pcmu", "pcma", "opus"}
	}
	result, err := media.NegotiateFromSDP(body, prefs, 20)
	if err != nil {
		s.log.Debug("sip SDP negotiation failed", zap.Error(err))
		return nil
	}
	return result.Audio
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
