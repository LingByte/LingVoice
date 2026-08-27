package rtsp

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/google/uuid"
	"github.com/pion/rtp"
	"go.uber.org/zap"
)

// RTSP 方法
const (
	MethodDescribe      = "DESCRIBE"
	MethodAnnounce      = "ANNOUNCE"
	MethodSetup         = "SETUP"
	MethodPlay          = "PLAY"
	MethodPause         = "PAUSE"
MethodTeardown      = "TEARDOWN"
	MethodGetParameter = "GET_PARAMETER"
	MethodSetParameter = "SET_PARAMETER"
	MethodOptions       = "OPTIONS"
)

// RTSP 状态码
const (
	StatusOK              = 200
	StatusCreated         = 201
	StatusBadRequest      = 400
	StatusNotFound        = 404
	StatusMethodNotAllow  = 405
	StatusNotAcceptable   = 406
	StatusUnsupported     = 415
	StatusInternalError   = 500
	StatusNotImplemented  = 501
)

// rtspRequest 解析后的 RTSP 请求。
type rtspRequest struct {
	Method     string
	URI        string
	Proto      string
	CSeq       string
	Session    string
	Headers    map[string]string
	Body       []byte
	ContentLen int
}

// parseRequest 从 bufio.Reader 解析一个 RTSP 请求。
func parseRequest(r *bufio.Reader) (*rtspRequest, error) {
	// 读取请求行
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("rtsp: invalid request line: %q", line)
	}
	req := &rtspRequest{
		Method:  parts[0],
		URI:     parts[1],
		Proto:   parts[2],
		Headers: make(map[string]string),
	}

	// 读取 headers
	for {
		hline, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		hline = strings.TrimRight(hline, "\r\n")
		if hline == "" {
			break
		}
		idx := strings.IndexByte(hline, ':')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(hline[:idx])
		val := strings.TrimSpace(hline[idx+1:])
		req.Headers[strings.ToLower(key)] = val
		switch strings.ToLower(key) {
		case "cseq":
			req.CSeq = val
		case "session":
			req.Session = val
		case "content-length":
			req.ContentLen, _ = strconv.Atoi(val)
		}
	}

	// 读取 body
	if req.ContentLen > 0 {
		req.Body = make([]byte, req.ContentLen)
		if _, err := io.ReadFull(r, req.Body); err != nil {
			return nil, fmt.Errorf("rtsp: read body: %w", err)
		}
	}
	return req, nil
}

// rtspResponse 构造 RTSP 响应。
type rtspResponse struct {
	Status  int
	Reason  string
	CSeq    string
	Session string
	Headers map[string]string
	Body    []byte
}

func newResponse(cseq string, status int) *rtspResponse {
	return &rtspResponse{
		Status:  status,
		Reason:  reasonPhrase(status),
		CSeq:    cseq,
		Headers: make(map[string]string),
	}
}

func (resp *rtspResponse) SetBody(body []byte, contentType string) {
	resp.Body = body
	resp.Headers["Content-Type"] = contentType
	resp.Headers["Content-Length"] = strconv.Itoa(len(body))
}

func (resp *rtspResponse) writeTo(w io.Writer) error {
	var buf strings.Builder
	fmt.Fprintf(&buf, "RTSP/1.0 %d %s\r\n", resp.Status, resp.Reason)
	if resp.CSeq != "" {
		fmt.Fprintf(&buf, "CSeq: %s\r\n", resp.CSeq)
	}
	if resp.Session != "" {
		fmt.Fprintf(&buf, "Session: %s\r\n", resp.Session)
	}
	for k, v := range resp.Headers {
		fmt.Fprintf(&buf, "%s: %s\r\n", k, v)
	}
	buf.WriteString("\r\n")
	if _, err := w.Write([]byte(buf.String())); err != nil {
		return err
	}
	if len(resp.Body) > 0 {
		if _, err := w.Write(resp.Body); err != nil {
			return err
		}
	}
	return nil
}

func reasonPhrase(code int) string {
	switch code {
	case StatusOK:
		return "OK"
	case StatusCreated:
		return "Created"
	case StatusBadRequest:
		return "Bad Request"
	case StatusNotFound:
		return "Not Found"
	case StatusMethodNotAllow:
		return "Method Not Allowed"
	case StatusNotAcceptable:
		return "Not Acceptable"
	case StatusUnsupported:
		return "Unsupported Media Type"
	case StatusInternalError:
		return "Internal Server Error"
	case StatusNotImplemented:
		return "Not Implemented"
	default:
		return "Unknown"
	}
}

// handleRequest 分发 RTSP 请求到对应处理函数。
func (s *Server) handleRequest(session *Session, req *rtspRequest, w io.Writer) error {
	s.log.Debug("rtsp request",
		zap.String("session", session.id),
		zap.String("method", req.Method),
		zap.String("uri", req.URI),
		zap.String("cseq", req.CSeq))
	session.touch()

	switch req.Method {
	case MethodOptions:
		return s.handleOptions(session, req, w)
	case MethodDescribe:
		return s.handleDescribe(session, req, w)
	case MethodAnnounce:
		return s.handleAnnounce(session, req, w)
	case MethodSetup:
		return s.handleSetup(session, req, w)
	case MethodPlay:
		return s.handlePlay(session, req, w)
	case MethodPause:
		return s.handlePause(session, req, w)
	case MethodTeardown:
		return s.handleTeardown(session, req, w)
	case MethodGetParameter:
		return s.handleGetParameter(session, req, w)
	case MethodSetParameter:
		return s.handleSetParameter(session, req, w)
	default:
		resp := newResponse(req.CSeq, StatusNotImplemented)
		return resp.writeTo(w)
	}
}

func (s *Server) handleOptions(session *Session, req *rtspRequest, w io.Writer) error {
	resp := newResponse(req.CSeq, StatusOK)
	resp.Headers["Public"] = "OPTIONS, DESCRIBE, ANNOUNCE, SETUP, PLAY, PAUSE, TEARDOWN, GET_PARAMETER, SET_PARAMETER"
	return resp.writeTo(w)
}

// handleAnnounce 接收推流端发送的 SDP 描述（推流模式）。
// ANNOUNCE 是推流端发起，告知 server 媒体描述。
func (s *Server) handleAnnounce(session *Session, req *rtspRequest, w io.Writer) error {
	if len(req.Body) == 0 {
		resp := newResponse(req.CSeq, StatusBadRequest)
		return resp.writeTo(w)
	}

	sdp, err := ParseSDP(req.Body)
	if err != nil {
		resp := newResponse(req.CSeq, StatusBadRequest)
		return resp.writeTo(w)
	}

	sessionID := uuid.NewString()
	session.SetRTSPSessionID(sessionID)

	// 解析 streamID from URI
	if u, err := url.Parse(req.URI); err == nil {
		session.streamID = strings.TrimPrefix(u.Path, "/")
	}

	// 注册 tracks
	for i, m := range sdp.Media {
		trackID := common.TrackID(m.Type) // "audio" / "video"
		kind := common.TrackAudio
		codec := common.CodecOpus
		clockRate := uint32(48000)
		var channels uint16 = 1

		if m.Type == "video" {
			kind = common.TrackVideo
			codec = common.CodecH264
			clockRate = 90000
		}
		if m.RTPMap != "" {
			c, cr, ch := parseRTPMap(m.RTPMap, m.Type)
			if c != 0 {
				codec = c
			}
			if cr > 0 {
				clockRate = cr
			}
			if ch > 0 {
				channels = ch
			}
		}

		st := &SessionTrack{
			TrackID:   trackID,
			Kind:      kind,
			Codec:     codec,
			ClockRate: clockRate,
			Channels:  channels,
			Channel:   byte(i * 2), // interleaved channel: 0,2,4...
		}
		session.AddTrack(st)

		// 通知上层轨道就绪
		s.handler.OnEvent(common.ProtocolEvent{
			Type:      common.EventTrackAdded,
			Protocol:  common.ProtocolRTSP,
			SessionID: session.id,
			Track: &common.TrackInfo{
				ID:         trackID,
				Kind:       kind,
				Direction:  common.TrackRecv,
				Codec:      codec,
				SampleRate: clockRate,
				Channels:   channels,
				StreamID:   session.streamID,
			},
			Timestamp: time.Now(),
		})
	}

	resp := newResponse(req.CSeq, StatusOK)
	resp.Session = session.RTSPSessionID()
	return resp.writeTo(w)
}

// handleDescribe 返回 SDP 描述。
// 在推流（ingest）模式下，DESCRIBE 通常由拉流端发起；
// 这里返回一个基本 SDP（如果有已注册的 track）或 404。
func (s *Server) handleDescribe(session *Session, req *rtspRequest, w io.Writer) error {
	// 推流模式下 DESCRIBE 不常用；返回空 SDP
	sdp := BuildSDP(session)
	resp := newResponse(req.CSeq, StatusOK)
	resp.SetBody([]byte(sdp), "application/sdp")
	return resp.writeTo(w)
}

// handleSetup 建立传输通道。
// 支持 interleaved TCP（Transport: RTP/AVP/TCP;interleaved=<a>-<b>）。
func (s *Server) handleSetup(session *Session, req *rtspRequest, w io.Writer) error {
	transport := req.Headers["transport"]
	if transport == "" {
		resp := newResponse(req.CSeq, StatusBadRequest)
		return resp.writeTo(w)
	}

	// 解析 interleaved channel
	channel, isTCP := parseInterleaved(transport)

	// 查找对应 track（通过 URL track 参数或 interleaved 推断）
	trackID := trackIDFromURI(req.URI)
	st, ok := session.findTrack(trackID)
	if !ok {
		// 如果没有指定 track，取第一个
		st = session.firstTrack()
		if st == nil {
			resp := newResponse(req.CSeq, StatusNotFound)
			return resp.writeTo(w)
		}
	}

	if isTCP && channel >= 0 {
		st.Channel = byte(channel)
		session.transport.RegisterChannel(st.Channel, st.TrackID, st.Kind, st.Codec)
	}

	resp := newResponse(req.CSeq, StatusOK)
	resp.Session = session.RTSPSessionID()
	if isTCP {
		resp.Headers["Transport"] = fmt.Sprintf("RTP/AVP/TCP;interleaved=%d-%d", channel, channel+1)
	} else {
		resp.Headers["Transport"] = transport
	}
	return resp.writeTo(w)
}

// handlePlay 开始传输。
func (s *Server) handlePlay(session *Session, req *rtspRequest, w io.Writer) error {
	session.SetPlaying(true)

	// 通知上层：接听 / 媒体就绪
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventAnswered,
		Protocol:  common.ProtocolRTSP,
		SessionID: session.id,
		Timestamp: time.Now(),
	})

	resp := newResponse(req.CSeq, StatusOK)
	resp.Session = session.RTSPSessionID()
	resp.Headers["Range"] = "npt=0.000-"
	return resp.writeTo(w)
}

// handlePause 暂停传输。
func (s *Server) handlePause(session *Session, req *rtspRequest, w io.Writer) error {
	session.SetPlaying(false)
	resp := newResponse(req.CSeq, StatusOK)
	resp.Session = session.RTSPSessionID()
	return resp.writeTo(w)
}

// handleTeardown 关闭会话。
func (s *Server) handleTeardown(session *Session, req *rtspRequest, w io.Writer) error {
	resp := newResponse(req.CSeq, StatusOK)
	resp.Session = session.RTSPSessionID()
	_ = resp.writeTo(w)
	// 触发关闭
	_ = session.Close()
	return nil
}

// handleGetParameter keepalive。
func (s *Server) handleGetParameter(session *Session, req *rtspRequest, w io.Writer) error {
	resp := newResponse(req.CSeq, StatusOK)
	resp.Session = session.RTSPSessionID()
	if len(req.Body) > 0 {
		resp.SetBody(req.Body, "text/parameters")
	}
	return resp.writeTo(w)
}

// handleSetParameter keepalive / 参数设置。
func (s *Server) handleSetParameter(session *Session, req *rtspRequest, w io.Writer) error {
	resp := newResponse(req.CSeq, StatusOK)
	resp.Session = session.RTSPSessionID()
	return resp.writeTo(w)
}

// --- 辅助函数 ---

// parseInterleaved 解析 Transport header 中的 interleaved channel。
// 返回 channel 编号和是否为 TCP interleaved 模式。
func parseInterleaved(transport string) (int, bool) {
	// RTP/AVP/TCP;interleaved=0-1
	if !strings.Contains(transport, "TCP") {
		return -1, false
	}
	parts := strings.Split(transport, ";")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "interleaved=") {
			val := strings.TrimPrefix(p, "interleaved=")
			rangeParts := strings.SplitN(val, "-", 2)
			ch, _ := strconv.Atoi(rangeParts[0])
			return ch, true
		}
	}
	return 0, true // TCP 但未指定 channel，默认 0
}

// trackIDFromURI 从 SETUP URL 推断 track ID。
// URL 形如 rtsp://host/path/trackID=audio
func trackIDFromURI(uri string) common.TrackID {
	if u, err := url.Parse(uri); err == nil {
		seg := u.Path
		if idx := strings.LastIndex(seg, "trackID="); idx >= 0 {
			return common.TrackID(seg[idx+8:])
		}
		if idx := strings.LastIndex(seg, "/"); idx >= 0 {
			return common.TrackID(seg[idx+1:])
		}
	}
	return ""
}

// parseRTPMap 解析 a=rtpmap 行，返回 codec/clockRate/channels。
// 格式: <payload> <codec>/<clockRate>[/channels]
func parseRTPMap(rtpmap, mediaType string) (common.CodecType, uint32, uint16) {
	// 去掉 payload type 前缀: "96 H264/90000"
	parts := strings.SplitN(rtpmap, " ", 2)
	if len(parts) < 2 {
		return 0, 0, 0
	}
	desc := parts[1]
	sub := strings.SplitN(desc, "/", 3)
	codecStr := sub[0]
	var clockRate uint32
	var channels uint16
	if len(sub) >= 2 {
		cr, _ := strconv.ParseUint(sub[1], 10, 32)
		clockRate = uint32(cr)
	}
	if len(sub) >= 3 {
		ch, _ := strconv.ParseUint(sub[2], 10, 16)
		channels = uint16(ch)
	}
	codec, err := common.CodecFromString(codecStr)
	if err != nil {
		// 默认值
		if mediaType == "video" {
			return common.CodecH264, clockRate, channels
		}
		return common.CodecOpus, clockRate, channels
	}
	return codec, clockRate, channels
}

// findTrack 查找轨道。
func (s *Session) findTrack(id common.TrackID) (*SessionTrack, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tracks[id]
	return t, ok
}

// firstTrack 返回第一个轨道。
func (s *Session) firstTrack() *SessionTrack {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tracks {
		return t
	}
	return nil
}

// handleRtpData 处理 interleaved 收到的 RTP 数据，转发上层。
func (s *Server) handleRtpData(session *Session, channel byte, data []byte) {
	trackID, kind, codec, ok := session.transport.ChannelInfo(channel)
	if !ok {
		return
	}
	var pkt rtp.Packet
	if err := pkt.Unmarshal(data); err != nil {
		s.log.Debug("rtsp: parse rtp failed",
			zap.String("session", session.id),
			zap.Int("channel", int(channel)),
			zap.Error(err))
		return
	}
	frame := RtpToFrame(pkt, kind, codec)
	// 使用 track 的 clockRate
	if st, ok := session.findTrack(trackID); ok && st.ClockRate > 0 {
		frame.SampleRate = st.ClockRate
		// 记录统计
		st.packetsReceived++
		st.bytesReceived += uint64(len(data))
	}
	_ = s.handler.OnMediaFrame(session.id, trackID, frame)
}
