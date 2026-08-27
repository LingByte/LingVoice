package rtmp

import (
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"go.uber.org/zap"
)

// ─── play/egress 模式：服务端向客户端推送媒体 ──────────────────────────────────

// playSession 管理 play 模式下的媒体发送。
// 实现 common.MediaSession 接口，上层通过 SendMediaFrame 推送媒体帧，
// playSession 将帧转为 RTMP audio/video 消息写入客户端连接。
type playSession struct {
	conn *Conn
	log  *zap.Logger

	// 媒体帧缓冲通道：上层通过 SendMediaFrame 投递帧，后台协程消费并写入连接
	frameCh chan *mediaFrameItem
	done    chan struct{}
	wg      sync.WaitGroup

	// 轨道信息
	tracks []common.TrackInfo

	// 编码参数（用于生成 sequence header）
	avcConfig *avcDecoderConfig // SPS/PPS
	aacConfig *aacDecoderConfig // sample rate / channels
	aacASC    []byte            // AudioSpecificConfig 原始字节

	// sequence header 发送状态
	videoSeqHeaderSent bool
	audioSeqHeaderSent bool

	// 统计
	stats   map[common.TrackID]common.TrackStats
	statsMu sync.Mutex
}

// mediaFrameItem 是投递到 frameCh 的媒体帧项
type mediaFrameItem struct {
	trackID common.TrackID
	frame   common.MediaFrame
}

// newPlaySession 创建 play 会话
func newPlaySession(conn *Conn, log *zap.Logger) *playSession {
	return &playSession{
		conn:    conn,
		log:     log,
		frameCh: make(chan *mediaFrameItem, 256),
		done:    make(chan struct{}),
		stats:   make(map[common.TrackID]common.TrackStats),
	}
}

// start 启动媒体发送协程
func (ps *playSession) start() {
	ps.wg.Add(1)
	go ps.sendLoop()
}

// stop 停止媒体发送协程
func (ps *playSession) stop() {
	select {
	case <-ps.done:
		// already closed
	default:
		close(ps.done)
	}
	ps.wg.Wait()
}

// sendLoop 从 frameCh 读取媒体帧并写入客户端连接
func (ps *playSession) sendLoop() {
	defer ps.wg.Done()
	for {
		select {
		case item := <-ps.frameCh:
			if err := ps.processFrame(item); err != nil {
				ps.log.Debug("rtmp play: write frame failed",
					zap.String("track", string(item.trackID)),
					zap.Error(err))
			}
		case <-ps.done:
			return
		}
	}
}

// ─── MediaSession 接口实现 ───────────────────────────────────────────────────

// SendMediaFrame 实现 common.MediaSession 接口。
// 将媒体帧投递到缓冲通道，由后台协程异步写入连接。
func (ps *playSession) SendMediaFrame(trackID common.TrackID, frame common.MediaFrame) error {
	select {
	case ps.frameCh <- &mediaFrameItem{trackID: trackID, frame: frame}:
		return nil
	case <-ps.done:
		return fmt.Errorf("rtmp play: session closed")
	default:
		return fmt.Errorf("rtmp play: frame buffer full, dropping frame")
	}
}

// Tracks 实现 common.MediaSession 接口
func (ps *playSession) Tracks() []common.TrackInfo {
	return ps.tracks
}

// MediaStats 实现 common.MediaSession 接口
func (ps *playSession) MediaStats() map[common.TrackID]common.TrackStats {
	ps.statsMu.Lock()
	defer ps.statsMu.Unlock()
	result := make(map[common.TrackID]common.TrackStats, len(ps.stats))
	for k, v := range ps.stats {
		result[k] = v
	}
	return result
}

// ─── 编码参数设置 ─────────────────────────────────────────────────────────────

// setVideoConfig 设置 H.264 SPS/PPS，用于生成 AVC sequence header
func (ps *playSession) setVideoConfig(sps, pps []byte) {
	ps.avcConfig = &avcDecoderConfig{
		sps: sps,
		pps: pps,
	}
	// 如果已经发送过旧的 sequence header，重新发送新的
	ps.videoSeqHeaderSent = false
}

// setAudioConfig 设置 AAC AudioSpecificConfig，用于生成 AAC sequence header
func (ps *playSession) setAudioConfig(asc []byte, sampleRate uint32, channels uint16) {
	ps.aacASC = asc
	ps.aacConfig = &aacDecoderConfig{
		sampleRate: sampleRate,
		channels:   channels,
	}
	ps.audioSeqHeaderSent = false
}

// ─── 媒体帧处理 ───────────────────────────────────────────────────────────────

// processFrame 将一个 MediaFrame 转为 RTMP 消息并写入连接
func (ps *playSession) processFrame(item *mediaFrameItem) error {
	trackID := item.trackID
	frame := item.frame

	switch frame.Type {
	case common.FrameVideo:
		return ps.sendVideoFrame(trackID, frame)
	case common.FrameAudio:
		return ps.sendAudioFrame(trackID, frame)
	default:
		return fmt.Errorf("rtmp play: unknown frame type %d", frame.Type)
	}
}

// sendVideoFrame 将 H.264 帧转为 RTMP video 消息
func (ps *playSession) sendVideoFrame(trackID common.TrackID, frame common.MediaFrame) error {
	if frame.Codec != common.CodecH264 {
		ps.log.Debug("rtmp play: unsupported video codec, skip",
			zap.String("codec", frame.Codec.String()))
		return nil
	}

	// 首次发送视频前，先发送 AVC sequence header（如果有 SPS/PPS）
	if !ps.videoSeqHeaderSent && ps.avcConfig != nil {
		if err := ps.sendAVCSequenceHeader(); err != nil {
			return fmt.Errorf("send avc seq header: %w", err)
		}
		ps.videoSeqHeaderSent = true
	}

	// 将 NALU payload 转为 RTMP AVC 格式（4 字节长度前缀）
	avcData, isKeyframe, err := frameToAVC(frame.Payload)
	if err != nil {
		return fmt.Errorf("frame to avc: %w", err)
	}
	if len(avcData) == 0 {
		return nil
	}

	// 构造 RTMP video message payload
	// byte 0: (frameType << 4) | codecID
	// byte 1: AVCPacketType (1=NALU)
	// byte 2-4: composition time (24-bit, 0)
	// byte 5+: AVC data
	frameType := byte(frameInter) // 默认 inter frame
	if isKeyframe {
		frameType = byte(frameKey)
	}

	payload := make([]byte, 5+len(avcData))
	payload[0] = (frameType << 4) | codecIDAVC
	payload[1] = avcNALU
	// composition time = 0
	payload[2] = 0
	payload[3] = 0
	payload[4] = 0
	copy(payload[5:], avcData)

	// RTP timestamp (90kHz) → RTMP timestamp (ms)
	ts := frame.Timestamp / 90

	if err := ps.conn.writeMediaMessage(MsgVideo, ps.conn.streamID, ts, payload); err != nil {
		return fmt.Errorf("write video message: %w", err)
	}

	ps.updateStats(trackID, len(payload))
	return nil
}

// sendAudioFrame 将 AAC 帧转为 RTMP audio 消息
func (ps *playSession) sendAudioFrame(trackID common.TrackID, frame common.MediaFrame) error {
	if frame.Codec != common.CodecAAC {
		ps.log.Debug("rtmp play: unsupported audio codec, skip",
			zap.String("codec", frame.Codec.String()))
		return nil
	}

	// 首次发送音频前，先发送 AAC sequence header（如果有 ASC）
	if !ps.audioSeqHeaderSent && ps.aacASC != nil {
		if err := ps.sendAACSequenceHeader(); err != nil {
			return fmt.Errorf("send aac seq header: %w", err)
		}
		ps.audioSeqHeaderSent = true
	}

	if len(frame.Payload) == 0 {
		return nil
	}

	// 构造 RTMP audio message payload
	// byte 0: (soundFormat << 4) | (sampleRateIndex << 2) | (sampleSizeIndex << 1) | channelType
	//   soundFormat = 10 (AAC)
	//   sampleRateIndex = 3 (44100, ignored for AAC)
	//   sampleSizeIndex = 1 (16-bit)
	//   channelType = 1 (stereo) or 0 (mono)
	channelType := byte(1) // stereo
	if ps.aacConfig != nil && ps.aacConfig.channels == 1 {
		channelType = 0 // mono
	}
	firstByte := byte(soundFormatAAC<<4) | 0x0E | channelType // 0xAF (stereo) or 0xAE (mono)

	// byte 1: AACPacketType (1=raw)
	payload := make([]byte, 2+len(frame.Payload))
	payload[0] = firstByte
	payload[1] = aacRaw
	copy(payload[2:], frame.Payload)

	// RTP timestamp (sample rate Hz) → RTMP timestamp (ms)
	sampleRate := uint32(44100)
	if ps.aacConfig != nil {
		sampleRate = ps.aacConfig.sampleRate
	}
	if frame.SampleRate > 0 {
		sampleRate = frame.SampleRate
	}
	ts := uint32(uint64(frame.Timestamp) * 1000 / uint64(sampleRate))

	if err := ps.conn.writeMediaMessage(MsgAudio, ps.conn.streamID, ts, payload); err != nil {
		return fmt.Errorf("write audio message: %w", err)
	}

	ps.updateStats(trackID, len(payload))
	return nil
}

// sendAVCSequenceHeader 发送 AVC sequence header（AVCDecoderConfigurationRecord）
func (ps *playSession) sendAVCSequenceHeader() error {
	if ps.avcConfig == nil || len(ps.avcConfig.sps) == 0 || len(ps.avcConfig.pps) == 0 {
		return fmt.Errorf("avc config not set (sps/pps missing)")
	}

	sps := ps.avcConfig.sps
	pps := ps.avcConfig.pps

	// AVCDecoderConfigurationRecord:
	//   configurationVersion(1) = 1
	//   AVCProfileIndication(1) = sps[1]
	//   profile_compatibility(1) = sps[2]
	//   AVCLevelIndication(1) = sps[3]
	//   lengthSizeMinusOne(1) = 0xFF (低2位=3, 表示4字节长度)
	//   numOfSPS(1) = 0xE1 (低5位=1)
	//   spsLength(2) + sps
	//   numOfPPS(1) = 1
	//   ppsLength(2) + pps
	avcCfg := make([]byte, 0, 11+len(sps)+len(pps))
	avcCfg = append(avcCfg, 1)            // configurationVersion
	avcCfg = append(avcCfg, sps[1])       // AVCProfileIndication
	avcCfg = append(avcCfg, sps[2])       // profile_compatibility
	avcCfg = append(avcCfg, sps[3])       // AVCLevelIndication
	avcCfg = append(avcCfg, 0xFF)         // lengthSizeMinusOne = 3 (4 bytes)
	avcCfg = append(avcCfg, 0xE1)         // numOfSPS = 1
	spsLen := make([]byte, 2)
	binary.BigEndian.PutUint16(spsLen, uint16(len(sps)))
	avcCfg = append(avcCfg, spsLen...)
	avcCfg = append(avcCfg, sps...)
	avcCfg = append(avcCfg, 1) // numOfPPS = 1
	ppsLen := make([]byte, 2)
	binary.BigEndian.PutUint16(ppsLen, uint16(len(pps)))
	avcCfg = append(avcCfg, ppsLen...)
	avcCfg = append(avcCfg, pps...)

	// RTMP video message: keyframe + AVC sequence header
	payload := make([]byte, 5+len(avcCfg))
	payload[0] = (byte(frameKey) << 4) | codecIDAVC
	payload[1] = avcSeqHeader
	payload[2] = 0 // composition time
	payload[3] = 0
	payload[4] = 0
	copy(payload[5:], avcCfg)

	if err := ps.conn.writeMediaMessage(MsgVideo, ps.conn.streamID, 0, payload); err != nil {
		return fmt.Errorf("write avc seq header: %w", err)
	}
	ps.updateStats("video", len(payload))
	return nil
}

// sendAACSequenceHeader 发送 AAC sequence header（AudioSpecificConfig）
func (ps *playSession) sendAACSequenceHeader() error {
	if len(ps.aacASC) == 0 {
		return fmt.Errorf("aac audio specific config not set")
	}

	// RTMP audio message: AAC sequence header
	// byte 0: soundFormat=AAC (0xAF for stereo, 0xAE for mono)
	channelType := byte(1)
	if ps.aacConfig != nil && ps.aacConfig.channels == 1 {
		channelType = 0
	}
	firstByte := byte(soundFormatAAC<<4) | 0x0E | channelType

	payload := make([]byte, 2+len(ps.aacASC))
	payload[0] = firstByte
	payload[1] = aacSeqHeader
	copy(payload[2:], ps.aacASC)

	if err := ps.conn.writeMediaMessage(MsgAudio, ps.conn.streamID, 0, payload); err != nil {
		return fmt.Errorf("write aac seq header: %w", err)
	}
	ps.updateStats("audio", len(payload))
	return nil
}

// sendOnMetaData 发送 onMetaData 数据消息（包含流元数据）
func (ps *playSession) sendOnMetaData(streamID uint32) {
	w := newAMFWriter()
	w.WriteString("onMetaData")

	props := map[string]AMFValue{
		"duration":      {Type: AMFTypeNumber, Number: 0},
		"videocodecid":  {Type: AMFTypeNumber, Number: float64(codecIDAVC)},
		"audiocodecid":  {Type: AMFTypeNumber, Number: float64(soundFormatAAC)},
		"canSeekToEnd":  {Type: AMFTypeBoolean, Bool: false},
	}

	if ps.aacConfig != nil {
		props["audiosamplerate"] = AMFValue{Type: AMFTypeNumber, Number: float64(ps.aacConfig.sampleRate)}
		props["audiochannels"] = AMFValue{Type: AMFTypeNumber, Number: float64(ps.aacConfig.channels)}
	}
	props["audiosamplesize"] = AMFValue{Type: AMFTypeNumber, Number: 16}

	w.WriteEcmaArray(props)

	if err := ps.conn.writeDataMessage(streamID, w.Bytes()); err != nil {
		ps.log.Debug("rtmp play: send onMetaData failed", zap.Error(err))
	}
}

// ─── 辅助函数 ─────────────────────────────────────────────────────────────────

// updateStats 更新轨道统计
func (ps *playSession) updateStats(trackID common.TrackID, bytesSent int) {
	ps.statsMu.Lock()
	defer ps.statsMu.Unlock()
	s := ps.stats[trackID]
	s.BytesSent += uint64(bytesSent)
	s.PacketsSent++
	ps.stats[trackID] = s
}

// frameToAVC 将媒体帧 payload 转为 RTMP AVC 格式（4 字节长度前缀）。
// 支持 Annex-B 格式（00 00 00 01 start code）和单个 NALU。
// 返回 AVC 数据和是否为关键帧。
func frameToAVC(payload []byte) ([]byte, bool, error) {
	if len(payload) == 0 {
		return nil, false, nil
	}

	// 检测是否为 Annex-B 格式（以 00 00 00 01 或 00 00 01 开头）
	nalus := parseAnnexB(payload)
	if len(nalus) == 0 {
		// 不是 Annex-B，当作单个 NALU
		nalus = [][]byte{payload}
	}

	// 判断是否为关键帧：NALU type 为 5 (IDR) 或 7 (SPS) 或 8 (PPS)
	isKeyframe := false
	for _, nal := range nalus {
		if len(nal) == 0 {
			continue
		}
		nalType := nal[0] & 0x1f
		if nalType == 5 { // IDR slice
			isKeyframe = true
			break
		}
	}

	// 打包为 4 字节长度前缀格式
	var buf []byte
	for _, nal := range nalus {
		if len(nal) == 0 {
			continue
		}
		lenBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBytes, uint32(len(nal)))
		buf = append(buf, lenBytes...)
		buf = append(buf, nal...)
	}

	return buf, isKeyframe, nil
}

// parseAnnexB 解析 Annex-B 格式的 H.264 bytestream，返回 NALU 列表。
// 如果不是 Annex-B 格式，返回 nil。
func parseAnnexB(data []byte) [][]byte {
	if len(data) < 3 {
		return nil
	}

	// 检查是否以 start code 开头
	if !isStartCode(data) {
		return nil
	}

	var nalus [][]byte
	pos := 0
	for pos < len(data) {
		// 找到当前 start code 的长度
		scLen := startCodeLen(data[pos:])
		if scLen == 0 {
			pos++
			continue
		}
		nalStart := pos + scLen

		// 找到下一个 start code
		nextPos := findNextStartCode(data, nalStart)
		if nextPos < 0 {
			nalus = append(nalus, data[nalStart:])
			break
		}
		nalus = append(nalus, data[nalStart:nextPos])
		pos = nextPos
	}

	return nalus
}

// isStartCode 检查是否以 Annex-B start code 开头
func isStartCode(data []byte) bool {
	if len(data) >= 4 && data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 1 {
		return true
	}
	if len(data) >= 3 && data[0] == 0 && data[1] == 0 && data[2] == 1 {
		return true
	}
	return false
}

// startCodeLen 返回 start code 的长度（3 或 4），如果不是 start code 返回 0
func startCodeLen(data []byte) int {
	if len(data) >= 4 && data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 1 {
		return 4
	}
	if len(data) >= 3 && data[0] == 0 && data[1] == 0 && data[2] == 1 {
		return 3
	}
	return 0
}

// findNextStartCode 从 offset 开始查找下一个 start code 的位置
func findNextStartCode(data []byte, offset int) int {
	for i := offset; i < len(data)-2; i++ {
		if data[i] == 0 && data[i+1] == 0 {
			if i+2 < len(data) && data[i+2] == 1 {
				return i
			}
			if i+3 < len(data) && data[i+2] == 0 && data[i+3] == 1 {
				return i
			}
		}
	}
	return -1
}

// 编译时检查 playSession 实现 common.MediaSession 接口
var _ common.MediaSession = (*playSession)(nil)

// 编译时检查 Conn 实现 common.MediaSession 接口
var _ common.MediaSession = (*Conn)(nil)
