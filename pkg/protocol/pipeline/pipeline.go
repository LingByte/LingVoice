// Package pipeline 提供统一的协议互转 fan-out 管道。
//
// 一个输入流可以同时输出到 HLS/DASH/RTMP play/WHEP/WebSocket/SRT 等多种输出协议。
// PipelineManager 管理所有流的 StreamPipeline，每个 StreamPipeline 负责将输入帧
// 分发到注册的 OutputSink。backpressure 通过带缓冲 channel + 非阻塞写入实现，
// channel 满时丢弃帧并计数；每个 sink 有独立后台 goroutine 消费 channel 并调用
// sink.WriteFrame()，失败重试 3 次后关闭该 sink。
package pipeline

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/LingByte/ling-base/common/logger"
	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"go.uber.org/zap"
)

// DefaultSinkBufferSize 每个 sink 输出 channel 的默认缓冲容量。
const DefaultSinkBufferSize = 100

// ErrSinkAlreadyExists sink 已存在
var ErrSinkAlreadyExists = errors.New("pipeline: sink already exists")

// ErrSinkNotFound sink 不存在
var ErrSinkNotFound = errors.New("pipeline: sink not found")

// ErrPipelineNotFound 管道不存在
var ErrPipelineNotFound = errors.New("pipeline: stream pipeline not found")

// ErrPipelineAlreadyExists 管道已存在
var ErrPipelineAlreadyExists = errors.New("pipeline: stream pipeline already exists")

// ─── 统计 ───────────────────────────────────────────────────────────────────

// PipelineStats 管道统计信息
type PipelineStats struct {
	StreamID      string
	SinkCount     int
	FramesIn      uint64
	FramesDropped uint64
	Sinks         []SinkStats
}

// SinkStats 单个 sink 的统计信息
type SinkStats struct {
	ID            string
	Protocol      string
	FramesWritten uint64
	FramesDropped uint64
	Errors        uint64
	Active        bool
}

// ─── 接口 ───────────────────────────────────────────────────────────────────

// OutputSink 输出 sink 接口 — 每种输出协议实现此接口。
type OutputSink interface {
	// ID 返回 sink 唯一标识
	ID() string
	// Protocol 返回输出协议类型: "hls" | "dash" | "rtmp" | "whep" | "ws" | "srt"
	Protocol() string
	// WriteFrame 写入一帧媒体数据
	WriteFrame(frame common.MediaFrame) error
	// Close 关闭 sink
	Close() error
}

// ─── sink 包装 ──────────────────────────────────────────────────────────────

// sinkWrapper 包装 OutputSink，提供 channel 缓冲、后台 goroutine 消费与统计。
type sinkWrapper struct {
	sink   OutputSink
	ch     chan common.MediaFrame
	done   chan struct{}
	closed atomic.Bool

	// 统计（原子操作）
	framesWritten uint64
	framesDropped uint64
	errors        uint64

	log *zap.Logger
}

func newSinkWrapper(sink OutputSink, bufSize int, log *zap.Logger) *sinkWrapper {
	if bufSize <= 0 {
		bufSize = DefaultSinkBufferSize
	}
	w := &sinkWrapper{
		sink: sink,
		ch:   make(chan common.MediaFrame, bufSize),
		done: make(chan struct{}),
		log:  log.With(zap.String("sink", sink.ID()), zap.String("protocol", sink.Protocol())),
	}
	go w.loop()
	return w
}

// loop 后台 goroutine：从 channel 读取帧并调用 sink.WriteFrame()，失败重试 3 次后关闭 sink。
func (w *sinkWrapper) loop() {
	for {
		select {
		case frame, ok := <-w.ch:
			if !ok {
				// channel 关闭，退出
				return
			}
			w.processFrame(frame)
		case <-w.done:
			return
		}
	}
}

// processFrame 处理一帧，失败重试 3 次后关闭 sink。
func (w *sinkWrapper) processFrame(frame common.MediaFrame) {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = w.sink.WriteFrame(frame); err == nil {
			atomic.AddUint64(&w.framesWritten, 1)
			return
		}
		w.log.Warn("pipeline: sink WriteFrame failed, retrying",
			zap.Int("attempt", attempt+1),
			zap.Error(err))
	}
	// 3 次重试均失败
	atomic.AddUint64(&w.errors, 1)
	w.log.Error("pipeline: sink WriteFrame failed after 3 retries, closing sink",
		zap.String("sinkID", w.sink.ID()),
		zap.Error(err))
	w.closeInternal()
}

// tryWrite 非阻塞写入 channel，满则丢弃并计数。
func (w *sinkWrapper) tryWrite(frame common.MediaFrame) bool {
	if w.closed.Load() {
		return false
	}
	select {
	case w.ch <- frame:
		return true
	default:
		atomic.AddUint64(&w.framesDropped, 1)
		return false
	}
}

// closeInternal 关闭 sink（幂等）。
func (w *sinkWrapper) closeInternal() {
	if !w.closed.CompareAndSwap(false, true) {
		return
	}
	close(w.done)
	// 排空 channel 避免 goroutine 阻塞
	go func() {
		for range w.ch {
		}
	}()
	close(w.ch)
	if err := w.sink.Close(); err != nil {
		w.log.Warn("pipeline: sink Close returned error", zap.Error(err))
	}
}

// stats 返回当前 sink 统计快照。
func (w *sinkWrapper) stats() SinkStats {
	return SinkStats{
		ID:            w.sink.ID(),
		Protocol:      w.sink.Protocol(),
		FramesWritten: atomic.LoadUint64(&w.framesWritten),
		FramesDropped: atomic.LoadUint64(&w.framesDropped),
		Errors:        atomic.LoadUint64(&w.errors),
		Active:        !w.closed.Load(),
	}
}

// ─── StreamPipeline ─────────────────────────────────────────────────────────

// StreamPipeline 单个输入流的 fan-out 管道。
type StreamPipeline struct {
	streamID string
	sinks    map[string]*sinkWrapper // sinkID → wrapper
	sinkMu   sync.RWMutex

	framesIn      uint64
	framesDropped uint64

	bufSize int
	log     *zap.Logger
	closed  atomic.Bool
}

// newStreamPipeline 创建一个流的管道（内部使用）。
func newStreamPipeline(streamID string, bufSize int, log *zap.Logger) *StreamPipeline {
	return &StreamPipeline{
		streamID: streamID,
		sinks:    make(map[string]*sinkWrapper),
		bufSize:  bufSize,
		log:      log.With(zap.String("stream", streamID)),
	}
}

// AddSink 注册输出 sink。若 sinkID 已存在返回 ErrSinkAlreadyExists。
func (p *StreamPipeline) AddSink(sink OutputSink) error {
	if sink == nil {
		return errors.New("pipeline: sink is nil")
	}
	if sink.ID() == "" {
		return errors.New("pipeline: sink ID is empty")
	}
	p.sinkMu.Lock()
	defer p.sinkMu.Unlock()
	if p.closed.Load() {
		return errors.New("pipeline: stream pipeline is closed")
	}
	if _, exists := p.sinks[sink.ID()]; exists {
		return fmt.Errorf("%w: %s", ErrSinkAlreadyExists, sink.ID())
	}
	p.sinks[sink.ID()] = newSinkWrapper(sink, p.bufSize, p.log)
	p.log.Info("pipeline: sink added", zap.String("sinkID", sink.ID()), zap.String("protocol", sink.Protocol()))
	return nil
}

// RemoveSink 注销输出 sink。若 sinkID 不存在返回 ErrSinkNotFound。
func (p *StreamPipeline) RemoveSink(sinkID string) error {
	p.sinkMu.Lock()
	defer p.sinkMu.Unlock()
	w, exists := p.sinks[sinkID]
	if !exists {
		return fmt.Errorf("%w: %s", ErrSinkNotFound, sinkID)
	}
	delete(p.sinks, sinkID)
	w.closeInternal()
	p.log.Info("pipeline: sink removed", zap.String("sinkID", sinkID))
	return nil
}

// WriteFrame 将帧分发到所有 sink（非阻塞，带 backpressure）。
func (p *StreamPipeline) WriteFrame(frame common.MediaFrame) {
	if p.closed.Load() {
		return
	}
	atomic.AddUint64(&p.framesIn, 1)
	p.sinkMu.RLock()
	wrappers := make([]*sinkWrapper, 0, len(p.sinks))
	for _, w := range p.sinks {
		wrappers = append(wrappers, w)
	}
	p.sinkMu.RUnlock()

	dropped := uint64(0)
	for _, w := range wrappers {
		if !w.tryWrite(frame) {
			dropped++
		}
	}
	if dropped > 0 {
		atomic.AddUint64(&p.framesDropped, dropped)
	}
}

// Sinks 列出所有 sink（返回当前快照）。
func (p *StreamPipeline) Sinks() []OutputSink {
	p.sinkMu.RLock()
	defer p.sinkMu.RUnlock()
	out := make([]OutputSink, 0, len(p.sinks))
	for _, w := range p.sinks {
		out = append(out, w.sink)
	}
	return out
}

// Stats 返回管道统计。
func (p *StreamPipeline) Stats() PipelineStats {
	p.sinkMu.RLock()
	defer p.sinkMu.RUnlock()
	sinkStats := make([]SinkStats, 0, len(p.sinks))
	for _, w := range p.sinks {
		sinkStats = append(sinkStats, w.stats())
	}
	return PipelineStats{
		StreamID:      p.streamID,
		SinkCount:     len(p.sinks),
		FramesIn:      atomic.LoadUint64(&p.framesIn),
		FramesDropped: atomic.LoadUint64(&p.framesDropped),
		Sinks:         sinkStats,
	}
}

// Close 关闭所有 sink。
func (p *StreamPipeline) Close() {
	if !p.closed.CompareAndSwap(false, true) {
		return
	}
	p.sinkMu.Lock()
	for _, w := range p.sinks {
		w.closeInternal()
	}
	p.sinks = make(map[string]*sinkWrapper)
	p.sinkMu.Unlock()
	p.log.Info("pipeline: stream pipeline closed")
}

// ─── PipelineManager ────────────────────────────────────────────────────────

// PipelineManager 管理所有流的管道。
type PipelineManager struct {
	pipelines map[string]*StreamPipeline // streamID → pipeline
	mu        sync.RWMutex
	log       *zap.Logger
	bufSize   int
}

// NewPipelineManager 创建管道管理器。log 为 nil 时 fallback 到 ling-base logger，
// 仍为 nil 则使用 zap.NewNop()。bufSize 为每个 sink channel 缓冲容量，<=0 使用默认值。
func NewPipelineManager(log *zap.Logger, bufSize int) *PipelineManager {
	if log == nil {
		if logger.Lg != nil {
			log = logger.Lg
		} else {
			log = zap.NewNop()
		}
	}
	if bufSize <= 0 {
		bufSize = DefaultSinkBufferSize
	}
	return &PipelineManager{
		pipelines: make(map[string]*StreamPipeline),
		log:       log.With(zap.String("component", "pipeline-manager")),
		bufSize:   bufSize,
	}
}

// CreatePipeline 创建管道。若 streamID 已存在返回 ErrPipelineAlreadyExists。
func (m *PipelineManager) CreatePipeline(streamID string) *StreamPipeline {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.pipelines[streamID]; exists {
		m.log.Warn("pipeline: create failed, already exists", zap.String("stream", streamID))
		return m.pipelines[streamID]
	}
	p := newStreamPipeline(streamID, m.bufSize, m.log)
	m.pipelines[streamID] = p
	m.log.Info("pipeline: created", zap.String("stream", streamID))
	return p
}

// GetPipeline 获取管道。
func (m *PipelineManager) GetPipeline(streamID string) (*StreamPipeline, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.pipelines[streamID]
	return p, ok
}

// DestroyPipeline 销毁管道（关闭所有 sink 并移除）。
func (m *PipelineManager) DestroyPipeline(streamID string) {
	m.mu.Lock()
	p, ok := m.pipelines[streamID]
	if !ok {
		m.mu.Unlock()
		return
	}
	delete(m.pipelines, streamID)
	m.mu.Unlock()
	p.Close()
	m.log.Info("pipeline: destroyed", zap.String("stream", streamID))
}

// WriteFrame 向指定流的管道写入帧。若管道不存在返回 ErrPipelineNotFound。
func (m *PipelineManager) WriteFrame(streamID string, frame common.MediaFrame) error {
	p, ok := m.GetPipeline(streamID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrPipelineNotFound, streamID)
	}
	p.WriteFrame(frame)
	return nil
}

// AddSink 向指定流添加输出。
func (m *PipelineManager) AddSink(streamID string, sink OutputSink) error {
	p, ok := m.GetPipeline(streamID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrPipelineNotFound, streamID)
	}
	return p.AddSink(sink)
}

// RemoveSink 从指定流移除输出。
func (m *PipelineManager) RemoveSink(streamID string, sinkID string) error {
	p, ok := m.GetPipeline(streamID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrPipelineNotFound, streamID)
	}
	return p.RemoveSink(sinkID)
}

// ListPipelines 列出所有流 ID。
func (m *PipelineManager) ListPipelines() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.pipelines))
	for id := range m.pipelines {
		out = append(out, id)
	}
	return out
}

// PipelineStats 获取指定流的统计。
func (m *PipelineManager) PipelineStats(streamID string) (PipelineStats, bool) {
	p, ok := m.GetPipeline(streamID)
	if !ok {
		return PipelineStats{}, false
	}
	return p.Stats(), true
}
