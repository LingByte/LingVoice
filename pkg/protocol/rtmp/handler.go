package rtmp

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ─── H.264/AVC + AAC 常量 ───────────────────────────────────────────────────

const (
	// RTMP video codec IDs
	codecIDAVC  = 7
	codecIDHEVC = 12 // 部分扩展用

	// RTMP video frame types
	frameKey    = 1
	frameInter  = 2
	frameDisp   = 3
	frameKeyDisp = 4

	// AVC packet types
	avcSeqHeader = 0
	avcNALU      = 1
	avcEnd       = 2

	// RTMP audio sound format
	soundFormatAAC = 10

	// AAC packet types
	aacSeqHeader = 0
	aacRaw       = 1
)

// ─── Conn：RTMP 连接处理器 ──────────────────────────────────────────────────

// Conn 封装一条 RTMP 连接的协议处理。
// 负责 handshake 之后的 chunk 读取、控制消息处理、命令处理、媒体数据转发。
type Conn struct {
	conn       net.Conn
	reader     *ChunkReader
	writer     *ChunkWriter
	writerMu   sync.Mutex // 保护 writer（控制消息和命令响应共用）
	handler    common.EventHandler
	log        *zap.Logger

	sessionID  string
	streamKey  string
	remote     string
	streamID   uint32 // createStream 分配的 stream id（通常 1）

	// 媒体状态
	videoSSRC    uint32
	videoSeq     uint16
	audioSSRC    uint32
	audioSeq     uint16
	videoClock   uint32 // 视频时钟基准（ms）
	audioClock   uint32 // 音频时钟基准（ms）
	hasVideo     bool
	hasAudio     bool
	videoTrackReported bool
	audioTrackReported bool
	avcConfig    *avcDecoderConfig // AVC sequence header 解析结果
	aacConfig    *aacDecoderConfig  // AAC sequence header 解析结果

	// play/egress 状态
	playMode        bool       // true 表示当前连接处于 play（拉流）模式
	playSess        *playSession // play 模式下的媒体发送会话

	// 控制状态
	windowAckSize   uint32
	bytesReceived   uint32
	chunkSize       int
	closed          bool
	mu              sync.Mutex
}

// avcDecoderConfig 保存 AVCDecoderConfigurationRecord 解析结果
type avcDecoderConfig struct {
	sps []byte
	pps []byte
}

// aacDecoderConfig 保存 AudioSpecificConfig 解析结果
type aacDecoderConfig struct {
	sampleRate uint32
	channels   uint16
}

// NewConn 创建 RTMP 连接处理器
func NewConn(conn net.Conn, handler common.EventHandler, log *zap.Logger) *Conn {
	return &Conn{
		conn:          conn,
		reader:        NewChunkReader(conn),
		writer:        NewChunkWriter(conn),
		handler:       handler,
		log:           log,
		sessionID:     uuid.NewString(),
		remote:        conn.RemoteAddr().String(),
		windowAckSize: defaultWindowAckSize,
		chunkSize:     defaultChunkSize,
		videoSSRC:     randUint32(),
		audioSSRC:     randUint32(),
	}
}

// Serve 处理一条 RTMP 连接的完整生命周期。
// 调用前应已完成 handshake。
func (c *Conn) Serve() error {
	defer c.cleanup()

	// 通知上层：连接建立（相当于来电）
	c.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolRTMP,
		SessionID: c.sessionID,
		From:      c.remote,
		To:        c.streamKey,
		Timestamp: time.Now(),
	})

	for {
		c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		msg, err := c.reader.ReadMessage()
		if err != nil {
			if c.isClosed() {
				return nil
			}
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read message: %w", err)
		}

		// 统计接收字节数用于 Acknowledgement
		c.bytesReceived += uint32(len(msg.Payload))
		if c.windowAckSize > 0 && c.bytesReceived >= c.windowAckSize {
			c.sendAcknowledgement(c.bytesReceived)
			c.bytesReceived = 0
		}

		if err := c.handleMessage(msg); err != nil {
			return err
		}
	}
}

// handleMessage 根据 message type 分发处理
func (c *Conn) handleMessage(msg *Message) error {
	switch msg.Type {
	case MsgSetChunkSize:
		return c.handleSetChunkSize(msg)

	case MsgWindowAckSize:
		return c.handleWindowAckSize(msg)

	case MsgSetPeerBandwidth:
		// 客户端发来的，通常忽略
		return nil

	case MsgAcknowledgement:
		// 客户端的 ack，忽略
		return nil

	case MsgUserControl:
		// 用户控制消息，忽略（如 PingRequest）
		return nil

	case MsgCommandAMF0, MsgCommandAMF3:
		return c.handleCommand(msg)

	case MsgAudio:
		return c.handleAudio(msg)

	case MsgVideo:
		return c.handleVideo(msg)

	case MsgDataAMF0, MsgDataAMF3:
		// onMetaData 等，忽略（或可转发）
		return nil

	case MsgAggregate:
		return nil

	default:
		c.log.Debug("rtmp unknown message type", zap.Uint8("type", msg.Type))
		return nil
	}
}

// ─── 协议控制消息 ────────────────────────────────────────────────────────────

func (c *Conn) handleSetChunkSize(msg *Message) error {
	if len(msg.Payload) < 4 {
		return fmt.Errorf("set chunk size: payload too short")
	}
	size := binary.BigEndian.Uint32(msg.Payload[:4])
	if size == 0 {
		return fmt.Errorf("set chunk size: zero")
	}
	c.chunkSize = int(size)
	c.reader.SetChunkSize(c.chunkSize)
	c.log.Debug("rtmp chunk size updated", zap.Int("size", c.chunkSize))
	return nil
}

func (c *Conn) handleWindowAckSize(msg *Message) error {
	if len(msg.Payload) < 4 {
		return nil
	}
	c.windowAckSize = binary.BigEndian.Uint32(msg.Payload[:4])
	return nil
}

// sendAcknowledgement 发送 Acknowledgement 控制消息
func (c *Conn) sendAcknowledgement(seq uint32) {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, seq)
	c.writeControlMessage(MsgAcknowledgement, payload)
}

// sendWindowAckSize 发送 Window Acknowledgement Size
func (c *Conn) sendWindowAckSize(size uint32) {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, size)
	c.writeControlMessage(MsgWindowAckSize, payload)
}

// sendSetPeerBandwidth 发送 Set Peer Bandwidth
func (c *Conn) sendSetPeerBandwidth(size uint32, limitType uint8) {
	payload := make([]byte, 5)
	binary.BigEndian.PutUint32(payload[:4], size)
	payload[4] = limitType
	c.writeControlMessage(MsgSetPeerBandwidth, payload)
}

// sendSetChunkSize 发送 Set Chunk Size
func (c *Conn) sendSetChunkSize(size uint32) {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, size)
	c.writeControlMessage(MsgSetChunkSize, payload)
	c.writer.SetChunkSize(int(size))
}

// writeControlMessage 写一个协议控制消息（CSID 2, stream 0）
func (c *Conn) writeControlMessage(msgType uint8, payload []byte) {
	c.writerMu.Lock()
	defer c.writerMu.Unlock()
	_ = c.writer.WriteMessage(&Message{
		Type:      msgType,
		CSID:      2,
		StreamID:  0,
		Timestamp: 0,
		Payload:   payload,
	})
}

// writeCommandMessage 写一个命令消息（CSID 3, 指定 stream）
func (c *Conn) writeCommandMessage(streamID uint32, payload []byte) error {
	c.writerMu.Lock()
	defer c.writerMu.Unlock()
	return c.writer.WriteMessage(&Message{
		Type:      MsgCommandAMF0,
		CSID:      3,
		StreamID:  streamID,
		Timestamp: 0,
		Payload:   payload,
	})
}

// ─── 命令处理 ────────────────────────────────────────────────────────────────

func (c *Conn) handleCommand(msg *Message) error {
	values, err := DecodeAMF0(msg.Payload)
	if err != nil {
		return fmt.Errorf("decode command: %w", err)
	}
	if len(values) == 0 {
		return fmt.Errorf("empty command")
	}

	cmdName, ok := amfString(values[0])
	if !ok {
		return fmt.Errorf("command name is not string")
	}

	// transactionID 通常是第 2 个值
	var txnID float64
	if len(values) > 1 {
		txnID, _ = amfNumber(values[1])
	}

	switch cmdName {
	case "connect":
		return c.handleConnect(values, txnID)

	case "createStream":
		return c.handleCreateStream(values, txnID)

	case "publish":
		return c.handlePublish(values, msg.StreamID)

	case "play":
		return c.handlePlay(values, msg.StreamID)

	case "deleteStream", "closeStream", "releaseStream":
		// 忽略
		return nil

	case "_checkbw", "_result", "onStatus", "onBWDone":
		// 客户端发来的响应，忽略
		return nil

	default:
		c.log.Debug("rtmp unhandled command", zap.String("cmd", cmdName))
		return nil
	}
}

// handleConnect 处理 connect 命令
func (c *Conn) handleConnect(values []AMFValue, txnID float64) error {
	// connect: commandName, txnID, commandObject, optional args
	// 提取 app / tcUrl
	if len(values) > 2 {
		if obj, ok := amfObject(values[2]); ok {
			if app, ok := obj["app"]; ok {
				if s, ok := amfString(app); ok {
					c.streamKey = s
				}
			}
			if tc, ok := obj["tcUrl"]; ok {
				if s, ok := amfString(tc); ok {
					_ = s // 可用于鉴权
				}
			}
		}
	}

	c.log.Info("rtmp connect",
		zap.String("session", c.sessionID),
		zap.String("app", c.streamKey),
		zap.String("remote", c.remote))

	// 1. 发送 Window Acknowledgement Size
	c.sendWindowAckSize(defaultWindowAckSize)
	// 2. 发送 Set Peer Bandwidth (dynamic limit)
	c.sendSetPeerBandwidth(defaultWindowAckSize, 2)
	// 3. 发送 Set Chunk Size（服务端用更大的 chunk size 提升效率）
	c.sendSetChunkSize(4096)
	// 4. 发送 _result
	w := newAMFWriter()
	w.WriteString("_result")
	w.WriteNumber(txnID)
	w.WriteObject(ConnectResultProps())
	w.WriteObject(ConnectResultInfo())
	if err := c.writeCommandMessage(0, w.Bytes()); err != nil {
		return fmt.Errorf("send connect result: %w", err)
	}
	return nil
}

// handleCreateStream 处理 createStream 命令
func (c *Conn) handleCreateStream(values []AMFValue, txnID float64) error {
	c.streamID = 1 // 分配 stream id 1

	w := newAMFWriter()
	w.WriteString("_result")
	w.WriteNumber(txnID)
	w.WriteNull()
	w.WriteNumber(float64(c.streamID))
	if err := c.writeCommandMessage(0, w.Bytes()); err != nil {
		return fmt.Errorf("send createStream result: %w", err)
	}
	c.log.Debug("rtmp createStream", zap.Uint32("streamID", c.streamID))
	return nil
}

// handlePublish 处理 publish 命令
func (c *Conn) handlePublish(values []AMFValue, streamID uint32) error {
	// publish: commandName, txnID, null, streamName, record/live
	streamName := ""
	if len(values) > 3 {
		if s, ok := amfString(values[3]); ok {
			streamName = s
		}
	}
	if streamName != "" {
		c.streamKey = c.streamKey + "/" + streamName
	}

	c.log.Info("rtmp publish",
		zap.String("session", c.sessionID),
		zap.String("stream", c.streamKey))

	// 回复 onStatus NetStream.Publish.Start
	info := PublishStatusInfo("NetStream.Publish.Start", "Started publishing stream.")
	c.sendOnStatus(streamID, "status", info)

	// 通知上层：轨道就绪（RTMP 固定音视频轨道，等收到 sequence header 后再上报具体 codec）
	// 这里先不上报，等收到第一个 audio/video sequence header 再上报 TrackAdded。
	return nil
}

// handlePlay 处理 play 命令（play/egress 模式：服务端向客户端推流）
func (c *Conn) handlePlay(values []AMFValue, streamID uint32) error {
	streamName := ""
	if len(values) > 3 {
		if s, ok := amfString(values[3]); ok {
			streamName = s
		}
	}
	if streamName != "" {
		c.streamKey = c.streamKey + "/" + streamName
	}

	c.log.Info("rtmp play", zap.String("session", c.sessionID), zap.String("stream", c.streamKey))

	// 标记为 play 模式
	c.playMode = true

	// 创建 playSession 并启动媒体发送协程
	c.playSess = newPlaySession(c, c.log)
	c.playSess.start()

	// 1. 发送 Stream Begin user control
	c.sendUserControl(UCStreamBegin, streamID)

	// 2. 回复 onStatus NetStream.Play.Reset
	resetInfo := PublishStatusInfo("NetStream.Play.Reset", "Playing and resetting stream.")
	c.sendOnStatus(streamID, "status", resetInfo)

	// 3. 回复 onStatus NetStream.Play.Start
	startInfo := PublishStatusInfo("NetStream.Play.Start", "Started playing stream.")
	c.sendOnStatus(streamID, "status", startInfo)

	// 4. 发送 onMetaData（如果已有轨道信息）
	c.playSess.sendOnMetaData(streamID)

	// 5. 通知上层：play 轨道就绪（send 方向），上层可通过 SendMediaFrame 推送媒体帧
	c.reportPlayTracks()

	return nil
}

// sendOnStatus 发送 onStatus 命令
func (c *Conn) sendOnStatus(streamID uint32, level string, info map[string]AMFValue) {
	w := newAMFWriter()
	w.WriteString("onStatus")
	w.WriteNumber(0) // txnID = 0
	w.WriteNull()
	w.WriteObject(info)
	_ = c.writeCommandMessage(streamID, w.Bytes())
}

// sendUserControl 发送 User Control Message
func (c *Conn) sendUserControl(eventType uint16, streamID uint32) {
	payload := make([]byte, 6)
	payload[0] = byte(eventType >> 8)
	payload[1] = byte(eventType)
	binary.BigEndian.PutUint32(payload[2:6], streamID)
	c.writeControlMessage(MsgUserControl, payload)
}

// ─── 媒体数据处理：H.264/AVC → RTP ──────────────────────────────────────────

func (c *Conn) handleVideo(msg *Message) error {
	if len(msg.Payload) < 5 {
		return nil
	}
	first := msg.Payload[0]
	frameType := first >> 4
	codecID := first & 0x0f

	if codecID != codecIDAVC {
		c.log.Debug("rtmp video: non-AVC codec, skip", zap.Uint8("codec", codecID))
		return nil
	}

	avcType := msg.Payload[1]
	// composition time (24-bit)
	// ct := uint32(msg.Payload[2])<<16 | uint32(msg.Payload[3])<<8 | uint32(msg.Payload[4])
	data := msg.Payload[5:]

	switch avcType {
	case avcSeqHeader:
		return c.handleAVCSequenceHeader(data, frameType)

	case avcNALU:
		return c.handleAVCNALUs(data, msg.Timestamp, frameType)

	case avcEnd:
		return nil
	}
	return nil
}

// handleAVCSequenceHeader 解析 AVCDecoderConfigurationRecord
func (c *Conn) handleAVCSequenceHeader(data []byte, frameType uint8) error {
	if len(data) < 7 {
		return fmt.Errorf("avc seq header: too short")
	}
	// AVCDecoderConfigurationRecord:
	//   configurationVersion(1) + AVCProfileIndication(1) + profile_compatibility(1)
	//   + AVCLevelIndication(1) + lengthSizeMinusOne(1, low 2 bits) + numOfSPS(1, low 5 bits)
	cfg := &avcDecoderConfig{}
	pos := 5
	numSPS := int(data[pos] & 0x1f)
	pos++
	for i := 0; i < numSPS; i++ {
		if pos+2 > len(data) {
			return fmt.Errorf("avc seq header: sps overflow")
		}
		n := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		pos += 2
		if pos+n > len(data) {
			return fmt.Errorf("avc seq header: sps data overflow")
		}
		if cfg.sps == nil {
			cfg.sps = make([]byte, n)
			copy(cfg.sps, data[pos:pos+n])
		}
		pos += n
	}
	if pos >= len(data) {
		return fmt.Errorf("avc seq header: no pps")
	}
	numPPS := int(data[pos])
	pos++
	for i := 0; i < numPPS; i++ {
		if pos+2 > len(data) {
			return fmt.Errorf("avc seq header: pps overflow")
		}
		n := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		pos += 2
		if pos+n > len(data) {
			return fmt.Errorf("avc seq header: pps data overflow")
		}
		if cfg.pps == nil {
			cfg.pps = make([]byte, n)
			copy(cfg.pps, data[pos:pos+n])
		}
		pos += n
	}
	c.avcConfig = cfg
	c.hasVideo = true
	c.videoClock = 0

	// 上报 video track
	if !c.videoTrackReported {
		c.reportVideoTrack()
	}

	c.log.Debug("rtmp avc sequence header parsed",
		zap.Int("spsLen", len(cfg.sps)),
		zap.Int("ppsLen", len(cfg.pps)))
	return nil
}

// handleAVCNALUs 解析 AVC NALU 数据（4 字节长度前缀格式）并转为 RTP 包
func (c *Conn) handleAVCNALUs(data []byte, timestamp uint32, frameType uint8) error {
	if c.avcConfig == nil {
		// 没有 sequence header，无法处理
		return nil
	}

	// RTMP AVC NALU 使用 4 字节长度前缀（lengthSizeMinusOne=3）
	// 解析所有 NALU
	var nalus [][]byte
	pos := 0
	for pos+4 <= len(data) {
		n := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		pos += 4
		if pos+n > len(data) {
			break
		}
		nalus = append(nalus, data[pos:pos+n])
		pos += n
	}

	if len(nalus) == 0 {
		return nil
	}

	// 更新视频时钟（RTMP timestamp 单位 ms，RTP 视频时钟 90000Hz）
	rtpTS := timestamp * 90 // ms → 90kHz

	isKeyframe := frameType == frameKey || frameType == frameKeyDisp

	// 每个 NALU 发一个 RTP 包（Single NALU Packet Mode, RFC 6184）
	// 最后一个 NALU 设置 marker bit
	for i, nal := range nalus {
		marker := i == len(nalus)-1
		frame := common.MediaFrame{
			Type:      common.FrameVideo,
			Codec:     common.CodecH264,
			Payload:   nal,
			Timestamp: rtpTS,
			Sequence:  c.videoSeq,
			SSRC:      c.videoSSRC,
			SampleRate: 90000,
			Marker:    marker,
		}
		c.videoSeq++
		if err := c.handler.OnMediaFrame(c.sessionID, "video", frame); err != nil {
			c.log.Debug("rtmp forward video frame failed", zap.Error(err))
		}
	}

	_ = isKeyframe
	return nil
}

// ─── 媒体数据处理：AAC → RTP ──────────────────────────────────────────────────

func (c *Conn) handleAudio(msg *Message) error {
	if len(msg.Payload) < 2 {
		return nil
	}
	first := msg.Payload[0]
	soundFormat := first >> 4
	if soundFormat != soundFormatAAC {
		c.log.Debug("rtmp audio: non-AAC format, skip", zap.Uint8("format", soundFormat))
		return nil
	}

	aacType := msg.Payload[1]
	data := msg.Payload[2:]

	switch aacType {
	case aacSeqHeader:
		return c.handleAACSequenceHeader(data)

	case aacRaw:
		return c.handleAACRaw(data, msg.Timestamp)
	}
	return nil
}

// handleAACSequenceHeader 解析 AudioSpecificConfig
func (c *Conn) handleAACSequenceHeader(data []byte) error {
	if len(data) < 2 {
		return fmt.Errorf("aac seq header: too short")
	}
	// AudioSpecificConfig:
	//   audioObjectType(5 bits) + samplingFrequencyIndex(4 bits) + channelConfiguration(4 bits)
	cfg := &aacDecoderConfig{}
	// 解析前 2 字节
	b0 := data[0]
	b1 := data[1]
	aot := (b0 >> 3) & 0x1f
	sampleIdx := ((b0 & 0x07) << 1) | (b1 >> 7)
	chanCfg := (b1 >> 3) & 0x0f

	// sampling frequency index 表
	sampleRates := []uint32{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}
	if int(sampleIdx) < len(sampleRates) {
		cfg.sampleRate = sampleRates[sampleIdx]
	} else {
		cfg.sampleRate = 44100
	}
	cfg.channels = uint16(chanCfg)
	_ = aot

	c.aacConfig = cfg
	c.hasAudio = true
	c.audioClock = 0

	// 上报 audio track
	if !c.audioTrackReported {
		c.reportAudioTrack()
	}

	c.log.Debug("rtmp aac sequence header parsed",
		zap.Uint32("sampleRate", cfg.sampleRate),
		zap.Uint16("channels", cfg.channels))
	return nil
}

// handleAACRaw 处理 raw AAC 帧并转为 RTP 包
func (c *Conn) handleAACRaw(data []byte, timestamp uint32) error {
	if c.aacConfig == nil {
		return nil
	}
	if len(data) == 0 {
		return nil
	}

	// AAC RTP 时钟 = sample rate
	rtpTS := timestamp * (c.aacConfig.sampleRate / 1000) // ms → sample rate Hz

	frame := common.MediaFrame{
		Type:       common.FrameAudio,
		Codec:      common.CodecAAC,
		Payload:    data,
		Timestamp:  rtpTS,
		Sequence:   c.audioSeq,
		SSRC:       c.audioSSRC,
		SampleRate: c.aacConfig.sampleRate,
		Channels:   c.aacConfig.channels,
		Marker:     true, // AAC 每帧一个 RTP 包，marker 置位
	}
	c.audioSeq++
	if err := c.handler.OnMediaFrame(c.sessionID, "audio", frame); err != nil {
		c.log.Debug("rtmp forward audio frame failed", zap.Error(err))
	}
	return nil
}

// ─── Track 上报 ──────────────────────────────────────────────────────────────

// reportVideoTrack 上报视频轨道就绪
func (c *Conn) reportVideoTrack() {
	c.videoTrackReported = true
	ti := common.TrackInfo{
		ID:        "video",
		Kind:      common.TrackVideo,
		Direction: common.TrackRecv,
		Codec:     common.CodecH264,
		StreamID:  c.streamKey,
	}
	c.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventTrackAdded,
		Protocol:  common.ProtocolRTMP,
		SessionID: c.sessionID,
		Track:     &ti,
		Timestamp: time.Now(),
	})
}

// reportAudioTrack 上报音频轨道就绪
func (c *Conn) reportAudioTrack() {
	c.audioTrackReported = true
	ti := common.TrackInfo{
		ID:         "audio",
		Kind:       common.TrackAudio,
		Direction:  common.TrackRecv,
		Codec:      common.CodecAAC,
		SampleRate: c.aacConfig.sampleRate,
		Channels:   c.aacConfig.channels,
		StreamID:   c.streamKey,
	}
	c.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventTrackAdded,
		Protocol:  common.ProtocolRTMP,
		SessionID: c.sessionID,
		Track:     &ti,
		Timestamp: time.Now(),
	})
}

// reportPlayTracks 上报 play 模式的轨道（send 方向）
func (c *Conn) reportPlayTracks() {
	// 视频轨道
	ti := common.TrackInfo{
		ID:        "video",
		Kind:      common.TrackVideo,
		Direction: common.TrackSend,
		Codec:     common.CodecH264,
		StreamID:  c.streamKey,
	}
	c.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventTrackAdded,
		Protocol:  common.ProtocolRTMP,
		SessionID: c.sessionID,
		Track:     &ti,
		Timestamp: time.Now(),
	})

	// 音频轨道
	ai := common.TrackInfo{
		ID:        "audio",
		Kind:      common.TrackAudio,
		Direction: common.TrackSend,
		Codec:     common.CodecAAC,
		StreamID:  c.streamKey,
	}
	c.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventTrackAdded,
		Protocol:  common.ProtocolRTMP,
		SessionID: c.sessionID,
		Track:     &ai,
		Timestamp: time.Now(),
	})
}

// writeDataMessage 写一个数据消息（如 onMetaData），CSID 4
func (c *Conn) writeDataMessage(streamID uint32, payload []byte) error {
	c.writerMu.Lock()
	defer c.writerMu.Unlock()
	return c.writer.WriteMessage(&Message{
		Type:      MsgDataAMF0,
		CSID:      4,
		StreamID:  streamID,
		Timestamp: 0,
		Payload:   payload,
	})
}

// writeMediaMessage 写一个音频/视频消息
func (c *Conn) writeMediaMessage(msgType uint8, streamID uint32, timestamp uint32, payload []byte) error {
	c.writerMu.Lock()
	defer c.writerMu.Unlock()
	return c.writer.WriteMessage(&Message{
		Type:      msgType,
		CSID:      6, // 媒体消息常用 CSID 6/7（配合大 chunk size）
		StreamID:  streamID,
		Timestamp: timestamp,
		Payload:   payload,
	})
}

// ─── 生命周期 ────────────────────────────────────────────────────────────────

func (c *Conn) cleanup() {
	// 停止 play session 的媒体发送协程
	if c.playSess != nil {
		c.playSess.stop()
	}
	c.handler.OnEvent(common.ProtocolEvent{
		Type:      common.EventHangup,
		Protocol:  common.ProtocolRTMP,
		SessionID: c.sessionID,
		Timestamp: time.Now(),
	})
	_ = c.conn.Close()
}

func (c *Conn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// Close 关闭连接
func (c *Conn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.conn.Close()
}

// ─── 辅助 ────────────────────────────────────────────────────────────────────

func randUint32() uint32 {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return binary.BigEndian.Uint32(b)
}

// SessionID 返回会话 ID
func (c *Conn) SessionID() string { return c.sessionID }

// StreamKey 返回流 key
func (c *Conn) StreamKey() string { return c.streamKey }

// ID 实现 common.SignalSession 接口
func (c *Conn) ID() string { return c.sessionID }

// Protocol 实现 common.SignalSession 接口
func (c *Conn) Protocol() common.ProtocolType { return common.ProtocolRTMP }

// SendCommand 实现 common.SignalSession 接口
func (c *Conn) SendCommand(cmd common.ProtocolCommand) error {
	switch cmd.Type {
	case common.CmdHangup:
		return c.Close()
	default:
		return nil
	}
}

// ─── MediaSession 接口实现（play 模式） ──────────────────────────────────────

// SendMediaFrame 实现 common.MediaSession 接口。
// 在 play 模式下，将媒体帧转为 RTMP 消息发送给客户端。
func (c *Conn) SendMediaFrame(trackID common.TrackID, frame common.MediaFrame) error {
	if c.playSess == nil {
		return fmt.Errorf("rtmp: not in play mode")
	}
	return c.playSess.SendMediaFrame(trackID, frame)
}

// Tracks 实现 common.MediaSession 接口
func (c *Conn) Tracks() []common.TrackInfo {
	if c.playSess == nil {
		return nil
	}
	return c.playSess.Tracks()
}

// MediaStats 实现 common.MediaSession 接口
func (c *Conn) MediaStats() map[common.TrackID]common.TrackStats {
	if c.playSess == nil {
		return nil
	}
	return c.playSess.MediaStats()
}

// SetVideoConfig 设置视频编码参数（SPS/PPS），用于生成 AVC sequence header
func (c *Conn) SetVideoConfig(sps, pps []byte) {
	if c.playSess != nil {
		c.playSess.setVideoConfig(sps, pps)
	}
}

// SetAudioConfig 设置音频编码参数（AudioSpecificConfig），用于生成 AAC sequence header
func (c *Conn) SetAudioConfig(audioSpecificConfig []byte, sampleRate uint32, channels uint16) {
	if c.playSess != nil {
		c.playSess.setAudioConfig(audioSpecificConfig, sampleRate, channels)
	}
}
