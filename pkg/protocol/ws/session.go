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
	id        string
	conn      *websocket.Conn
	mu        sync.Mutex
	negotiated bool
	audio     *common.AudioMedia // 协商后的音频参数
	video     *common.VideoMedia // 协商后的视频参数
	handler   common.EventHandler
	closed    atomic.Bool
	sendSeq   atomic.Uint32 // 发送序列号（用 Uint32 避免 Go 1.26 移除的 AddUint16）
	createdAt time.Time
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

// SendMediaFrame 向客户端发送音视频帧（二进制）
func (s *Session) SendMediaFrame(frame common.MediaFrame) error {
	if s.closed.Load() {
		return ErrSessionNotFound
	}
	// 填充序列号
	frame.Sequence = uint16(s.sendSeq.Add(1))
	data := EncodeFrame(frame)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
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
