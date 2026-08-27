// Package hls implements HTTP Live Streaming (HLS) and Low-Latency HLS (LL-HLS)
// segmenter for the Go protocol layer.
//
// 工作流程:
//   1. 上层通过 PushFrame() 推送音视频帧
//   2. Segmenter 按 GOP 或固定时长切割 segment
//   3. 生成 m3u8 playlist + .ts/.m4s segment 文件
//   4. HTTP handler 提供 playlist 和 segment 下载
//
// LL-HLS 特性:
//   - CMAF/fMP4 分片 (LL-HLS 要求 fMP4)
//   - Partial segments (PART-HOLD-BACK)
//   - Preload hint (EXT-X-PRELOAD-HINT)
//   - Delta updates (EXT-X-SKIP)
//   - Blocking playlist reload (_HLS_msn, _HLS_part)
package hls

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

// Mode HLS 模式
type Mode int

const (
	ModeClassic Mode = iota // 经典 HLS (.ts segments)
	ModeLLHLS               // 低延迟 HLS (.m4s partial segments)
)

// Config HLS segmenter 配置
type Config struct {
	Addr           string        // HTTP 监听地址
	Path           string        // HLS 路径前缀, 如 "/hls"
	SegmentDuration time.Duration // segment 时长, 默认 6s (classic) / 1s (LL-HLS)
	PartDuration   time.Duration // partial segment 时长 (LL-HLS), 默认 200ms
	MaxSegments    int           // playlist 中保留的最大 segment 数
	Mode           Mode          // classic 或 LL-HLS
}

// DefaultConfig 默认配置 (LL-HLS)
func DefaultConfig() Config {
	return Config{
		Addr:            ":8085",
		Path:            "/hls",
		SegmentDuration: 1 * time.Second,
		PartDuration:    200 * time.Millisecond,
		MaxSegments:     6,
		Mode:            ModeLLHLS,
	}
}

// Segment 一个 HLS segment
type Segment struct {
	Index      int
	Sequence   uint64
	StartTime  time.Time
	Duration   time.Duration
	Data       []byte // TS 或 fMP4 数据
	IsPartial  bool   // LL-HLS partial segment
	PartIndex  int    // partial segment 索引
	URI        string // segment URI
}

// Stream 一个 HLS 流 (对应一个 session/stream)
type Stream struct {
	mu         sync.RWMutex
	id         string
	config     Config
	segments   []*Segment
	parts      []*Segment // LL-HLS partial segments for current segment
	currentSeg *Segment
	mediaSeq   uint64
	videoCodec common.CodecType
	audioCodec common.CodecType
	hasVideo   bool
	hasAudio   bool
	// GOP buffer for segmenting
	gopBuffer  []common.MediaFrame
	segStart   time.Time
	lastCleanup time.Time
	// LL-HLS blocking playlist reload
	blockChan  chan struct{}
}

// Segmenter HLS segmenter 管理多个流
type Segmenter struct {
	config   Config
	streams  sync.Map // map[string]*Stream
	log      *zap.Logger
}

// NewSegmenter 创建 HLS segmenter
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
		log:    log.With(zap.String("component", "hls-segmenter")),
	}
}

// CreateStream 创建一个 HLS 流
func (s *Segmenter) CreateStream(streamID string, videoCodec, audioCodec common.CodecType) *Stream {
	st := &Stream{
		id:         streamID,
		config:     s.config,
		segments:   make([]*Segment, 0, s.config.MaxSegments+2),
		parts:      make([]*Segment, 0, 10),
		videoCodec: videoCodec,
		audioCodec: audioCodec,
		hasVideo:   videoCodec != 0,
		hasAudio:   audioCodec != 0,
		segStart:   time.Now(),
		blockChan:  make(chan struct{}, 1),
	}
	s.streams.Store(streamID, st)
	s.log.Info("hls stream created", zap.String("stream", streamID))
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

// PushFrame 推送媒体帧到指定流
func (s *Segmenter) PushFrame(streamID string, frame common.MediaFrame) error {
	st, ok := s.GetStream(streamID)
	if !ok {
		return fmt.Errorf("hls stream not found: %s", streamID)
	}
	return st.pushFrame(frame)
}

func (st *Stream) pushFrame(frame common.MediaFrame) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	// Buffer frame for GOP-based segmentation
	st.gopBuffer = append(st.gopBuffer, frame)

	// Check if we should cut a new segment
	now := time.Now()
	elapsed := now.Sub(st.segStart)

	// For video keyframes, check if segment duration is reached
	if frame.Type == common.FrameVideo && frame.Marker && elapsed >= st.config.SegmentDuration {
		st.cutSegment()
		st.segStart = now
	}

	// LL-HLS: cut partial segments more frequently
	if st.config.Mode == ModeLLHLS && elapsed >= st.config.PartDuration {
		st.cutPartialSegment()
		// Notify blocking playlist reload
		select {
		case st.blockChan <- struct{}{}:
		default:
		}
	}

	return nil
}

func (st *Stream) cutSegment() {
	if len(st.gopBuffer) == 0 {
		return
	}

	// Build segment data from buffered frames
	var data []byte
	for _, f := range st.gopBuffer {
		// In a real implementation, this would mux into TS or fMP4
		// For now, concatenate payload as a placeholder
		data = append(data, f.Payload...)
	}

	seg := &Segment{
		Index:     len(st.segments),
		Sequence:  st.mediaSeq,
		StartTime: st.segStart,
		Duration:  time.Since(st.segStart),
		Data:      data,
		URI:       fmt.Sprintf("%s_%d.ts", st.id, st.mediaSeq),
	}
	if st.config.Mode == ModeLLHLS {
		seg.URI = fmt.Sprintf("%s_%d.m4s", st.id, st.mediaSeq)
	}

	st.segments = append(st.segments, seg)
	st.mediaSeq++
	st.currentSeg = seg
	st.parts = st.parts[:0] // reset partials for new segment

	// Trim old segments
	if len(st.segments) > st.config.MaxSegments {
		st.segments = st.segments[len(st.segments)-st.config.MaxSegments:]
	}
}

func (st *Stream) cutPartialSegment() {
	if len(st.gopBuffer) == 0 {
		return
	}

	// Take recent frames since last partial cut
	var data []byte
	for _, f := range st.gopBuffer {
		data = append(data, f.Payload...)
	}

	part := &Segment{
		Index:     len(st.segments),
		Sequence:  st.mediaSeq,
		StartTime: st.segStart,
		Duration:  st.config.PartDuration,
		Data:      data,
		IsPartial: true,
		PartIndex: len(st.parts),
		URI:       fmt.Sprintf("%s_%d_part%d.m4s", st.id, st.mediaSeq, len(st.parts)),
	}
	st.parts = append(st.parts, part)
}

// GeneratePlaylist 生成 m3u8 playlist
func (st *Stream) GeneratePlaylist() string {
	st.mu.RLock()
	defer st.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")
	sb.WriteString("#EXT-X-VERSION:6\n")

	targetDur := int(st.config.SegmentDuration.Seconds())
	if targetDur < 1 {
		targetDur = 1
	}
	sb.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", targetDur))

	if st.config.Mode == ModeLLHLS {
		partDur := int(st.config.PartDuration.Seconds() * 1000) / 1000
		if partDur < 1 {
			partDur = 1
		}
		sb.WriteString(fmt.Sprintf("#EXT-X-PART-INF:PART-TARGET=%.3f\n", st.config.PartDuration.Seconds()))
		sb.WriteString(fmt.Sprintf("#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES,PART-HOLD-BACK=%.3f,HOLD-BACK=%.3f\n",
			float64(partDur)*3, float64(targetDur)*3))
	}

	if len(st.segments) > 0 {
		sb.WriteString(fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", st.segments[0].Sequence))
	}

	for _, seg := range st.segments {
		sb.WriteString(fmt.Sprintf("#EXTINF:%.3f,\n", seg.Duration.Seconds()))
		sb.WriteString(seg.URI + "\n")

		// LL-HLS: list partial segments
		if st.config.Mode == ModeLLHLS {
			for _, part := range st.parts {
				if part.Sequence == seg.Sequence {
					sb.WriteString(fmt.Sprintf("#EXT-X-PART:DURATION=%.3f,URI=\"%s\"\n",
						part.Duration.Seconds(), part.URI))
				}
			}
		}
	}

	// LL-HLS: preload hint for next partial
	if st.config.Mode == ModeLLHLS && st.currentSeg != nil {
		nextPartURI := fmt.Sprintf("%s_%d_part%d.m4s", st.id, st.mediaSeq, len(st.parts))
		sb.WriteString(fmt.Sprintf("#EXT-X-PRELOAD-HINT:TYPE=PART,URI=\"%s\"\n", nextPartURI))
	}

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
	// Check partials
	for _, part := range st.parts {
		if part.URI == uri {
			return part.Data, true
		}
	}
	return nil, false
}

// Handler 返回 HTTP Handler
func (s *Segmenter) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.config.Path+"/", s.handleHLS)
	return mux
}

// Start 启动 HLS HTTP 服务
func (s *Segmenter) Start() error {
	s.log.Info("hls server starting", zap.String("addr", s.config.Addr), zap.String("path", s.config.Path))
	return http.ListenAndServe(s.config.Addr, s.Handler())
}

func (s *Segmenter) handleHLS(w http.ResponseWriter, r *http.Request) {
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

	// LL-HLS blocking playlist reload
	if st.config.Mode == ModeLLHLS {
		msn := r.URL.Query().Get("_HLS_msn")
		part := r.URL.Query().Get("_HLS_part")
		if msn != "" || part != "" {
			// Block until new segment/partial is available
			select {
			case <-st.blockChan:
			case <-r.Context().Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}

	if len(parts) == 1 || parts[1] == "" || parts[1] == "playlist.m3u8" {
		// Serve playlist
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
		playlist := st.GeneratePlaylist()
		_, _ = w.Write([]byte(playlist))
		return
	}

	// Serve segment
	segURI := parts[1]
	data, ok := st.GetSegment(segURI)
	if !ok {
		http.Error(w, "segment not found", http.StatusNotFound)
		return
	}

	contentType := "video/mp2t"
	if strings.HasSuffix(segURI, ".m4s") {
		contentType = "video/iso.segment"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(data)
}
