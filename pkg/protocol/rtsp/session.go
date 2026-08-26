package rtsp

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
)

// Session 表示一个 RTSP 接收会话（接收推流模式）。
type Session struct {
	id        string
	conn      net.Conn
	transport *Transport
	handler   common.EventHandler
	server    *Server

	mu          sync.Mutex
	uri         string
	streamID    string
	sessionID   string // RTSP Session header 值
	tracks      map[common.TrackID]*SessionTrack
	playing     bool
	closed      atomic.Bool
	createdAt   time.Time
	lastActive  atomic.Int64 // unix nano
}

// SessionTrack 描述会话内一条轨道的传输信息。
type SessionTrack struct {
	TrackID    common.TrackID
	Kind       common.TrackKind
	Codec      common.CodecType
	SSRC       uint32
	ClockRate  uint32
	Channels   uint16
	// Interleaved TCP 模式
	Channel    byte // RTP channel（RTCP = Channel+1）
	// UDP 模式（暂未实现 UDP 接收，保留字段）
	UDPPort    int
}

func newSession(id string, conn net.Conn, handler common.EventHandler, server *Server) *Session {
	s := &Session{
		id:        id,
		conn:      conn,
		transport: NewTransport(conn),
		handler:   handler,
		server:    server,
		tracks:    make(map[common.TrackID]*SessionTrack),
		createdAt: time.Now(),
	}
	s.touch()
	return s
}

func (s *Session) touch() {
	s.lastActive.Store(time.Now().UnixNano())
}

// ID 返回会话 ID。
func (s *Session) ID() string { return s.id }

// Protocol 返回协议类型。
func (s *Session) Protocol() common.ProtocolType { return common.ProtocolRTSP }

// StreamID 返回流标识（URL path）。
func (s *Session) StreamID() string { return s.streamID }

// RTSPSessionID 返回 RTSP Session header 值。
func (s *Session) RTSPSessionID() string { return s.sessionID }

// SetRTSPSessionID 设置 RTSP Session header 值。
func (s *Session) SetRTSPSessionID(sid string) {
	s.mu.Lock()
	s.sessionID = sid
	s.mu.Unlock()
}

// Tracks 返回当前所有轨道信息（实现 MediaSession）。
func (s *Session) Tracks() []common.TrackInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []common.TrackInfo
	for _, t := range s.tracks {
		out = append(out, common.TrackInfo{
			ID:         t.TrackID,
			Kind:       t.Kind,
			Direction:  common.TrackRecv,
			Codec:      t.Codec,
			SampleRate: t.ClockRate,
			Channels:   t.Channels,
			SSRC:       t.SSRC,
			StreamID:   s.streamID,
		})
	}
	return out
}

// MediaStats 返回轨道统计（实现 MediaSession）。
func (s *Session) MediaStats() map[common.TrackID]common.TrackStats {
	return nil
}

// AddTrack 注册一条轨道。
func (s *Session) AddTrack(t *SessionTrack) {
	s.mu.Lock()
	s.tracks[t.TrackID] = t
	s.mu.Unlock()
	s.transport.RegisterChannel(t.Channel, t.TrackID, t.Kind, t.Codec)
}

// TrackByChannel 根据 interleaved channel 查找轨道。
func (s *Session) TrackByChannel(channel byte) (*SessionTrack, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tracks {
		if t.Channel == channel {
			return t, true
		}
	}
	return nil, false
}

// SetPlaying 标记会话进入 PLAY 状态。
func (s *Session) SetPlaying(v bool) {
	s.mu.Lock()
	s.playing = v
	s.mu.Unlock()
}

// IsPlaying 返回是否在 PLAY 状态。
func (s *Session) IsPlaying() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.playing
}

// SendCommand 处理上层指令（实现 SignalSession）。
func (s *Session) SendCommand(cmd common.ProtocolCommand) error {
	switch cmd.Type {
	case common.CmdHangup:
		return s.Close()
	default:
		return nil
	}
}

// SendMediaFrame RTSP 接收模式不支持发送媒体帧（只接收推流）。
func (s *Session) SendMediaFrame(trackID common.TrackID, frame common.MediaFrame) error {
	return fmt.Errorf("rtsp: send not supported in ingest mode")
}

// Close 关闭会话。
func (s *Session) Close() error {
	if s.closed.Load() {
		return nil
	}
	s.closed.Store(true)
	return s.conn.Close()
}

// LastActive 返回最后活动时间。
func (s *Session) LastActive() time.Time {
	return time.Unix(0, s.lastActive.Load())
}
