// Package gb28181 implements a GB28181 SIP signaling listener and PS
// (MPEG-2 Program Stream) receiver that forwards decoded media frames to the
// upper layer via the common EventHandler interface.
//
// GB28181 是中国安防监控标准。设备通过 SIP 信令（UDP，默认 5060）完成注册、
// INVITE 媒体协商，随后通过 UDP 向协商端口发送 PS 流（封装 H.264/H.265 视频 +
// G.711 音频）。本包实现接收侧（监听模式），不依赖外部 SIP 库。
package gb28181

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// md5Sum 包装 crypto/md5.Sum。
func md5Sum(data []byte) [16]byte {
	return md5.Sum(data)
}

// hexEncode 包装 encoding/hex.EncodeToString。
func hexEncode(b []byte) string {
	return hex.EncodeToString(b)
}

// ─── SIP 方法 / 状态码 ───────────────────────────────────────────────────────

// SIP 方法
const (
	MethodRegister = "REGISTER"
	MethodInvite   = "INVITE"
	MethodAck      = "ACK"
	MethodBye      = "BYE"
	MethodMessage  = "MESSAGE"
	MethodOptions  = "OPTIONS"
	MethodCancel   = "CANCEL"
)

// SIP 状态码
const (
	StatusTrying               = 100
	StatusOK                   = 200
	StatusMovedTemporarily     = 302
	StatusBadRequest           = 400
	StatusUnauthorized         = 401
	StatusForbidden            = 403
	StatusNotFound             = 404
	StatusMethodNotAllowed     = 405
	StatusNotAcceptable        = 406
	StatusRequestTimeout       = 408
	StatusPreconditionFailed   = 412
	StatusUnsupportedMediaType = 415
	StatusServerInternalError  = 500
	StatusNotImplemented       = 501
	StatusBadGateway           = 502
	StatusServiceUnavailable   = 503
)

func sipReasonPhrase(code int) string {
	switch code {
	case StatusTrying:
		return "Trying"
	case StatusOK:
		return "OK"
	case StatusMovedTemporarily:
		return "Moved Temporarily"
	case StatusBadRequest:
		return "Bad Request"
	case StatusUnauthorized:
		return "Unauthorized"
	case StatusForbidden:
		return "Forbidden"
	case StatusNotFound:
		return "Not Found"
	case StatusMethodNotAllowed:
		return "Method Not Allowed"
	case StatusNotAcceptable:
		return "Not Acceptable"
	case StatusRequestTimeout:
		return "Request Timeout"
	case StatusPreconditionFailed:
		return "Precondition Failed"
	case StatusUnsupportedMediaType:
		return "Unsupported Media Type"
	case StatusServerInternalError:
		return "Server Internal Error"
	case StatusNotImplemented:
		return "Not Implemented"
	case StatusBadGateway:
		return "Bad Gateway"
	case StatusServiceUnavailable:
		return "Service Unavailable"
	default:
		return "Unknown"
	}
}

// ─── SIP 消息结构 ────────────────────────────────────────────────────────────

// SipRequest 解析后的 SIP 请求。
type SipRequest struct {
	Method     string // REGISTER / INVITE / BYE / ...
	URI        string // 请求 URI
	Proto      string // "SIP/2.0"
	Headers    map[string]string
	Body       []byte
	ContentLen int

	// 常用头（已解析）
	Via         string
	From        string
	To          string
	CallID      string
	CSeq        string
	CSeqNum     int
	CSeqMethod  string
	Contact     string
	MaxForwards int
	UserAgent   string
	// From/To tag
	FromTag string
	ToTag   string
	// Via 分支
	Branch string
}

// SipResponse 构造 SIP 响应。
type SipResponse struct {
	Status  int
	Reason  string
	Headers map[string]string
	Body    []byte

	// 常用头（直接字段，写时合并到 Headers）
	Via    string
	From   string
	To     string
	ToTag  string
	CallID string
	CSeq   string
}

// newSipResponse 创建一个 SIP 响应。
func newSipResponse(status int) *SipResponse {
	return &SipResponse{
		Status:  status,
		Reason:  sipReasonPhrase(status),
		Headers: make(map[string]string),
	}
}

// SetBody 设置响应体及 Content-Type / Content-Length。
func (r *SipResponse) SetBody(body []byte, contentType string) {
	r.Body = body
	r.Headers["Content-Type"] = contentType
	r.Headers["Content-Length"] = strconv.Itoa(len(body))
}

// WriteTo 将响应序列化写入 w。
func (r *SipResponse) Render(w io.Writer) error {
	var buf strings.Builder
	fmt.Fprintf(&buf, "SIP/2.0 %d %s\r\n", r.Status, r.Reason)

	// Via / From / To / Call-ID / CSeq 顺序按 SIP 规范
	if r.Via != "" {
		fmt.Fprintf(&buf, "Via: %s\r\n", r.Via)
	}
	if r.From != "" {
		fmt.Fprintf(&buf, "From: %s\r\n", r.From)
	}
	if r.To != "" {
		to := r.To
		if r.ToTag != "" {
			// 追加 tag（若 To 已含 tag 则不重复）
			if !strings.Contains(to, "tag=") {
				to = to + ";tag=" + r.ToTag
			}
		}
		fmt.Fprintf(&buf, "To: %s\r\n", to)
	}
	if r.CallID != "" {
		fmt.Fprintf(&buf, "Call-ID: %s\r\n", r.CallID)
	}
	if r.CSeq != "" {
		fmt.Fprintf(&buf, "CSeq: %s\r\n", r.CSeq)
	}

	for k, v := range r.Headers {
		// 跳过已显式写入的头
		switch k {
		case "Content-Length":
			// 始终用实际 body 长度
			fmt.Fprintf(&buf, "Content-Length: %d\r\n", len(r.Body))
			continue
		}
		fmt.Fprintf(&buf, "%s: %s\r\n", k, v)
	}
	if _, ok := r.Headers["Content-Length"]; !ok {
		fmt.Fprintf(&buf, "Content-Length: %d\r\n", len(r.Body))
	}

	buf.WriteString("\r\n")
	if _, err := w.Write([]byte(buf.String())); err != nil {
		return err
	}
	if len(r.Body) > 0 {
		if _, err := w.Write(r.Body); err != nil {
			return err
		}
	}
	return nil
}

// ─── SIP 消息解析 ────────────────────────────────────────────────────────────

// ParseSipRequest 从字节切片解析一个 SIP 请求（UDP 单包）。
func ParseSipRequest(data []byte) (*SipRequest, error) {
	text := string(data)
	// 头部与正文以 \r\n\r\n 分隔
	sep := "\r\n\r\n"
	idx := strings.Index(text, sep)
	var headerText, bodyText string
	if idx < 0 {
		// 兼容仅 \n\n
		sep = "\n\n"
		idx = strings.Index(text, sep)
		if idx < 0 {
			headerText = text
			bodyText = ""
		} else {
			headerText = text[:idx]
			bodyText = text[idx+len(sep):]
		}
	} else {
		headerText = text[:idx]
		bodyText = text[idx+len(sep):]
	}

	lines := splitLines(headerText)
	if len(lines) == 0 {
		return nil, fmt.Errorf("gb28181: empty sip message")
	}

	// Request-Line: METHOD URI SIP/2.0
	reqLine := lines[0]
	parts := strings.SplitN(reqLine, " ", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("gb28181: invalid request line: %q", reqLine)
	}
	// 必须是 SIP 请求（非响应）
	if !strings.HasPrefix(parts[2], "SIP/") {
		return nil, fmt.Errorf("gb28181: not a sip request: %q", reqLine)
	}

	req := &SipRequest{
		Method:  parts[0],
		URI:     parts[1],
		Proto:   parts[2],
		Headers: make(map[string]string),
	}

	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		ci := strings.IndexByte(line, ':')
		if ci < 0 {
			continue
		}
		key := strings.TrimSpace(line[:ci])
		val := strings.TrimSpace(line[ci+1:])
		lk := strings.ToLower(key)
		req.Headers[lk] = val
		switch lk {
		case "via":
			req.Via = val
			req.Branch = extractParam(val, "branch")
		case "from":
			req.From = val
			req.FromTag = extractParam(val, "tag")
		case "to":
			req.To = val
			req.ToTag = extractParam(val, "tag")
		case "call-id":
			req.CallID = val
		case "cseq":
			req.CSeq = val
			req.CSeqNum, req.CSeqMethod = parseCSeq(val)
		case "contact":
			req.Contact = val
		case "max-forwards":
			req.MaxForwards, _ = strconv.Atoi(val)
		case "user-agent":
			req.UserAgent = val
		case "content-length":
			req.ContentLen, _ = strconv.Atoi(val)
		}
	}

	// body：优先用 Content-Length 截断，否则用分隔后的剩余
	if req.ContentLen > 0 && req.ContentLen <= len(bodyText) {
		req.Body = []byte(bodyText[:req.ContentLen])
	} else if req.ContentLen == 0 {
		req.Body = nil
	} else {
		req.Body = []byte(bodyText)
	}

	return req, nil
}

// IsSipResponse 判断字节流是否是 SIP 响应（以 "SIP/2.0 " 开头）。
func IsSipResponse(data []byte) bool {
	return len(data) >= 8 && string(data[:8]) == "SIP/2.0 "
}

// splitLines 按行拆分（兼容 \r\n 与 \n）。
func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		out = append(out, strings.TrimRight(l, "\r"))
	}
	return out
}

// extractParam 从 SIP 头值中提取参数，如 branch=z9hG4bKxxx / tag=abc。
func extractParam(headerVal, name string) string {
	parts := strings.Split(headerVal, ";")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, name+"=") {
			return strings.TrimPrefix(p, name+"=")
		}
	}
	return ""
}

// parseCSeq 解析 "1 REGISTER" → (1, "REGISTER")。
func parseCSeq(val string) (int, string) {
	val = strings.TrimSpace(val)
	sp := strings.IndexByte(val, ' ')
	if sp < 0 {
		return 0, val
	}
	num, _ := strconv.Atoi(val[:sp])
	return num, strings.TrimSpace(val[sp+1:])
}

// extractUser 从 SIP URI / header 中提取用户部分（device ID）。
// 如 <sip:34020000001320000001@3402000000>;tag=xxx → 34020000001320000001
func extractUser(headerVal string) string {
	// 去掉参数
	if ci := strings.IndexByte(headerVal, ';'); ci >= 0 {
		headerVal = headerVal[:ci]
	}
	headerVal = strings.TrimSpace(headerVal)
	// 去掉尖括号
	headerVal = strings.Trim(headerVal, "<>")
	// sip:user@host
	if strings.HasPrefix(headerVal, "sip:") {
		headerVal = strings.TrimPrefix(headerVal, "sip:")
	}
	at := strings.IndexByte(headerVal, '@')
	if at >= 0 {
		return headerVal[:at]
	}
	return headerVal
}

// ─── SDP 解析 / 生成 ─────────────────────────────────────────────────────────

// SdpDescription 解析后的 GB28181 SDP（简化版，关注 m= / a= / y=）。
type SdpDescription struct {
	// o= 行
	Username  string
	SessionID string
	Version   string
	AddrType  string // IP4 / IP6
	Address   string

	// c= 行
	ConnectionAddrType string
	ConnectionAddress  string

	// s= 行
	Subject string

	// m= 行
	MediaType    string // video / audio
	Port         int
	Transport    string // RTP/AVP
	PayloadTypes []int

	// a= 行
	Attributes map[string]string // rtpmap:PT -> "codec/clockrate"

	// y= 行（GB28181 SSRC，10 位十进制）
	SSRC string
}

// ParseSDP 解析 SDP 文本。
func ParseSDP(data []byte) (*SdpDescription, error) {
	sdp := &SdpDescription{
		Attributes: make(map[string]string),
	}
	lines := splitLines(string(data))
	for _, line := range lines {
		if len(line) < 2 || line[1] != '=' {
			continue
		}
		key := line[0]
		val := strings.TrimSpace(line[2:])
		switch key {
		case 'o':
			// o=username session version addrtype address
			fields := strings.Fields(val)
			if len(fields) >= 6 {
				sdp.Username = fields[0]
				sdp.SessionID = fields[1]
				sdp.Version = fields[2]
				sdp.AddrType = fields[3]
				sdp.Address = fields[4]
			}
		case 's':
			sdp.Subject = val
		case 'c':
			// c=IN IP4 192.168.1.100
			fields := strings.Fields(val)
			if len(fields) >= 3 {
				sdp.ConnectionAddrType = fields[1]
				sdp.ConnectionAddress = fields[2]
			}
		case 'm':
			// m=video 6000 RTP/AVP 96 98
			fields := strings.Fields(val)
			if len(fields) >= 3 {
				sdp.MediaType = fields[0]
				sdp.Port, _ = strconv.Atoi(fields[1])
				sdp.Transport = fields[2]
				for _, pt := range fields[3:] {
					n, err := strconv.Atoi(pt)
					if err == nil {
						sdp.PayloadTypes = append(sdp.PayloadTypes, n)
					}
				}
			}
		case 'a':
			// a=rtpmap:96 PS/90000
			if strings.HasPrefix(val, "rtpmap:") {
				rest := strings.TrimPrefix(val, "rtpmap:")
				sp := strings.IndexByte(rest, ' ')
				if sp >= 0 {
					pt := rest[:sp]
					sdp.Attributes["rtpmap:"+pt] = rest[sp+1:]
				}
			}
		case 'y':
			sdp.SSRC = val
		}
	}
	return sdp, nil
}

// SdpAnswerConfig 构造 SDP answer 的参数。
type SdpAnswerConfig struct {
	// 本端接收 PS 流的 IP（通常是 server IP）
	LocalIP string
	// 本端接收 PS 流的 UDP 端口（动态分配）
	LocalPort int
	// SSRC（GB28181 y= 字段，10 位十进制字符串）
	SSRC string
	// 设备 ID（o= username）
	DeviceID string
	// s= 主题
	Subject string
}

// BuildSdpAnswer 构造 GB28181 SDP answer（INVITE 200 OK 的 body）。
// GB28181 中 server 作为接收端（a=recvonly 由设备发，server 回 a=sendonly
// 表示由设备发送、server 接收；实际方向取决于实现，这里 server 接收 PS 流，
// 设备发送，因此 answer 用 a=sendonly 表示"对端发送"）。
func BuildSdpAnswer(cfg SdpAnswerConfig) string {
	var buf strings.Builder
	buf.WriteString("v=0\r\n")
	fmt.Fprintf(&buf, "o=%s 0 0 IN IP4 %s\r\n", cfg.DeviceID, cfg.LocalIP)
	fmt.Fprintf(&buf, "s=%s\r\n", orDefault(cfg.Subject, "Play"))
	fmt.Fprintf(&buf, "c=IN IP4 %s\r\n", cfg.LocalIP)
	buf.WriteString("t=0 0\r\n")
	fmt.Fprintf(&buf, "m=video %d RTP/AVP 96\r\n", cfg.LocalPort)
	buf.WriteString("a=rtpmap:96 PS/90000\r\n")
	buf.WriteString("a=sendonly\r\n")
	if cfg.SSRC != "" {
		fmt.Fprintf(&buf, "y=%s\r\n", cfg.SSRC)
	}
	return buf.String()
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ─── SIP 请求构造（server 主动向设备发 MESSAGE / INFO） ──────────────────────

// SipRequestBuilder 构造一个发往设备的 SIP 请求。
type SipRequestBuilder struct {
	Method     string
	URI        string
	From       string
	FromTag    string
	To         string
	CallID     string
	CSeqNum    int
	CSeqMethod string
	Via        string
	Branch     string
	MaxForwards int
	Body       []byte
	ContentType string
	// 额外头
	ExtraHeaders map[string]string
}

// BuildSipRequest 序列化为 SIP 请求字节。
func (b *SipRequestBuilder) Build() []byte {
	var buf strings.Builder
	fmt.Fprintf(&buf, "%s %s SIP/2.0\r\n", b.Method, b.URI)
	if b.Via != "" {
		fmt.Fprintf(&buf, "Via: %s\r\n", b.Via)
	}
	if b.From != "" {
		from := b.From
		if b.FromTag != "" && !strings.Contains(from, "tag=") {
			from = from + ";tag=" + b.FromTag
		}
		fmt.Fprintf(&buf, "From: %s\r\n", from)
	}
	if b.To != "" {
		fmt.Fprintf(&buf, "To: %s\r\n", b.To)
	}
	if b.CallID != "" {
		fmt.Fprintf(&buf, "Call-ID: %s\r\n", b.CallID)
	}
	if b.CSeqMethod != "" {
		fmt.Fprintf(&buf, "CSeq: %d %s\r\n", b.CSeqNum, b.CSeqMethod)
	}
	mf := b.MaxForwards
	if mf == 0 {
		mf = 70
	}
	fmt.Fprintf(&buf, "Max-Forwards: %d\r\n", mf)
	for k, v := range b.ExtraHeaders {
		fmt.Fprintf(&buf, "%s: %s\r\n", k, v)
	}
	if len(b.Body) > 0 {
		if b.ContentType != "" {
			fmt.Fprintf(&buf, "Content-Type: %s\r\n", b.ContentType)
		}
		fmt.Fprintf(&buf, "Content-Length: %d\r\n", len(b.Body))
	} else {
		fmt.Fprintf(&buf, "Content-Length: 0\r\n")
	}
	buf.WriteString("\r\n")
	if len(b.Body) > 0 {
		buf.Write(b.Body)
	}
	return []byte(buf.String())
}

// ─── Digest 认证工具 ─────────────────────────────────────────────────────────

// DigestParams 解析 Authorization / WWW-Authenticate 头中的参数。
type DigestParams map[string]string

// ParseDigestHeader 解析 Digest 头值，如：
//   Digest realm="3402000000",nonce="abc",algorithm=MD5,username="dev",uri="sip:...",response="..."
func ParseDigestHeader(headerVal string) (DigestParams, bool) {
	headerVal = strings.TrimSpace(headerVal)
	if !strings.HasPrefix(headerVal, "Digest") {
		return nil, false
	}
	headerVal = strings.TrimPrefix(headerVal, "Digest")
	headerVal = strings.TrimSpace(headerVal)
	params := DigestParams{}
	// 按逗号拆分，但注意引号内可能含逗号（此处简化处理）
	for _, kv := range splitDigestParams(headerVal) {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(kv[:eq])
		val := strings.TrimSpace(kv[eq+1:])
		val = strings.Trim(val, `"`)
		params[key] = val
	}
	return params, true
}

// splitDigestParams 按逗号拆分 Digest 参数（兼容引号内逗号的简单实现）。
func splitDigestParams(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			inQuote = !inQuote
			cur.WriteByte(c)
			continue
		}
		if c == ',' && !inQuote {
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// ComputeDigestResponse 计算 SIP Digest 认证响应值。
// response = MD5( MD5(user:realm:pass) : nonce : MD5(method:uri) )
func ComputeDigestResponse(username, password, realm, nonce, method, uri string) string {
	ha1 := md5Hex(username + ":" + realm + ":" + password)
	ha2 := md5Hex(method + ":" + uri)
	return md5Hex(ha1 + ":" + nonce + ":" + ha2)
}

// md5Hex 计算 MD5 并返回 32 位小写十六进制字符串。
func md5Hex(s string) string {
	h := md5Sum([]byte(s))
	return hexEncode(h[:])
}
