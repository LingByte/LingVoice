package srt

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/ling-base/common/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Config SRT 服务配置
type Config struct {
	Addr string // 监听地址，如 ":9000"
}

// DefaultConfig 默认配置（端口 9000）
func DefaultConfig() Config {
	return Config{Addr: ":9000"}
}

// Server SRT UDP 服务（接收推流模式）
type Server struct {
	config   Config
	handler  common.EventHandler
	sessions sync.Map // map[string]*Session（key=sessionID）
	addrMap  sync.Map // map[string]*Session（key=peerAddr.String()）
	sockMap  sync.Map // map[uint32]*Session（key=peerSocketID）
	log      *zap.Logger
	conn     *net.UDPConn
	closed   atomic.Bool
	nextSock atomic.Uint32
}

// NewServer 创建 SRT 服务
func NewServer(config Config, handler common.EventHandler, log *zap.Logger) *Server {
	if log == nil {
		log = logger.Lg
	}
	s := &Server{
		config:  config,
		handler: handler,
		log:     log.With(zap.String("component", "srt-server")),
	}
	s.nextSock.Store(1000)
	return s
}

// Start 启动 SRT UDP 服务
func (s *Server) Start() error {
	addr, err := net.ResolveUDPAddr("udp", s.config.Addr)
	if err != nil {
		return fmt.Errorf("srt resolve addr %s: %w", s.config.Addr, err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("srt listen %s: %w", s.config.Addr, err)
	}
	s.conn = conn
	s.log.Info("srt server starting", zap.String("addr", s.config.Addr))

	go s.readLoop()
	go s.monitorLoop()
	return nil
}

// Close 关闭服务
func (s *Server) Close() error {
	s.closed.Store(true)
	if s.conn != nil {
		return s.conn.Close()
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

// CloseSession 关闭并移除会话
func (s *Server) CloseSession(id string) {
	if v, ok := s.sessions.LoadAndDelete(id); ok {
		sess := v.(*Session)
		s.addrMap.Delete(sess.peerAddr.String())
		s.sockMap.Delete(sess.peerSockID)
		_ = sess.Close()
	}
}

// sendTo 向对端发送数据。
func (s *Server) sendTo(addr *net.UDPAddr, data []byte) error {
	if s.conn == nil {
		return fmt.Errorf("srt: connection closed")
	}
	_, err := s.conn.WriteToUDP(data, addr)
	return err
}

// readLoop 读取 UDP 数据包循环。
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
			s.log.Error("srt read", zap.Error(err))
			continue
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		s.handlePacket(data, addr)
	}
}

// handlePacket 处理收到的 SRT 包。
func (s *Server) handlePacket(data []byte, addr *net.UDPAddr) {
	pkt, err := ParsePacket(data)
	if err != nil {
		s.log.Debug("srt parse packet", zap.String("remote", addr.String()), zap.Error(err))
		return
	}

	if pkt.IsControl {
		s.handleControl(pkt, addr)
	} else {
		s.handleData(pkt, addr)
	}
}

// handleControl 处理 control packet（握手 / keepalive / shutdown）。
func (s *Server) handleControl(pkt *Packet, addr *net.UDPAddr) {
	switch pkt.CtrlType {
	case ctrlHandshake:
		s.handleHandshake(pkt, addr)
	case ctrlKeepalive:
		s.handleKeepalive(pkt, addr)
	case ctrlShutdown:
		s.handleShutdown(pkt, addr)
	default:
		s.log.Debug("srt control packet", zap.String("remote", addr.String()),
			zap.Uint16("type", pkt.CtrlType))
	}
}

// handleHandshake 处理 HSv5 握手。
func (s *Server) handleHandshake(pkt *Packet, addr *net.UDPAddr) {
	hs, err := ParseHandshake(pkt.Payload)
	if err != nil {
		s.log.Error("srt parse handshake", zap.String("remote", addr.String()), zap.Error(err))
		return
	}

	s.log.Debug("srt handshake",
		zap.String("remote", addr.String()),
		zap.Uint32("version", hs.Version),
		zap.Uint32("hsType", uint32(hs.HandshakeType)),
		zap.Uint32("socketID", hs.SocketID),
		zap.String("streamID", hs.StreamID))

	switch hs.HandshakeType {
	case HsInduction:
		// 第一阶段：返回 induction 响应，分配 SYN cookie
		s.handleInduction(hs, pkt, addr)
	case HsConclusion:
		// 第二阶段：完成握手，创建会话
		s.handleConclusion(hs, pkt, addr)
	default:
		s.log.Debug("srt handshake type", zap.Uint32("type", uint32(hs.HandshakeType)))
	}
}

// handleInduction 处理 induction 阶段。
func (s *Server) handleInduction(hs *HandshakePacket, pkt *Packet, addr *net.UDPAddr) {
	// 分配 SYN cookie（简化：用时间戳）
	cookie := uint32(time.Now().UnixNano() & 0xFFFFFFFF)

	resp := &HandshakePacket{
		Version:       hsVersionV5,
		Encryption:    0,
		Extension:     hs.Extension,
		InitSeqNum:    hs.InitSeqNum,
		MTU:           hsMtuDefault,
		MaxFlowWindow: hsFlowDefault,
		HandshakeType: HsInduction,
		SocketID:      0, // induction 阶段 socket ID = 0
		SynCookie:     cookie,
	}
	resp.PeerIP = hs.PeerIP

	// 发送 induction 响应（dest socket ID = 0）
	respData := BuildHandshakeResponse(resp, 0)
	if err := s.sendTo(addr, respData); err != nil {
		s.log.Error("srt send induction response", zap.Error(err))
	}
}

// handleConclusion 处理 conclusion 阶段，创建会话。
func (s *Server) handleConclusion(hs *HandshakePacket, pkt *Packet, addr *net.UDPAddr) {
	// 分配本端 socket ID
	localSockID := s.nextSock.Add(1)

	sessionID := uuid.NewString()
	session := newSession(sessionID, addr, localSockID, hs.StreamID, s.handler, s)
	session.SetPeerSocketID(hs.SocketID)

	s.sessions.Store(sessionID, session)
	s.addrMap.Store(addr.String(), session)
	s.sockMap.Store(hs.SocketID, session)

	s.log.Info("srt session established",
		zap.String("session", sessionID),
		zap.String("remote", addr.String()),
		zap.String("streamID", hs.StreamID),
		zap.Uint32("localSock", localSockID),
		zap.Uint32("peerSock", hs.SocketID))

	// 发送 conclusion 响应
	resp := &HandshakePacket{
		Version:       hsVersionV5,
		Encryption:    0,
		Extension:     hs.Extension,
		InitSeqNum:    hs.InitSeqNum,
		MTU:           hsMtuDefault,
		MaxFlowWindow: hsFlowDefault,
		HandshakeType: HsConclusion,
		SocketID:      localSockID,
		SynCookie:     hs.SynCookie,
	}
	resp.PeerIP = hs.PeerIP

	respData := BuildHandshakeResponse(resp, hs.SocketID)
	if err := s.sendTo(addr, respData); err != nil {
		s.log.Error("srt send conclusion response", zap.Error(err))
	}

	// 通知上层
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolSRT,
		SessionID: sessionID,
		From:      addr.String(),
		To:        hs.StreamID,
		Timestamp: time.Now(),
	})

	// 通知轨道就绪（SRT 默认 TS 流，固定 video track）
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventTrackAdded,
		Protocol:  common.ProtocolSRT,
		SessionID: sessionID,
		Track: &common.TrackInfo{
			ID:        "video",
			Kind:      common.TrackVideo,
			Direction: common.TrackRecv,
			Codec:     common.CodecH264,
			StreamID:  hs.StreamID,
		},
		Timestamp: time.Now(),
	})

	// 媒体就绪
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventAnswered,
		Protocol:  common.ProtocolSRT,
		SessionID: sessionID,
		Timestamp: time.Now(),
	})
}

// handleKeepalive 处理 keepalive。
func (s *Server) handleKeepalive(pkt *Packet, addr *net.UDPAddr) {
	session := s.sessionByAddr(addr)
	if session != nil {
		session.touch()
		// 回复 keepalive
		ka := BuildKeepalive(session.peerSockID)
		_ = s.sendTo(addr, ka)
	}
}

// handleShutdown 处理 shutdown，关闭会话。
func (s *Server) handleShutdown(pkt *Packet, addr *net.UDPAddr) {
	session := s.sessionByAddr(addr)
	if session == nil {
		return
	}
	s.cleanupSession(session)
}

// handleData 处理 data packet，提取媒体数据转发上层。
func (s *Server) handleData(pkt *Packet, addr *net.UDPAddr) {
	session := s.sessionByAddr(addr)
	if session == nil {
		// 没有握手过的数据包：尝试 data-only 模式（跳过握手）
		session = s.getOrCreateDataOnlySession(addr)
	}
	session.touch()

	// 提取 payload：可能是 TS 包或 RTP 包
	payload := pkt.Payload
	if len(payload) == 0 {
		return
	}

	// 判断是 TS 还是 RTP：
	// TS 包以 0x47 同步字节开头，固定 188 字节
	// RTP 包第一个字节 version=2 (0x80)
	if payload[0] == 0x47 {
		s.handleTSPayload(session, payload)
	} else if (payload[0] & 0xC0) == 0x80 {
		s.handleRtpPayload(session, payload)
	} else {
		// 未知格式，作为原始数据转发
		s.handler.OnMediaFrame(session.id, "video", common.MediaFrame{
			Type:      common.FrameVideo,
			Codec:     common.CodecH264,
			Payload:   payload,
			Timestamp: pkt.Timestamp,
			Sequence:  uint16(pkt.SeqNum),
		})
	}
}

// handleTSPayload 处理 MPEG-TS payload（可能包含多个 188 字节 TS 包）。
func (s *Server) handleTSPayload(session *Session, data []byte) {
	// TS 包固定 188 字节
	const tsPacketSize = 188
	off := 0
	for off+tsPacketSize <= len(data) {
		ts := data[off : off+tsPacketSize]
		if ts[0] != 0x47 {
			off++
			continue
		}
		// 提取 PID（13 bit）
		pid := uint16(ts[1]&0x1F)<<8 | uint16(ts[2])
		// payload unit start indicator
		pusi := (ts[1] & 0x40) != 0
		_ = pid
		_ = pusi

		// 简化：把整个 TS 包作为 payload 转发上层（上层负责解复用）
		// 实际生产中需要按 PID 重组 PES，这里保持简单
		off += tsPacketSize
	}
	// 整体转发 TS 数据
	s.handler.OnMediaFrame(session.id, "video", common.MediaFrame{
		Type:      common.FrameVideo,
		Codec:     common.CodecH264,
		Payload:   data,
		Timestamp: 0,
	})
}

// handleRtpPayload 处理 RTP payload。
func (s *Server) handleRtpPayload(session *Session, data []byte) {
	// 解析 RTP 头
	if len(data) < 12 {
		return
	}
	// RTP header: V(2) P(1) X(1) CC(4) M(1) PT(7) seq(16) ts(32) ssrc(32)
	v := (data[0] >> 6) & 0x03
	if v != 2 {
		return
	}
	cc := int(data[0] & 0x0F)
	marker := (data[1] & 0x80) != 0
	pt := data[1] & 0x7F
	seq := uint16(data[2])<<8 | uint16(data[3])
	ts := uint32(data[4])<<24 | uint32(data[5])<<16 | uint32(data[6])<<8 | uint32(data[7])
	ssrc := uint32(data[8])<<24 | uint32(data[9])<<16 | uint32(data[10])<<8 | uint32(data[11])

	headerLen := 12 + cc*4
	if len(data) < headerLen {
		return
	}
	payload := data[headerLen:]

	// 根据 PT 推断 codec
	codec := common.CodecH264
	kind := common.TrackVideo
	if pt < 96 {
		// 静态 PT
		switch pt {
		case 0, 8: // PCMU, PCMA
			codec = common.CodecPCMU
			kind = common.TrackAudio
		case 96:
			codec = common.CodecH264
		}
	}

	trackID := common.TrackID("video")
	if kind == common.TrackAudio {
		trackID = "audio"
	}

	s.handler.OnMediaFrame(session.id, trackID, common.MediaFrame{
		Type:      common.FrameTypeFromKind(kind),
		Codec:     codec,
		Payload:   payload,
		Timestamp: ts,
		Sequence:  seq,
		SSRC:      ssrc,
		Marker:    marker,
	})
}

// sessionByAddr 通过对端地址查找会话。
func (s *Server) sessionByAddr(addr *net.UDPAddr) *Session {
	v, ok := s.addrMap.Load(addr.String())
	if !ok {
		return nil
	}
	return v.(*Session)
}

// getOrCreateDataOnlySession data-only 模式：跳过握手，直接创建会话。
func (s *Server) getOrCreateDataOnlySession(addr *net.UDPAddr) *Session {
	if v, ok := s.addrMap.Load(addr.String()); ok {
		return v.(*Session)
	}
	sessionID := uuid.NewString()
	localSock := s.nextSock.Add(1)
	session := newSession(sessionID, addr, localSock, "", s.handler, s)
	s.sessions.Store(sessionID, session)
	s.addrMap.Store(addr.String(), session)

	s.log.Info("srt data-only session",
		zap.String("session", sessionID),
		zap.String("remote", addr.String()))

	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolSRT,
		SessionID: sessionID,
		From:      addr.String(),
		Timestamp: time.Now(),
	})
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventTrackAdded,
		Protocol:  common.ProtocolSRT,
		SessionID: sessionID,
		Track: &common.TrackInfo{
			ID:        "video",
			Kind:      common.TrackVideo,
			Direction: common.TrackRecv,
			Codec:     common.CodecH264,
		},
		Timestamp: time.Now(),
	})
	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventAnswered,
		Protocol:  common.ProtocolSRT,
		SessionID: sessionID,
		Timestamp: time.Now(),
	})
	return session
}

// cleanupSession 清理会话。
func (s *Server) cleanupSession(session *Session) {
	s.sessions.Delete(session.id)
	s.addrMap.Delete(session.peerAddr.String())
	s.sockMap.Delete(session.peerSockID)
	_ = session.Close()

	s.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventHangup,
		Protocol:  common.ProtocolSRT,
		SessionID: session.id,
		Timestamp: time.Now(),
	})
}

// monitorLoop 定期清理超时会话。
func (s *Server) monitorLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if s.closed.Load() {
			return
		}
		now := time.Now()
		s.sessions.Range(func(key, value any) bool {
			sess := value.(*Session)
			if now.Sub(sess.LastActive()) > 30*time.Second {
				s.log.Info("srt session timeout", zap.String("session", sess.id))
				s.cleanupSession(sess)
			}
			return true
		})
	}
}
