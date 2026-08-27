package ws

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/gorilla/websocket"
)

// Session 表示一个 WebSocket 语音会话
type Session struct {
	id         string
	conn       *websocket.Conn
	mu         sync.Mutex
	negotiated bool
	trackAdded bool // 是否已触发 EventTrackAdded（防止重复）
	audio      *common.AudioMedia // 协商后的音频参数
	video      *common.VideoMedia // 协商后的视频参数
	handler    common.EventHandler
	closed     atomic.Bool
	sendSeq    atomic.Uint32 // 发送序列号（用 Uint32 避免 Go 1.26 移除的 AddUint16）
	createdAt  time.Time
	// 统计
	audioPacketsSent atomic.Uint64
	audioBytesSent   atomic.Uint64
	videoPacketsSent atomic.Uint64
	videoBytesSent   atomic.Uint64
}

func newSession(id string, conn *websocket.Conn, handler common.EventHandler) *Session {
	return &Session{
		id:        id,
		conn:      conn,
		handler:   handler,
		createdAt: time.Now(),
	}
}

func (s *Session) ID() string                  { return s.id }
func (s *Session) Protocol() common.ProtocolType { return common.ProtocolWS }
func (s *Session) Negotiated() bool            { return s.negotiated }
func (s *Session) Audio() *common.AudioMedia   { return s.audio }
func (s *Session) Video() *common.VideoMedia   { return s.video }

// SendCommand 处理上层下发的指令
func (s *Session) SendCommand(cmd common.ProtocolCommand) error {
	switch cmd.Type {
	case common.CmdAnswer:
		// WS 没有"接听"概念，协商成功即接听
		return nil
	case common.CmdReject:
		return s.sendJSON(ErrorMessage{
			Type:    MsgTypeError,
			Code:    "rejected",
			Message: cmd.Reason,
		})
	case common.CmdHangup:
		return s.Close()
	case common.CmdStartMedia:
		// 通知客户端 ready
		return s.sendJSON(ReadyMessage{
			Type:      MsgTypeReady,
			Timestamp: time.Now().UnixMilli(),
		})
	case common.CmdStopMedia:
		return s.sendJSON(StopMessage{Type: MsgTypeStop})
	default:
		return nil
	}
}

// SendMediaFrame 向客户端发送音视频帧（二进制）。
// WS 是固定轨道协议，trackID 应为 TrackIDAudio / TrackIDVideo。
func (s *Session) SendMediaFrame(trackID common.TrackID, frame common.MediaFrame) error {
	if s.closed.Load() {
		return ErrSessionNotFound
	}
	// 填充序列号
	frame.Sequence = uint16(s.sendSeq.Add(1))
	data := EncodeFrame(frame)
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.conn.WriteMessage(websocket.BinaryMessage, data)
	if err == nil {
		n := uint64(len(data))
		if trackID == TrackIDAudio {
			s.audioPacketsSent.Add(1)
			s.audioBytesSent.Add(n)
		} else if trackID == TrackIDVideo {
			s.videoPacketsSent.Add(1)
			s.videoBytesSent.Add(n)
		}
	}
	return err
}

// Tracks 返回当前协商的轨道信息（实现 MediaSession 接口）
func (s *Session) Tracks() []common.TrackInfo {
	var tracks []common.TrackInfo
	if s.audio != nil {
		tracks = append(tracks, common.TrackInfo{
			ID:         TrackIDAudio,
			Kind:       common.TrackAudio,
			Direction:  common.TrackSend,
			Codec:      s.audio.Codec,
			SampleRate: s.audio.SampleRate,
			Channels:   s.audio.Channels,
		})
	}
	if s.video != nil {
		tracks = append(tracks, common.TrackInfo{
			ID:        TrackIDVideo,
			Kind:      common.TrackVideo,
			Direction: common.TrackSend,
			Codec:     s.video.Codec,
		})
	}
	return tracks
}

// MediaStats 返回所有轨道的统计信息（实现 MediaSession 接口）
func (s *Session) MediaStats() map[common.TrackID]common.TrackStats {
	stats := make(map[common.TrackID]common.TrackStats)
	if s.audio != nil {
		stats[TrackIDAudio] = common.TrackStats{
			PacketsSent: s.audioPacketsSent.Load(),
			BytesSent:   s.audioBytesSent.Load(),
		}
	}
	if s.video != nil {
		stats[TrackIDVideo] = common.TrackStats{
			PacketsSent: s.videoPacketsSent.Load(),
			BytesSent:   s.videoBytesSent.Load(),
		}
	}
	return stats
}

// SendData 通过指定通道发送文本数据（实现 DataSession 接口）。
// WS 协议用 JSON ChatMessage 承载文本消息，channel 作为通道标签。
func (s *Session) SendData(channel string, data []byte) error {
	if s.closed.Load() {
		return ErrSessionNotFound
	}
	return s.sendJSON(ChatMessage{
		Type:    MsgTypeChat,
		Channel: channel,
		Message: string(data),
	})
}

// sendJSON 发送文本 JSON 消息
func (s *Session) sendJSON(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(v)
}

// Close 关闭会话
func (s *Session) Close() error {
	if s.closed.Load() {
		return nil
	}
	s.closed.Store(true)
	_ = s.conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	return s.conn.Close()
}
