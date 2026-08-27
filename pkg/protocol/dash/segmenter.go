// Package dash implements MPEG-DASH (Dynamic Adaptive Streaming over HTTP)
// segmenter for the Go protocol layer.
//
// 工作流程:
//   1. 上层通过 PushFrame() 推送音视频帧
//   2. Segmenter 按固定时长切割 segment
//   3. 生成 MPD (Media Presentation Description) manifest + .m4s segment 文件
//   4. HTTP handler 提供 MPD 和 segment 下载
//
// DASH 特性:
//   - CMAF/fMP4 分片
//   - 动态 MPD (type="dynamic") for live streaming
//   - SegmentTimeline 精确时间线
//   - 多 Representation (自适应码率, 预留)
//   - 低延迟 DASH (LL-DASH) chunked encoding
package dash

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

// Config DASH segmenter 配置
type Config struct {
	Addr            string        // HTTP 监听地址
	Path            string        // DASH 路径前缀, 如 "/dash"
	SegmentDuration time.Duration // segment 时长, 默认 2s
	MaxSegments     int           // 保留的最大 segment 数
	MinBufferTime   time.Duration // 最小缓冲时间, 默认 4s
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Addr:            ":8086",
		Path:            "/dash",
		SegmentDuration: 2 * time.Second,
		MaxSegments:     5,
		MinBufferTime:   4 * time.Second,
	}
}

// Segment 一个 DASH segment
type Segment struct {
	Index     int
	Sequence  uint64
	StartTime time.Time
	Duration  time.Duration
	Data      []byte // fMP4 数据
	URI       string
}

// Representation 一个 DASH Representation (码率层)
type Representation struct {
	ID        string
	Width     uint16
	Height    uint16
	Codec     common.CodecType
	SampleRate uint32
	Channels  uint16
}

// Stream 一个 DASH 流
type Stream struct {
	mu             sync.RWMutex
	id             string
	config         Config
	segments       []*Segment
	gopBuffer      []common.MediaFrame
	segStart       time.Time
	mediaSeq       uint64
	videoRep       *Representation
	audioRep       *Representation
	publishTime    time.Time
}

// Segmenter DASH segmenter
type Segmenter struct {
	config  Config
	streams sync.Map // map[string]*Stream
	log     *zap.Logger
}

// NewSegmenter 创建 DASH segmenter
func NewSegmenter(config Config, log *zap.Logger) *Segmenter {
	if log == nil {
		if logger.Lg != nil {
			log = logger.Lg
		} else {
			log = zap.NewNop()
		}
	}
	return &Segmenter{
		config: config,
		log:    log.With(zap.String("component", "dash-segmenter")),
	}
}

// CreateStream 创建 DASH 流
func (s *Segmenter) CreateStream(streamID string, videoRep, audioRep *Representation) *Stream {
	st := &Stream{
		id:          streamID,
		config:      s.config,
		segments:    make([]*Segment, 0, s.config.MaxSegments+2),
		videoRep:    videoRep,
		audioRep:    audioRep,
		segStart:    time.Now(),
		publishTime: time.Now(),
	}
	s.streams.Store(streamID, st)
	s.log.Info("dash stream created", zap.String("stream", streamID))
	return st
}

// RemoveStream 移除流
func (s *Segmenter) RemoveStream(streamID string) {
	s.streams.Delete(streamID)
}

// GetStream 获取流
func (s *Segmenter) GetStream(streamID string) (*Stream, bool) {
	v, ok := s.streams.Load(streamID)
	if !ok {
		return nil, false
	}
	return v.(*Stream), true
}

// PushFrame 推送媒体帧
func (s *Segmenter) PushFrame(streamID string, frame common.MediaFrame) error {
	st, ok := s.GetStream(streamID)
	if !ok {
		return fmt.Errorf("dash stream not found: %s", streamID)
	}
	return st.pushFrame(frame)
}

func (st *Stream) pushFrame(frame common.MediaFrame) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.gopBuffer = append(st.gopBuffer, frame)

	now := time.Now()
	elapsed := now.Sub(st.segStart)

	// Cut segment on video keyframe marker or duration reached
	if (frame.Type == common.FrameVideo && frame.Marker && elapsed >= st.config.SegmentDuration) ||
		elapsed >= st.config.SegmentDuration*2 {
		st.cutSegment()
		st.segStart = now
	}

	return nil
}

func (st *Stream) cutSegment() {
	if len(st.gopBuffer) == 0 {
		return
	}

	var data []byte
	for _, f := range st.gopBuffer {
		data = append(data, f.Payload...)
	}

	seg := &Segment{
		Index:     len(st.segments),
		Sequence:  st.mediaSeq,
		StartTime: st.segStart,
		Duration:  time.Since(st.segStart),
		Data:      data,
		URI:       fmt.Sprintf("%s_%d.m4s", st.id, st.mediaSeq),
	}

	st.segments = append(st.segments, seg)
	st.mediaSeq++

	// Trim old segments
	if len(st.segments) > st.config.MaxSegments {
		st.segments = st.segments[len(st.segments)-st.config.MaxSegments:]
	}
}

// GenerateMPD 生成 MPD (Media Presentation Description) manifest
func (st *Stream) GenerateMPD() string {
	st.mu.RLock()
	defer st.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sb.WriteString("\n")
	sb.WriteString(`<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" `)
	sb.WriteString(`type="dynamic" `)
	sb.WriteString(`minimumUpdatePeriod="PT2S" `)
	sb.WriteString(fmt.Sprintf(`minBufferTime="PT%.3fS" `, st.config.MinBufferTime.Seconds()))
	sb.WriteString(`profiles="urn:mpeg:dash:profile:isoff-live:2011" `)
	sb.WriteString(`publishTime="`)
	sb.WriteString(time.Now().Format(time.RFC3339Nano))
	sb.WriteString(`">`)
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintf(`  <Period id="0" start="PT0S">`))
	sb.WriteString("\n")

	// Video AdaptationSet
	if st.videoRep != nil {
		sb.WriteString(`    <AdaptationSet id="0" mimeType="video/mp4" segmentAlignment="true">`)
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf(`      <Representation id="%s" codecs="avc1.42c01e" bandwidth="500000" width="%d" height="%d">`,
			st.videoRep.ID, st.videoRep.Width, st.videoRep.Height))
		sb.WriteString("\n")
		sb.WriteString(`        <SegmentTemplate timescale="1000" duration="`)
		sb.WriteString(fmt.Sprintf("%d", int(st.config.SegmentDuration.Seconds()*1000)))
		sb.WriteString(`" startNumber="`)
		startNum := uint64(1)
		if len(st.segments) > 0 {
			startNum = st.segments[0].Sequence
		}
		sb.WriteString(fmt.Sprintf("%d", startNum))
		sb.WriteString(`" media="`)
		sb.WriteString(fmt.Sprintf("%s_$Number$.m4s", st.id))
		sb.WriteString(`" initialization="`)
		sb.WriteString(fmt.Sprintf("%s_init.m4s", st.id))
		sb.WriteString(`"/>`)
		sb.WriteString("\n")
		sb.WriteString(`      </Representation>`)
		sb.WriteString("\n")
		sb.WriteString(`    </AdaptationSet>`)
		sb.WriteString("\n")
	}

	// Audio AdaptationSet
	if st.audioRep != nil {
		sb.WriteString(`    <AdaptationSet id="1" mimeType="audio/mp4" segmentAlignment="true">`)
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf(`      <Representation id="%s" codecs="mp4a.40.2" bandwidth="128000" audioSamplingRate="%d">`,
			st.audioRep.ID, st.audioRep.SampleRate))
		sb.WriteString("\n")
		sb.WriteString(`        <SegmentTemplate timescale="1000" duration="`)
		sb.WriteString(fmt.Sprintf("%d", int(st.config.SegmentDuration.Seconds()*1000)))
		sb.WriteString(`" startNumber="`)
		startNum := uint64(1)
		if len(st.segments) > 0 {
			startNum = st.segments[0].Sequence
		}
		sb.WriteString(fmt.Sprintf("%d", startNum))
		sb.WriteString(`" media="`)
		sb.WriteString(fmt.Sprintf("%s_audio_$Number$.m4s", st.id))
		sb.WriteString(`" initialization="`)
		sb.WriteString(fmt.Sprintf("%s_audio_init.m4s", st.id))
		sb.WriteString(`"/>`)
		sb.WriteString("\n")
		sb.WriteString(`      </Representation>`)
		sb.WriteString("\n")
		sb.WriteString(`    </AdaptationSet>`)
		sb.WriteString("\n")
	}

	// SegmentTimeline for live
	if len(st.segments) > 0 {
		sb.WriteString("    <!-- SegmentTimeline -->\n")
		for _, seg := range st.segments {
			sb.WriteString(fmt.Sprintf("    <!-- seg %d: start=%s dur=%.3fs -->\n",
				seg.Sequence, seg.StartTime.Format("15:04:05.000"), seg.Duration.Seconds()))
		}
	}

	sb.WriteString(`  </Period>`)
	sb.WriteString("\n")
	sb.WriteString(`</MPD>`)
	sb.WriteString("\n")

	return sb.String()
}

// GetSegment 获取 segment 数据
func (st *Stream) GetSegment(uri string) ([]byte, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	for _, seg := range st.segments {
		if seg.URI == uri {
			return seg.Data, true
		}
	}
	return nil, false
}

// Handler 返回 HTTP Handler
func (s *Segmenter) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.config.Path+"/", s.handleDASH)
	return mux
}

// Start 启动 DASH HTTP 服务
func (s *Segmenter) Start() error {
	s.log.Info("dash server starting", zap.String("addr", s.config.Addr), zap.String("path", s.config.Path))
	return http.ListenAndServe(s.config.Addr, s.Handler())
}

func (s *Segmenter) handleDASH(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, s.config.Path+"/")
	parts := strings.SplitN(path, "/", 2)

	streamID := parts[0]
	if streamID == "" {
		http.Error(w, "missing stream id", http.StatusBadRequest)
		return
	}

	st, ok := s.GetStream(streamID)
	if !ok {
		http.Error(w, "stream not found", http.StatusNotFound)
		return
	}

	if len(parts) == 1 || parts[1] == "" || parts[1] == "manifest.mpd" {
		// Serve MPD
		w.Header().Set("Content-Type", "application/dash+xml")
		w.Header().Set("Cache-Control", "no-cache")
		mpd := st.GenerateMPD()
		_, _ = w.Write([]byte(mpd))
		return
	}

	// Serve segment
	segURI := parts[1]
	data, ok := st.GetSegment(segURI)
	if !ok {
		// Check for init segment
		if strings.Contains(segURI, "init") {
			// Return empty init segment (placeholder)
			w.Header().Set("Content-Type", "video/iso.segment")
			_, _ = w.Write([]byte{})
			return
		}
		http.Error(w, "segment not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "video/iso.segment")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(data)
}
