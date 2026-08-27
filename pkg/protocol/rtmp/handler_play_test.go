package rtmp

import (
	"bytes"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"go.uber.org/zap"
)

// ─── 测试辅助 ─────────────────────────────────────────────────────────────────

// mockEventHandler 实现 common.EventHandler 用于测试
type mockEventHandler struct {
	mu       sync.Mutex
	events   []common.ProtocolEvent
	frames   []mockFrame
	dataMsgs []common.DataMessage
}

type mockFrame struct {
	sessionID string
	trackID   common.TrackID
	frame     common.MediaFrame
}

func (h *mockEventHandler) OnEvent(event common.ProtocolEvent) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, event)
	return nil
}

func (h *mockEventHandler) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.frames = append(h.frames, mockFrame{sessionID: sessionID, trackID: trackID, frame: frame})
	return nil
}

func (h *mockEventHandler) OnData(sessionID string, msg common.DataMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dataMsgs = append(h.dataMsgs, msg)
	return nil
}

func (h *mockEventHandler) getEvents() []common.ProtocolEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := make([]common.ProtocolEvent, len(h.events))
	copy(result, h.events)
	return result
}

// newTestConn 创建一对连接用于测试：返回服务端 Conn 和客户端 net.Conn
func newTestConn(t *testing.T) (*Conn, net.Conn) {
	t.Helper()
	srvConn, clientConn := net.Pipe()

	handler := &mockEventHandler{}
	log := zap.NewNop()
	c := NewConn(srvConn, handler, log)
	return c, clientConn
}

// ─── TestPlayCommandHandling: 验证 play 命令被正确识别和处理 ───────────────────

func TestPlayCommandHandling(t *testing.T) {
	srvConn, clientConn := newTestConn(t)
	defer srvConn.Close()
	defer clientConn.Close()

	// 构造 play 命令 AMF0 消息
	// play: commandName, txnID, null, streamName, start, duration, reset
	w := newAMFWriter()
	w.WriteString("play")
	w.WriteNumber(2.0) // txnID
	w.WriteNull()
	w.WriteString("teststream")
	w.WriteNumber(-2) // start (live)
	w.WriteNumber(-1) // duration
	w.WriteBoolean(true)

	playMsg := &Message{
		Type:      MsgCommandAMF0,
		CSID:      3,
		StreamID:  1,
		Timestamp: 0,
		Payload:   w.Bytes(),
	}

	// 设置 streamKey（模拟 connect 已处理）
	srvConn.streamKey = "live"
	srvConn.streamID = 1

	// 直接调用 handlePlay（在 goroutine 中，因为 net.Pipe 是同步的）
	values, err := DecodeAMF0(playMsg.Payload)
	if err != nil {
		t.Fatalf("decode play command: %v", err)
	}

	playErrCh := make(chan error, 1)
	go func() {
		playErrCh <- srvConn.handlePlay(values, playMsg.StreamID)
	}()

	// 从客户端读取服务端发送的响应消息
	// 服务端应该发送：StreamBegin user control + Play.Reset + Play.Start + onMetaData
	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))

	reader := NewChunkReader(clientConn)
	// 设置更大的 chunk size（服务端在 connect 时会设置 4096）
	reader.SetChunkSize(4096)

	var messages []*Message
	for i := 0; i < 4; i++ {
		msg, err := reader.ReadMessage()
		if err != nil {
			// 可能没有那么多消息（onMetaData 在没有配置时也会发送）
			break
		}
		messages = append(messages, msg)
	}

	// 等待 handlePlay 完成
	select {
	case err := <-playErrCh:
		if err != nil {
			t.Fatalf("handlePlay: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handlePlay timed out")
	}

	// 验证 playMode 已设置
	if !srvConn.playMode {
		t.Fatal("playMode should be true after handlePlay")
	}

	// 验证 playSession 已创建
	if srvConn.playSess == nil {
		t.Fatal("playSess should be created after handlePlay")
	}

	// 验证 streamKey 包含 stream name
	if srvConn.streamKey != "live/teststream" {
		t.Fatalf("streamKey = %q, want %q", srvConn.streamKey, "live/teststream")
	}

	// 验证收到了消息
	if len(messages) < 3 {
		t.Fatalf("expected at least 3 messages from server, got %d", len(messages))
	}

	// 第一条应该是 User Control (Stream Begin)
	if messages[0].Type != MsgUserControl {
		t.Fatalf("messages[0] type = %d, want %d (UserControl)", messages[0].Type, MsgUserControl)
	}

	// 检查是否有 onStatus 消息（Play.Reset 和 Play.Start）
	foundReset := false
	foundStart := false
	for _, msg := range messages {
		if msg.Type == MsgCommandAMF0 {
			values, err := DecodeAMF0(msg.Payload)
			if err != nil {
				continue
			}
			if len(values) >= 4 {
				if cmd, ok := amfString(values[0]); ok && cmd == "onStatus" {
					if info, ok := amfObject(values[3]); ok {
						if code, ok := info["code"]; ok {
							if s, ok := amfString(code); ok {
								if s == "NetStream.Play.Reset" {
									foundReset = true
								}
								if s == "NetStream.Play.Start" {
									foundStart = true
								}
							}
						}
					}
				}
			}
		}
	}

	if !foundReset {
		t.Error("missing NetStream.Play.Reset status")
	}
	if !foundStart {
		t.Error("missing NetStream.Play.Start status")
	}

	// 清理 play session
	srvConn.playSess.stop()
}

// ─── TestSendMediaFrame: 验证帧被正确转为 RTMP 消息 ───────────────────────────

func TestSendMediaFrame(t *testing.T) {
	srvConn, clientConn := newTestConn(t)
	defer srvConn.Close()
	defer clientConn.Close()

	srvConn.streamID = 1

	// 创建 playSession
	ps := newPlaySession(srvConn, zap.NewNop())
	ps.start()
	defer ps.stop()

	// 设置视频配置（SPS/PPS）
	sps := []byte{0x67, 0x42, 0x00, 0x0a, 0xf8, 0x41, 0xa2}
	pps := []byte{0x68, 0xce, 0x38, 0x80}
	ps.setVideoConfig(sps, pps)

	// 设置音频配置（AudioSpecificConfig）
	asc := []byte{0x12, 0x10} // AAC-LC, 44100Hz, stereo
	ps.setAudioConfig(asc, 44100, 2)

	// 构造一个 H.264 IDR 帧（Annex-B 格式）
	idrNALU := []byte{0x65, 0x88, 0x84, 0x00, 0x33, 0xff, 0xfe, 0xf7}
	annexBFrame := append([]byte{0x00, 0x00, 0x00, 0x01}, idrNALU...)

	videoFrame := common.MediaFrame{
		Type:       common.FrameVideo,
		Codec:      common.CodecH264,
		Payload:    annexBFrame,
		Timestamp:  90000, // 90kHz * 1s = 1000ms
		SampleRate: 90000,
		Marker:     true,
	}

	// 发送视频帧
	if err := ps.SendMediaFrame("video", videoFrame); err != nil {
		t.Fatalf("SendMediaFrame video: %v", err)
	}

	// 构造一个 AAC 音频帧
	aacPayload := []byte{0x21, 0x1a, 0x6d, 0x20, 0x00, 0x00, 0x00, 0x00}
	audioFrame := common.MediaFrame{
		Type:       common.FrameAudio,
		Codec:      common.CodecAAC,
		Payload:    aacPayload,
		Timestamp:  44100, // 44100Hz * 1s = 1000ms
		SampleRate: 44100,
		Channels:   2,
		Marker:     true,
	}

	// 发送音频帧
	if err := ps.SendMediaFrame("audio", audioFrame); err != nil {
		t.Fatalf("SendMediaFrame audio: %v", err)
	}

	// 从客户端并发读取 RTMP 消息（net.Pipe 是同步的，需要并发读取）
	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := NewChunkReader(clientConn)
	reader.SetChunkSize(4096)

	type readResult struct {
		msg *Message
		err error
	}
	readCh := make(chan readResult, 4)

	go func() {
		for i := 0; i < 4; i++ {
			msg, err := reader.ReadMessage()
			readCh <- readResult{msg: msg, err: err}
			if err != nil {
				return
			}
		}
	}()

	var videoMsgs []*Message
	var audioMsgs []*Message

	// 读取消息（AVC seq header + video NALU + AAC seq header + audio raw = 4 条）
	for i := 0; i < 4; i++ {
		select {
		case r := <-readCh:
			if r.err != nil {
				// 达到 EOF 或超时，停止读取
				goto done
			}
			switch r.msg.Type {
			case MsgVideo:
				videoMsgs = append(videoMsgs, r.msg)
			case MsgAudio:
				audioMsgs = append(audioMsgs, r.msg)
			}
		case <-time.After(5 * time.Second):
			goto done
		}
	}
done:

	// 验证视频消息
	if len(videoMsgs) < 2 {
		t.Fatalf("expected at least 2 video messages (seq header + NALU), got %d", len(videoMsgs))
	}

	// 第一条视频消息应该是 AVC sequence header
	seqMsg := videoMsgs[0]
	if len(seqMsg.Payload) < 5 {
		t.Fatalf("video seq header payload too short: %d", len(seqMsg.Payload))
	}
	if seqMsg.Payload[0]>>4 != frameKey {
		t.Fatalf("seq header frame type = %d, want keyframe %d", seqMsg.Payload[0]>>4, frameKey)
	}
	if seqMsg.Payload[0]&0x0f != codecIDAVC {
		t.Fatalf("seq header codec id = %d, want %d", seqMsg.Payload[0]&0x0f, codecIDAVC)
	}
	if seqMsg.Payload[1] != avcSeqHeader {
		t.Fatalf("seq header AVC packet type = %d, want %d", seqMsg.Payload[1], avcSeqHeader)
	}
	// 验证 sequence header 包含 SPS 和 PPS
	seqData := seqMsg.Payload[5:]
	if len(seqData) < 11 {
		t.Fatalf("AVCDecoderConfigurationRecord too short: %d", len(seqData))
	}
	if seqData[0] != 1 {
		t.Fatalf("configurationVersion = %d, want 1", seqData[0])
	}
	// 检查 SPS
	numSPS := int(seqData[5] & 0x1f)
	if numSPS != 1 {
		t.Fatalf("numSPS = %d, want 1", numSPS)
	}
	spsLen := int(binary.BigEndian.Uint16(seqData[6:8]))
	if spsLen != len(sps) {
		t.Fatalf("SPS length = %d, want %d", spsLen, len(sps))
	}
	if !bytes.Equal(seqData[8:8+spsLen], sps) {
		t.Fatalf("SPS data mismatch")
	}

	// 第二条视频消息应该是 AVC NALU
	naluMsg := videoMsgs[1]
	if len(naluMsg.Payload) < 5 {
		t.Fatalf("video NALU payload too short: %d", len(naluMsg.Payload))
	}
	if naluMsg.Payload[1] != avcNALU {
		t.Fatalf("NALU AVC packet type = %d, want %d", naluMsg.Payload[1], avcNALU)
	}
	// 验证时间戳转换：90000 (90kHz) → 1000ms
	if naluMsg.Timestamp != 1000 {
		t.Fatalf("video timestamp = %d, want 1000", naluMsg.Timestamp)
	}
	// 验证 NALU 数据（4 字节长度前缀 + NALU）
	naluData := naluMsg.Payload[5:]
	if len(naluData) < 4 {
		t.Fatalf("NALU data too short")
	}
	nalLen := binary.BigEndian.Uint32(naluData[:4])
	if int(nalLen) != len(idrNALU) {
		t.Fatalf("NALU length = %d, want %d", nalLen, len(idrNALU))
	}
	if !bytes.Equal(naluData[4:4+nalLen], idrNALU) {
		t.Fatalf("NALU data mismatch")
	}

	// 验证音频消息
	if len(audioMsgs) < 2 {
		t.Fatalf("expected at least 2 audio messages (seq header + raw), got %d", len(audioMsgs))
	}

	// 第一条音频消息应该是 AAC sequence header
	aacSeqMsg := audioMsgs[0]
	if len(aacSeqMsg.Payload) < 2 {
		t.Fatalf("audio seq header payload too short")
	}
	if aacSeqMsg.Payload[0]>>4 != soundFormatAAC {
		t.Fatalf("audio seq header sound format = %d, want %d (AAC)",
			aacSeqMsg.Payload[0]>>4, soundFormatAAC)
	}
	if aacSeqMsg.Payload[1] != aacSeqHeader {
		t.Fatalf("audio seq header AAC packet type = %d, want %d",
			aacSeqMsg.Payload[1], aacSeqHeader)
	}
	if !bytes.Equal(aacSeqMsg.Payload[2:], asc) {
		t.Fatalf("AAC ASC data mismatch")
	}

	// 第二条音频消息应该是 AAC raw
	aacRawMsg := audioMsgs[1]
	if len(aacRawMsg.Payload) < 2 {
		t.Fatalf("audio raw payload too short")
	}
	if aacRawMsg.Payload[1] != aacRaw {
		t.Fatalf("audio raw AAC packet type = %d, want %d", aacRawMsg.Payload[1], aacRaw)
	}
	// 验证时间戳转换：44100 (44100Hz) → 1000ms
	if aacRawMsg.Timestamp != 1000 {
		t.Fatalf("audio timestamp = %d, want 1000", aacRawMsg.Timestamp)
	}
	if !bytes.Equal(aacRawMsg.Payload[2:], aacPayload) {
		t.Fatalf("AAC raw data mismatch")
	}
}

// ─── TestFrameToAVC: 验证 Annex-B 到 AVCC 的转换 ──────────────────────────────

func TestFrameToAVC(t *testing.T) {
	// 测试 Annex-B 格式（带 4 字节 start code）
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, // start code
		0x67, 0x42, 0x00, 0x0a, // SPS NALU
		0x00, 0x00, 0x00, 0x01, // start code
		0x65, 0x88, 0x84, 0x00, // IDR NALU
	}

	avcData, isKeyframe, err := frameToAVC(annexB)
	if err != nil {
		t.Fatalf("frameToAVC: %v", err)
	}

	// 应该有 2 个 NALU，每个 4 字节 + 4 字节长度前缀 = 16
	if len(avcData) != 16 {
		t.Fatalf("AVC data length = %d, want 16", len(avcData))
	}

	// IDR NALU 存在，应该是关键帧
	if !isKeyframe {
		t.Fatal("expected keyframe (IDR NALU present)")
	}

	// 验证第一个 NALU 长度
	nal1Len := binary.BigEndian.Uint32(avcData[:4])
	if nal1Len != 4 {
		t.Fatalf("first NALU length = %d, want 4", nal1Len)
	}

	// 验证第二个 NALU 长度
	nal2Len := binary.BigEndian.Uint32(avcData[8:12])
	if nal2Len != 4 {
		t.Fatalf("second NALU length = %d, want 4", nal2Len)
	}

	// 测试单个 NALU（非 Annex-B）
	singleNALU := []byte{0x61, 0x64, 0x00, 0x01}
	avcData2, isKeyframe2, err := frameToAVC(singleNALU)
	if err != nil {
		t.Fatalf("frameToAVC single: %v", err)
	}
	if isKeyframe2 {
		t.Fatal("single non-IDR NALU should not be keyframe")
	}
	nalLen := binary.BigEndian.Uint32(avcData2[:4])
	if nalLen != 4 {
		t.Fatalf("single NALU length = %d, want 4", nalLen)
	}

	// 测试空 payload
	avcData3, _, err := frameToAVC(nil)
	if err != nil {
		t.Fatalf("frameToAVC empty: %v", err)
	}
	if avcData3 != nil {
		t.Fatalf("empty payload should return nil, got %d bytes", len(avcData3))
	}
}

// ─── TestPlaySessionTracks: 验证 Tracks 和 MediaStats 接口 ─────────────────────

func TestPlaySessionTracksAndStats(t *testing.T) {
	srvConn, clientConn := newTestConn(t)
	defer srvConn.Close()
	defer clientConn.Close()

	ps := newPlaySession(srvConn, zap.NewNop())
	ps.start()
	defer ps.stop()

	// 设置配置
	sps := []byte{0x67, 0x42, 0x00, 0x0a}
	pps := []byte{0x68, 0xce, 0x38, 0x80}
	ps.setVideoConfig(sps, pps)
	ps.setAudioConfig([]byte{0x12, 0x10}, 44100, 2)

	// 并发排空客户端读取（net.Pipe 是同步的）
	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := NewChunkReader(clientConn)
	reader.SetChunkSize(4096)
	go func() {
		for {
			_, err := reader.ReadMessage()
			if err != nil {
				return
			}
		}
	}()

	// 发送一帧视频（通过直接调用 processFrame）
	videoFrame := common.MediaFrame{
		Type:       common.FrameVideo,
		Codec:      common.CodecH264,
		Payload:    []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
		Timestamp:  90000,
		SampleRate: 90000,
	}
	_ = ps.processFrame(&mediaFrameItem{trackID: "video", frame: videoFrame})

	// 等待一点时间让 sendLoop 处理完
	time.Sleep(100 * time.Millisecond)

	// 验证统计
	stats := ps.MediaStats()
	if s, ok := stats["video"]; !ok {
		t.Fatal("missing video stats")
	} else if s.PacketsSent != 2 { // seq header + NALU
		t.Fatalf("video packets sent = %d, want 2", s.PacketsSent)
	}

	// 验证 Tracks（初始为空，因为 tracks 在 handlePlay 中设置）
	if len(ps.Tracks()) != 0 {
		t.Fatalf("expected 0 tracks, got %d", len(ps.Tracks()))
	}
}

// ─── TestSendMediaFrameBufferFull: 验证缓冲区满时丢帧 ─────────────────────────

func TestSendMediaFrameBufferFull(t *testing.T) {
	srvConn, _ := newTestConn(t)
	defer srvConn.Close()

	// 创建小缓冲的 playSession
	ps := newPlaySession(srvConn, zap.NewNop())
	ps.frameCh = make(chan *mediaFrameItem, 2) // 小缓冲
	// 不启动 sendLoop，让缓冲区填满

	// 填满缓冲区
	for i := 0; i < 2; i++ {
		if err := ps.SendMediaFrame("video", common.MediaFrame{
			Type:    common.FrameVideo,
			Codec:   common.CodecH264,
			Payload: []byte{0x65},
		}); err != nil {
			t.Fatalf("SendMediaFrame %d: %v", i, err)
		}
	}

	// 第三次应该返回错误（缓冲区满）
	err := ps.SendMediaFrame("video", common.MediaFrame{
		Type:    common.FrameVideo,
		Codec:   common.CodecH264,
		Payload: []byte{0x65},
	})
	if err == nil {
		t.Fatal("expected error when buffer full, got nil")
	}
}

// ─── TestConnMediaSessionInterface: 验证 Conn 实现 MediaSession 接口 ───────────

func TestConnMediaSessionInterface(t *testing.T) {
	srvConn, clientConn := newTestConn(t)
	defer srvConn.Close()
	defer clientConn.Close()

	// 没有 playSession 时，SendMediaFrame 应返回错误
	err := srvConn.SendMediaFrame("video", common.MediaFrame{
		Type:    common.FrameVideo,
		Codec:   common.CodecH264,
		Payload: []byte{0x65},
	})
	if err == nil {
		t.Fatal("expected error when not in play mode")
	}

	// 创建 playSession
	srvConn.playSess = newPlaySession(srvConn, zap.NewNop())
	srvConn.playSess.start()
	defer srvConn.playSess.stop()

	// 设置配置
	srvConn.SetVideoConfig([]byte{0x67, 0x42, 0x00, 0x0a}, []byte{0x68, 0xce, 0x38, 0x80})
	srvConn.SetAudioConfig([]byte{0x12, 0x10}, 44100, 2)

	// 并发排空客户端读取
	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := NewChunkReader(clientConn)
	reader.SetChunkSize(4096)
	go func() {
		for {
			_, err := reader.ReadMessage()
			if err != nil {
				return
			}
		}
	}()

	// 现在 SendMediaFrame 应该不返回错误
	err = srvConn.SendMediaFrame("video", common.MediaFrame{
		Type:       common.FrameVideo,
		Codec:      common.CodecH264,
		Payload:    []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
		Timestamp:  90000,
		SampleRate: 90000,
	})
	if err != nil {
		t.Fatalf("SendMediaFrame: %v", err)
	}

	// 等待 sendLoop 处理
	time.Sleep(100 * time.Millisecond)

	// 验证 Tracks 和 MediaStats
	_ = srvConn.Tracks()
	_ = srvConn.MediaStats()
}

// 确保 bytes 包被使用
var _ = bytes.NewBuffer
