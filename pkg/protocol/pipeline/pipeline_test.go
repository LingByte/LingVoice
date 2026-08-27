package pipeline

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
)

// ─── 测试用 mock sink ──────────────────────────────────────────────────────

// mockSink 测试用 OutputSink 实现
type mockSink struct {
	mu           sync.Mutex
	id           string
	protocol     string
	frames       []common.MediaFrame
	writeErr     error // 非 nil 时 WriteFrame 返回此错误
	closeErr     error
	closeCount   int32
	writeCount   int32
	delay        time.Duration
	closed       bool
	failFirstN   int32 // 前 N 次失败，之后成功（模拟瞬时错误）
	failAttempts int32
}

func (m *mockSink) ID() string       { return m.id }
func (m *mockSink) Protocol() string { return m.protocol }

func (m *mockSink) WriteFrame(frame common.MediaFrame) error {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	atomic.AddInt32(&m.writeCount, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("sink closed")
	}
	// 模拟瞬时错误：前 failFirstN 次失败
	if m.failFirstN > 0 {
		m.failAttempts++
		if m.failAttempts <= m.failFirstN {
			return errors.New("transient error")
		}
	}
	if m.writeErr != nil {
		return m.writeErr
	}
	// 复制 payload 避免数据竞争
	payload := make([]byte, len(frame.Payload))
	copy(payload, frame.Payload)
	cp := frame
	cp.Payload = payload
	m.frames = append(m.frames, cp)
	return nil
}

func (m *mockSink) Close() error {
	atomic.AddInt32(&m.closeCount, 1)
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	return m.closeErr
}

func newMockSink(id, protocol string) *mockSink {
	return &mockSink{id: id, protocol: protocol}
}

func testFrame(seq uint16) common.MediaFrame {
	return common.MediaFrame{
		Type:      common.FrameVideo,
		Codec:     common.CodecH264,
		Payload:   []byte{0x00, 0x00, 0x00, 0x01, byte(seq)},
		Timestamp: uint32(seq) * 3000,
		Sequence:  seq,
		Marker:    true,
	}
}

// waitForCount 等待 writeCount 达到目标值，超时返回 false
func waitForCount(counter *int32, target int32, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(counter) >= target {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return atomic.LoadInt32(counter) >= target
}

// ─── PipelineManager 创建/销毁 ─────────────────────────────────────────────

func TestCreateAndDestroyPipeline(t *testing.T) {
	mgr := NewPipelineManager(nil, 0)

	// 创建
	p := mgr.CreatePipeline("stream-1")
	if p == nil {
		t.Fatal("CreatePipeline returned nil")
	}
	if p.streamID != "stream-1" {
		t.Errorf("streamID = %q, want %q", p.streamID, "stream-1")
	}

	// 再次创建相同 ID 应返回已存在的管道
	p2 := mgr.CreatePipeline("stream-1")
	if p2 != p {
		t.Error("CreatePipeline on existing stream should return same pipeline")
	}

	// GetPipeline
	got, ok := mgr.GetPipeline("stream-1")
	if !ok || got != p {
		t.Error("GetPipeline failed")
	}

	// ListPipelines
	list := mgr.ListPipelines()
	if len(list) != 1 || list[0] != "stream-1" {
		t.Errorf("ListPipelines = %v, want [stream-1]", list)
	}

	// DestroyPipeline
	mgr.DestroyPipeline("stream-1")
	if _, ok := mgr.GetPipeline("stream-1"); ok {
		t.Error("pipeline still exists after DestroyPipeline")
	}
	if list := mgr.ListPipelines(); len(list) != 0 {
		t.Errorf("ListPipelines after destroy = %v, want empty", list)
	}
}

func TestDestroyNonExistentPipeline(t *testing.T) {
	mgr := NewPipelineManager(nil, 0)
	// 不应 panic
	mgr.DestroyPipeline("no-such-stream")
}

// ─── AddSink / RemoveSink ───────────────────────────────────────────────────

func TestAddAndRemoveSink(t *testing.T) {
	mgr := NewPipelineManager(nil, 0)
	p := mgr.CreatePipeline("stream-1")

	sink1 := newMockSink("sink-hls", "hls")
	sink2 := newMockSink("sink-dash", "dash")

	if err := p.AddSink(sink1); err != nil {
		t.Fatalf("AddSink sink1: %v", err)
	}
	if err := p.AddSink(sink2); err != nil {
		t.Fatalf("AddSink sink2: %v", err)
	}

	sinks := p.Sinks()
	if len(sinks) != 2 {
		t.Fatalf("Sinks() len = %d, want 2", len(sinks))
	}

	// 重复添加
	if err := p.AddSink(sink1); !errors.Is(err, ErrSinkAlreadyExists) {
		t.Errorf("AddSink duplicate should return ErrSinkAlreadyExists, got %v", err)
	}

	// RemoveSink
	if err := p.RemoveSink("sink-hls"); err != nil {
		t.Fatalf("RemoveSink: %v", err)
	}
	sinks = p.Sinks()
	if len(sinks) != 1 {
		t.Errorf("Sinks() after remove len = %d, want 1", len(sinks))
	}
	if sinks[0].ID() != "sink-dash" {
		t.Errorf("remaining sink = %q, want sink-dash", sinks[0].ID())
	}

	// RemoveSink 不存在的
	if err := p.RemoveSink("no-such"); !errors.Is(err, ErrSinkNotFound) {
		t.Errorf("RemoveSink non-existent should return ErrSinkNotFound, got %v", err)
	}

	// 验证 Close 被调用
	if atomic.LoadInt32(&sink1.closeCount) != 1 {
		t.Errorf("sink1 closeCount = %d, want 1", sink1.closeCount)
	}
}

func TestAddSinkNil(t *testing.T) {
	mgr := NewPipelineManager(nil, 0)
	p := mgr.CreatePipeline("stream-1")
	if err := p.AddSink(nil); err == nil {
		t.Error("AddSink(nil) should return error")
	}
}

func TestAddSinkToNonExistentPipeline(t *testing.T) {
	mgr := NewPipelineManager(nil, 0)
	sink := newMockSink("s1", "hls")
	if err := mgr.AddSink("no-stream", sink); !errors.Is(err, ErrPipelineNotFound) {
		t.Errorf("AddSink to non-existent pipeline should return ErrPipelineNotFound, got %v", err)
	}
}

// ─── WriteFrame fan-out ─────────────────────────────────────────────────────

func TestWriteFrameFanOut(t *testing.T) {
	mgr := NewPipelineManager(nil, 0)
	p := mgr.CreatePipeline("stream-1")

	sink1 := newMockSink("sink-hls", "hls")
	sink2 := newMockSink("sink-dash", "dash")
	sink3 := newMockSink("sink-whep", "whep")

	_ = p.AddSink(sink1)
	_ = p.AddSink(sink2)
	_ = p.AddSink(sink3)

	// 写入 10 帧
	const n = 10
	for i := uint16(0); i < n; i++ {
		mgr.WriteFrame("stream-1", testFrame(i))
	}

	// 等待所有 sink 收到帧
	if !waitForCount(&sink1.writeCount, n, 2*time.Second) {
		t.Errorf("sink1 writeCount = %d, want %d", atomic.LoadInt32(&sink1.writeCount), n)
	}
	if !waitForCount(&sink2.writeCount, n, 2*time.Second) {
		t.Errorf("sink2 writeCount = %d, want %d", atomic.LoadInt32(&sink2.writeCount), n)
	}
	if !waitForCount(&sink3.writeCount, n, 2*time.Second) {
		t.Errorf("sink3 writeCount = %d, want %d", atomic.LoadInt32(&sink3.writeCount), n)
	}

	// 验证帧内容
	sink1.mu.Lock()
	if len(sink1.frames) != n {
		t.Errorf("sink1 frames len = %d, want %d", len(sink1.frames), n)
	}
	sink1.mu.Unlock()

	// 验证统计
	stats := p.Stats()
	if stats.FramesIn != n {
		t.Errorf("FramesIn = %d, want %d", stats.FramesIn, n)
	}
	if stats.FramesDropped != 0 {
		t.Errorf("FramesDropped = %d, want 0", stats.FramesDropped)
	}
	if stats.SinkCount != 3 {
		t.Errorf("SinkCount = %d, want 3", stats.SinkCount)
	}
}

func TestWriteFrameToNonExistentPipeline(t *testing.T) {
	mgr := NewPipelineManager(nil, 0)
	if err := mgr.WriteFrame("no-stream", testFrame(0)); !errors.Is(err, ErrPipelineNotFound) {
		t.Errorf("WriteFrame to non-existent pipeline should return ErrPipelineNotFound, got %v", err)
	}
}

// ─── Backpressure ───────────────────────────────────────────────────────────

func TestBackpressureDropsFrames(t *testing.T) {
	// 使用很小的 buffer 和延迟 sink 模拟 backpressure
	mgr := NewPipelineManager(nil, 2) // buffer=2
	p := mgr.CreatePipeline("stream-1")

	slowSink := &mockSink{
		id:       "slow",
		protocol: "hls",
		delay:    50 * time.Millisecond, // 每帧 50ms，消费很慢
	}
	_ = p.AddSink(slowSink)

	// 快速写入大量帧，channel 很快填满
	const n = 50
	for i := uint16(0); i < n; i++ {
		mgr.WriteFrame("stream-1", testFrame(i))
	}

	// 应有大量丢帧（buffer=2 + 1 正在处理，最多 3 帧不丢）
	stats := p.Stats()
	if stats.FramesIn != n {
		t.Errorf("FramesIn = %d, want %d", stats.FramesIn, n)
	}
	if stats.FramesDropped == 0 {
		t.Error("FramesDropped = 0, expected some drops under backpressure")
	}
	t.Logf("backpressure: FramesIn=%d, FramesDropped=%d", stats.FramesIn, stats.FramesDropped)

	// sink 级别也应有丢帧统计
	sinkStats := stats.Sinks[0]
	if sinkStats.FramesDropped == 0 {
		t.Error("sink FramesDropped = 0, expected drops")
	}
	if sinkStats.ID != "slow" {
		t.Errorf("sink ID = %q, want slow", sinkStats.ID)
	}
	if sinkStats.Protocol != "hls" {
		t.Errorf("sink Protocol = %q, want hls", sinkStats.Protocol)
	}

	// 等待 slow sink 处理完已接收的帧
	time.Sleep(500 * time.Millisecond)
}

// ─── Sink WriteFrame 错误处理 ───────────────────────────────────────────────

func TestSinkWriteFrameErrorRetriesAndCloses(t *testing.T) {
	mgr := NewPipelineManager(nil, 0)
	p := mgr.CreatePipeline("stream-1")

	// 持续返回错误的 sink
	errSink := &mockSink{
		id:       "err-sink",
		protocol: "rtmp",
		writeErr: errors.New("permanent error"),
	}
	_ = p.AddSink(errSink)

	// 写入一帧
	mgr.WriteFrame("stream-1", testFrame(0))

	// 等待重试 3 次后关闭
	if !waitForCount(&errSink.writeCount, 3, 2*time.Second) {
		t.Errorf("writeCount = %d, want 3 (retries)", atomic.LoadInt32(&errSink.writeCount))
	}

	// 等待 Close 被调用
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&errSink.closeCount) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt32(&errSink.closeCount) < 1 {
		t.Errorf("closeCount = %d, want >=1 after 3 failed retries", errSink.closeCount)
	}

	// 验证统计中 errors >= 1
	stats := p.Stats()
	found := false
	for _, s := range stats.Sinks {
		if s.ID == "err-sink" {
			if s.Errors < 1 {
				t.Errorf("err-sink Errors = %d, want >=1", s.Errors)
			}
			if s.Active {
				t.Error("err-sink should be inactive after close")
			}
			found = true
		}
	}
	if !found {
		t.Error("err-sink not found in stats (may have been removed by close)")
	}
}

func TestSinkWriteFrameTransientErrorRecovers(t *testing.T) {
	mgr := NewPipelineManager(nil, 0)
	p := mgr.CreatePipeline("stream-1")

	// 前 2 次失败，第 3 次成功（模拟瞬时错误，重试内恢复）
	transientSink := &mockSink{
		id:         "transient",
		protocol:   "whep",
		failFirstN: 2,
	}
	_ = p.AddSink(transientSink)

	mgr.WriteFrame("stream-1", testFrame(0))

	// 应在第 3 次尝试成功
	if !waitForCount(&transientSink.writeCount, 3, 2*time.Second) {
		t.Errorf("writeCount = %d, want 3", atomic.LoadInt32(&transientSink.writeCount))
	}

	// 等待帧写入完成
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		transientSink.mu.Lock()
		n := len(transientSink.frames)
		transientSink.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	transientSink.mu.Lock()
	got := len(transientSink.frames)
	transientSink.mu.Unlock()
	if got != 1 {
		t.Errorf("transient sink frames = %d, want 1 (recovered after retry)", got)
	}

	// 不应关闭
	if atomic.LoadInt32(&transientSink.closeCount) != 0 {
		t.Errorf("transient sink closeCount = %d, want 0 (should not close on recovery)", transientSink.closeCount)
	}
}

// ─── Stats 统计 ─────────────────────────────────────────────────────────────

func TestStatsCorrect(t *testing.T) {
	mgr := NewPipelineManager(nil, 0)
	p := mgr.CreatePipeline("stream-stats")

	sink1 := newMockSink("s1", "hls")
	sink2 := newMockSink("s2", "dash")
	_ = p.AddSink(sink1)
	_ = p.AddSink(sink2)

	const n = 20
	for i := uint16(0); i < n; i++ {
		p.WriteFrame(testFrame(i))
	}

	// 等待 sink 处理
	if !waitForCount(&sink1.writeCount, n, 2*time.Second) {
		t.Errorf("sink1 writeCount = %d, want %d", atomic.LoadInt32(&sink1.writeCount), n)
	}
	if !waitForCount(&sink2.writeCount, n, 2*time.Second) {
		t.Errorf("sink2 writeCount = %d, want %d", atomic.LoadInt32(&sink2.writeCount), n)
	}

	stats := p.Stats()
	if stats.StreamID != "stream-stats" {
		t.Errorf("StreamID = %q, want stream-stats", stats.StreamID)
	}
	if stats.SinkCount != 2 {
		t.Errorf("SinkCount = %d, want 2", stats.SinkCount)
	}
	if stats.FramesIn != n {
		t.Errorf("FramesIn = %d, want %d", stats.FramesIn, n)
	}
	if stats.FramesDropped != 0 {
		t.Errorf("FramesDropped = %d, want 0", stats.FramesDropped)
	}
	if len(stats.Sinks) != 2 {
		t.Fatalf("Sinks len = %d, want 2", len(stats.Sinks))
	}

	// 每个 sink 应有 n 帧写入
	for _, s := range stats.Sinks {
		if s.FramesWritten != n {
			t.Errorf("sink %s FramesWritten = %d, want %d", s.ID, s.FramesWritten, n)
		}
		if s.FramesDropped != 0 {
			t.Errorf("sink %s FramesDropped = %d, want 0", s.ID, s.FramesDropped)
		}
		if s.Errors != 0 {
			t.Errorf("sink %s Errors = %d, want 0", s.ID, s.Errors)
		}
		if !s.Active {
			t.Errorf("sink %s Active = false, want true", s.ID)
		}
	}

	// PipelineStats via manager
	mStats, ok := mgr.PipelineStats("stream-stats")
	if !ok {
		t.Fatal("PipelineStats returned false")
	}
	if mStats.FramesIn != n {
		t.Errorf("manager PipelineStats FramesIn = %d, want %d", mStats.FramesIn, n)
	}

	// 不存在的流
	if _, ok := mgr.PipelineStats("no-stream"); ok {
		t.Error("PipelineStats for non-existent stream should return false")
	}
}

// ─── Close ──────────────────────────────────────────────────────────────────

func TestClosePipelineClosesAllSinks(t *testing.T) {
	mgr := NewPipelineManager(nil, 0)
	p := mgr.CreatePipeline("stream-close")

	sink1 := newMockSink("s1", "hls")
	sink2 := newMockSink("s2", "dash")
	sink3 := newMockSink("s3", "ws")
	_ = p.AddSink(sink1)
	_ = p.AddSink(sink2)
	_ = p.AddSink(sink3)

	p.Close()

	for i, s := range []*mockSink{sink1, sink2, sink3} {
		if atomic.LoadInt32(&s.closeCount) != 1 {
			t.Errorf("sink %d closeCount = %d, want 1", i+1, s.closeCount)
		}
	}

	// Close 后再写入不应 panic 且不应产生效果
	p.WriteFrame(testFrame(0))
	stats := p.Stats()
	if stats.FramesIn != 0 {
		t.Errorf("FramesIn after close = %d, want 0", stats.FramesIn)
	}

	// Close 幂等
	p.Close() // 不应 panic
}

func TestRemoveSinkViaManager(t *testing.T) {
	mgr := NewPipelineManager(nil, 0)
	mgr.CreatePipeline("stream-1")
	sink := newMockSink("s1", "hls")
	_ = mgr.AddSink("stream-1", sink)

	if err := mgr.RemoveSink("stream-1", "s1"); err != nil {
		t.Fatalf("RemoveSink: %v", err)
	}
	if atomic.LoadInt32(&sink.closeCount) != 1 {
		t.Errorf("closeCount = %d, want 1", sink.closeCount)
	}

	// 从不存在的流移除
	if err := mgr.RemoveSink("no-stream", "s1"); !errors.Is(err, ErrPipelineNotFound) {
		t.Errorf("RemoveSink from non-existent stream should return ErrPipelineNotFound, got %v", err)
	}
}

// ─── 多流隔离 ───────────────────────────────────────────────────────────────

func TestMultipleStreamsIsolated(t *testing.T) {
	mgr := NewPipelineManager(nil, 0)
	p1 := mgr.CreatePipeline("stream-1")
	p2 := mgr.CreatePipeline("stream-2")

	sink1 := newMockSink("sink1", "hls")
	sink2 := newMockSink("sink2", "dash")
	_ = p1.AddSink(sink1)
	_ = p2.AddSink(sink2)

	// 只向 stream-1 写
	mgr.WriteFrame("stream-1", testFrame(0))
	mgr.WriteFrame("stream-1", testFrame(1))

	if !waitForCount(&sink1.writeCount, 2, 2*time.Second) {
		t.Errorf("sink1 writeCount = %d, want 2", atomic.LoadInt32(&sink1.writeCount))
	}

	// sink2 不应收到帧
	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(&sink2.writeCount) != 0 {
		t.Errorf("sink2 writeCount = %d, want 0 (isolated)", atomic.LoadInt32(&sink2.writeCount))
	}

	// 各自统计独立
	st1, _ := mgr.PipelineStats("stream-1")
	st2, _ := mgr.PipelineStats("stream-2")
	if st1.FramesIn != 2 {
		t.Errorf("stream-1 FramesIn = %d, want 2", st1.FramesIn)
	}
	if st2.FramesIn != 0 {
		t.Errorf("stream-2 FramesIn = %d, want 0", st2.FramesIn)
	}

	if list := mgr.ListPipelines(); len(list) != 2 {
		t.Errorf("ListPipelines len = %d, want 2", len(list))
	}
}
