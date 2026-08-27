package srt

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
)

// Session 表示一个 SRT 接收会话。
type Session struct {
	id        string
	socketID  uint32
	peerAddr  *net.UDPAddr
	peerSockID uint32
	streamID  string
	handler   common.EventHandler
	server    *Server

	mu         sync.Mutex
	playing    bool
	closed     atomic.Bool
	createdAt  time.Time
	lastActive atomic.Int64

	// TS 解析状态
	tsBuffer   []byte
	tsPackets  int
	tsParser   *tsParser
}

func newSession(id string, addr *net.UDPAddr, socketID uint32, streamID string, handler common.EventHandler, server *Server) *Session {
	s := &Session{
		id:        id,
		socketID:  socketID,
		peerAddr:  addr,
		streamID:  streamID,
		handler:   handler,
		server:    server,
		createdAt: time.Now(),
		tsParser:  newTSParser(),
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
func (s *Session) Protocol() common.ProtocolType { return common.ProtocolSRT }

// StreamID 返回 SRT stream ID（SRTO_STREAMID）。
func (s *Session) StreamID() string { return s.streamID }

// SocketID 返回本端 SRT socket ID。
func (s *Session) SocketID() uint32 { return s.socketID }

// PeerSocketID 返回对端 socket ID。
func (s *Session) PeerSocketID() uint32 { return s.peerSockID }

// SetPeerSocketID 设置对端 socket ID。
func (s *Session) SetPeerSocketID(id uint32) {
	s.mu.Lock()
	s.peerSockID = id
	s.mu.Unlock()
}

// PeerAddr 返回对端地址。
func (s *Session) PeerAddr() *net.UDPAddr { return s.peerAddr }

// Tracks 返回轨道信息（SRT 默认单路 TS 流，固定一个 video track）。
func (s *Session) Tracks() []common.TrackInfo {
	return []common.TrackInfo{
		{
			ID:        "video",
			Kind:      common.TrackVideo,
			Direction: common.TrackRecv,
			Codec:     common.CodecH264,
			StreamID:  s.streamID,
		},
	}
}

// MediaStats 返回轨道统计。
func (s *Session) MediaStats() map[common.TrackID]common.TrackStats {
	return nil
}

// SendCommand 处理上层指令。
func (s *Session) SendCommand(cmd common.ProtocolCommand) error {
	switch cmd.Type {
	case common.CmdHangup:
		return s.Close()
	default:
		return nil
	}
}

// SendMediaFrame SRT 接收模式不支持发送。
func (s *Session) SendMediaFrame(trackID common.TrackID, frame common.MediaFrame) error {
	return nil
}

// Close 关闭会话，发送 shutdown。
func (s *Session) Close() error {
	if s.closed.Load() {
		return nil
	}
	s.closed.Store(true)
	// 发送 shutdown control packet
	if s.server != nil && s.peerAddr != nil {
		shutdown := BuildShutdown(s.peerSockID)
		_ = s.server.sendTo(s.peerAddr, shutdown)
	}
	return nil
}

// LastActive 返回最后活动时间。
func (s *Session) LastActive() time.Time {
	return time.Unix(0, s.lastActive.Load())
}
