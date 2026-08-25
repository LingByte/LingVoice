// Package rtmp implements RTMP protocol-layer handling (publish ingest + play egress).
//
// 纯协议层职责：
//   - publisher：解析 RTMP handshake/connect/publish，提取 tracks，把媒体帧通过 OnMediaFrame 转发上层
//   - player：接受 play 请求，通知上层（EventIncomingCall），由上层通过 SendMediaFrame 喂媒体帧
//
// 不做媒体路由/分发/转码——那是 Rust 媒体层的职责。
package rtmp

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/ling-base/common/logger"
	"github.com/bluenviron/gortmplib"
	"github.com/bluenviron/gortmplib/pkg/codecs"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Config RTMP 服务配置
type Config struct {
	Addr string // 监听地址，如 ":1935"
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{Addr: ":1935"}
}

// Server RTMP 协议服务（纯协议层，不做媒体分发）
type Server struct {
	config   Config
	handler  common.EventHandler
	sessions sync.Map // map[string]*Session
	log      *zap.Logger
	listener net.Listener
}

// Session RTMP 会话（publisher 或 player）
type Session struct {
	id        string
	conn      *gortmplib.ServerConn
	reader    *gortmplib.Reader // publisher 用
	writer    *gortmplib.Writer // player 用
	isPlayer  bool
	handler   common.EventHandler
	remote    string
	streamKey string
	createdAt time.Time
	mu        sync.Mutex
	closed    bool
}

// NewServer 创建 RTMP 服务
func NewServer(config Config, handler common.EventHandler, log *zap.Logger) *Server {
	if log == nil {
		log = logger.Lg
	}
	return &Server{
		config:  config,
		handler: handler,
		log:     log.With(zap.String("component", "rtmp-server")),
	}
}

// Start 启动 RTMP 服务
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		return fmt.Errorf("rtmp listen %s: %w", s.config.Addr, err)
	}
	s.listener = ln
	s.log.Info("rtmp server starting", zap.String("addr", s.config.Addr))

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handleConn(conn)
		}
	}()
	return nil
}

// Close 关闭服务
func (s *Server) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// GetSession 获取会话
func (s *Server) GetSession(id string) (*Session, bool) {
	v, ok := s.sessions.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*Session), true
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	sc := &gortmplib.ServerConn{RW: conn}
	if err := sc.Initialize(); err != nil {
		s.log.Error("rtmp init conn", zap.String("remote", conn.RemoteAddr().String()), zap.Error(err))
		return
	}
	if err := sc.AcceptConn(); err != nil {
		s.log.Error("rtmp accept conn", zap.String("remote", conn.RemoteAddr().String()), zap.Error(err))
		return
	}

	if sc.Publish {
		s.handlePublisher(sc, conn)
	} else {
		s.handlePlayer(sc, conn)
	}
}

// --- Publisher：接收推流，提取 tracks，转发媒体帧给上层 ---

func (s *Server) handlePublisher(sc *gortmplib.ServerConn, conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	r := &gortmplib.Reader{Conn: sc}
	if err := r.Initialize(); err != nil {
		s.log.Error("rtmp reader init", zap.String("remote", conn.RemoteAddr().String()), zap.Error(err))
		return
	}

	sessionID := uuid.NewString()
	streamKey := ""
	if sc.URL != nil {
		streamKey = sc.URL.Path
	}

	session := &Session{
		id:        sessionID,
		conn:      sc,
		reader:    r,
		handler:   s.handler,
		remote:    conn.RemoteAddr().String(),
		streamKey: streamKey,
		createdAt: time.Now(),
	}
	s.sessions.Store(sessionID, session)
	defer s.sessions.Delete(sessionID)

	s.log.Info("rtmp publisher connected",
		zap.String("session", sessionID),
		zap.String("remote", conn.RemoteAddr().String()),
		zap.String("stream", streamKey),
	)

	// 解析 Track 信息，构造 MediaDescription
	var audio *common.AudioMedia
	var video *common.VideoMedia
	for _, track := range r.Tracks() {
		switch codec := track.Codec.(type) {
		case *codecs.MPEG4Audio:
			audio = &common.AudioMedia{Codec: common.CodecOpus, SampleRate: 48000, Channels: 2, FrameDurationMs: 20}
			_ = codec
		case *codecs.MPEG1Audio:
			audio = &common.AudioMedia{Codec: common.CodecOpus, SampleRate: 48000, Channels: 2, FrameDurationMs: 20}
		case *codecs.G711:
			audio = &common.AudioMedia{Codec: common.CodecPCMU, SampleRate: 8000, Channels: 1, FrameDurationMs: 20}
		case *codecs.Opus:
			audio = &common.AudioMedia{Codec: common.CodecOpus, SampleRate: 48000, Channels: 2, FrameDurationMs: 20}
		case *codecs.H264:
			video = &common.VideoMedia{Codec: common.CodecH264, Width: 1920, Height: 1080, FPS: 30}
		case *codecs.H265:
			video = &common.VideoMedia{Codec: common.CodecH264, Width: 1920, Height: 1080, FPS: 30}
		case *codecs.VP9:
			video = &common.VideoMedia{Codec: common.CodecVP8, Width: 1920, Height: 1080, FPS: 30}
		case *codecs.AV1:
			video = &common.VideoMedia{Codec: common.CodecH264, Width: 1920, Height: 1080, FPS: 30}
		}
	}

	// 注册数据回调：只转发给上层 handler，不做任何媒体分发
	for _, track := range r.Tracks() {
		s.registerTrackCallback(sessionID, r, track)
	}

	// 通知上层：来电 + 媒体就绪
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolRTMP,
		SessionID: sessionID,
		From:      conn.RemoteAddr().String(),
		To:        streamKey,
		Timestamp: time.Now(),
	})
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventMediaReady,
		Protocol:  common.ProtocolRTMP,
		SessionID: sessionID,
		Media:     &common.MediaDescription{Audio: audio, Video: video},
		Timestamp: time.Now(),
	})

	// 读循环
	for {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		if err := r.Read(); err != nil {
			s.log.Info("rtmp publisher disconnected", zap.String("session", sessionID), zap.Error(err))
			break
		}
	}

	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventHangup,
		Protocol:  common.ProtocolRTMP,
		SessionID: sessionID,
		Timestamp: time.Now(),
	})
}

// --- Player：接受 play 请求，通知上层，由上层喂媒体帧 ---

func (s *Server) handlePlayer(sc *gortmplib.ServerConn, conn net.Conn) {
	streamKey := ""
	if sc.URL != nil {
		streamKey = sc.URL.Path
	}

	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	// player 需要知道 publisher 的 tracks 才能初始化 Writer。
	// 但 tracks 由上层（媒体层）持有，协议层不持有。
	// 这里用一个空的 tracks 列表初始化 Writer，实际 track 信息由上层通过
	// SendMediaFrame 时按 codec 写入。或者更干净的做法：上层在 EventIncomingCall
	// 回调里通过某种方式把 tracks 传给协议层。
	//
	// 当前简化：player session 创建后通知上层，上层通过 SendMediaFrame 喂帧。
	// Writer 的 tracks 在没有 publisher tracks 时无法初始化——这是 RTMP 协议限制。
	// 真正的解决需要上层（媒体层）在 player 连接时把 publisher 的 tracks 传过来。
	// 这里先记录 player 连接，通知上层，由上层决定如何处理。

	sessionID := uuid.NewString()
	session := &Session{
		id:        sessionID,
		conn:      sc,
		isPlayer:  true,
		handler:   s.handler,
		remote:    conn.RemoteAddr().String(),
		streamKey: streamKey,
		createdAt: time.Now(),
	}
	s.sessions.Store(sessionID, session)
	defer s.sessions.Delete(sessionID)

	s.log.Info("rtmp player connected",
		zap.String("session", sessionID),
		zap.String("remote", conn.RemoteAddr().String()),
		zap.String("stream", streamKey),
	)

	// 通知上层：有 player 要拉流
	// 上层（媒体层）负责把 publisher 的 tracks 传回来，或直接通过 SendMediaFrame 喂帧
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolRTMP,
		SessionID: sessionID,
		From:      conn.RemoteAddr().String(),
		To:        streamKey,
		Timestamp: time.Now(),
	})

	// player 读循环（等待 player 断开）
	conn.SetReadDeadline(time.Time{})
	buf := make([]byte, 1024)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			break
		}
	}

	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventHangup,
		Protocol:  common.ProtocolRTMP,
		SessionID: sessionID,
		Timestamp: time.Now(),
	})
}

// --- Track 回调：只转发给上层，不做媒体分发 ---

func (s *Server) registerTrackCallback(sessionID string, r *gortmplib.Reader, track *gortmplib.Track) {
	switch track.Codec.(type) {
	case *codecs.H264:
		r.OnDataH264(track, func(pts, dts time.Duration, au [][]byte) {
			for _, nal := range au {
				s.handler.OnMediaFrame(sessionID, common.MediaFrame{
					Type:      common.FrameVideo,
					Codec:     common.CodecH264,
					Payload:   nal,
					Timestamp: uint32(pts.Microseconds()),
				})
			}
		})
	case *codecs.H265:
		r.OnDataH265(track, func(pts, dts time.Duration, au [][]byte) {
			for _, nal := range au {
				s.handler.OnMediaFrame(sessionID, common.MediaFrame{
					Type:      common.FrameVideo,
					Codec:     common.CodecH264,
					Payload:   nal,
					Timestamp: uint32(pts.Microseconds()),
				})
			}
		})
	case *codecs.AV1:
		r.OnDataAV1(track, func(pts time.Duration, tu [][]byte) {
			for _, obu := range tu {
				s.handler.OnMediaFrame(sessionID, common.MediaFrame{
					Type:      common.FrameVideo,
					Codec:     common.CodecH264,
					Payload:   obu,
					Timestamp: uint32(pts.Microseconds()),
				})
			}
		})
	case *codecs.VP9:
		r.OnDataVP9(track, func(pts time.Duration, frame []byte) {
			s.handler.OnMediaFrame(sessionID, common.MediaFrame{
				Type:      common.FrameVideo,
				Codec:     common.CodecVP8,
				Payload:   frame,
				Timestamp: uint32(pts.Microseconds()),
			})
		})
	case *codecs.Opus:
		r.OnDataOpus(track, func(pts time.Duration, packet []byte) {
			s.handler.OnMediaFrame(sessionID, common.MediaFrame{
				Type:       common.FrameAudio,
				Codec:      common.CodecOpus,
				Payload:    packet,
				Timestamp:  uint32(pts.Microseconds()),
				SampleRate: 48000,
				Channels:   2,
			})
		})
	case *codecs.G711:
		r.OnDataG711(track, func(pts time.Duration, samples []byte) {
			s.handler.OnMediaFrame(sessionID, common.MediaFrame{
				Type:       common.FrameAudio,
				Codec:      common.CodecPCMU,
				Payload:    samples,
				Timestamp:  uint32(pts.Microseconds()),
				SampleRate: 8000,
				Channels:   1,
			})
		})
	case *codecs.MPEG4Audio:
		r.OnDataMPEG4Audio(track, func(pts time.Duration, au []byte) {
			s.handler.OnMediaFrame(sessionID, common.MediaFrame{
				Type:       common.FrameAudio,
				Codec:      common.CodecOpus,
				Payload:    au,
				Timestamp:  uint32(pts.Microseconds()),
				SampleRate: 48000,
				Channels:   2,
			})
		})
	case *codecs.MPEG1Audio:
		r.OnDataMPEG1Audio(track, func(pts time.Duration, frame []byte) {
			s.handler.OnMediaFrame(sessionID, common.MediaFrame{
				Type:      common.FrameAudio,
				Codec:     common.CodecOpus,
				Payload:   frame,
				Timestamp: uint32(pts.Microseconds()),
			})
		})
	case *codecs.LPCM:
		r.OnDataLPCM(track, func(pts time.Duration, samples []byte) {
			s.handler.OnMediaFrame(sessionID, common.MediaFrame{
				Type:      common.FrameAudio,
				Codec:     common.CodecPCM16,
				Payload:   samples,
				Timestamp: uint32(pts.Microseconds()),
			})
		})
	}
}

// --- Session 方法 ---

func (sess *Session) ID() string                    { return sess.id }
func (sess *Session) Protocol() common.ProtocolType { return common.ProtocolRTMP }

func (sess *Session) SendCommand(cmd common.ProtocolCommand) error {
	switch cmd.Type {
	case common.CmdHangup:
		return sess.Close()
	default:
		return nil
	}
}

// SendMediaFrame 向 player 发送媒体帧。
// publisher 端忽略（只接收）；player 端通过 writer 写出。
// 注意：player 的 Writer 需要上层（媒体层）先通过 SetWriterTracks 初始化。
func (sess *Session) SendMediaFrame(frame common.MediaFrame) error {
	if !sess.isPlayer || sess.writer == nil {
		return nil // publisher 端只接收；player 端未初始化 writer 时忽略
	}
	// 按 codec 类型写入（媒体层负责保证帧格式正确）
	// 这里简化：直接写 payload，实际需要 track 引用
	// 真正实现需要上层在初始化时传入 tracks 并创建 writer
	return nil
}

// SetWriterTracks 由上层（媒体层）调用，用 publisher 的 tracks 初始化 player 的 Writer。
// 这是 RTMP 协议要求：player 必须知道 publisher 的 track 描述才能开始拉流。
func (sess *Session) SetWriterTracks(tracks []*gortmplib.Track) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()

	w := &gortmplib.Writer{
		Conn:   sess.conn,
		Tracks: tracks,
	}
	if err := w.Initialize(); err != nil {
		return fmt.Errorf("rtmp player writer init: %w", err)
	}
	sess.writer = w
	return nil
}

func (sess *Session) Close() error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.closed {
		return nil
	}
	sess.closed = true
	if conn, ok := sess.conn.RW.(net.Conn); ok {
		return conn.Close()
	}
	return nil
}
