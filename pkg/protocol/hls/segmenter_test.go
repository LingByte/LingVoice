package hls

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
)

func TestSegmenterCreateStream(t *testing.T) {
	seg := NewSegmenter(DefaultConfig(), nil)
	st := seg.CreateStream("test-stream", common.CodecH264, common.CodecAAC)
	if st == nil {
		t.Fatal("expected stream, got nil")
	}
	if st.id != "test-stream" {
		t.Errorf("expected id 'test-stream', got '%s'", st.id)
	}
}

func TestSegmenterPushFrame(t *testing.T) {
	seg := NewSegmenter(DefaultConfig(), nil)
	st := seg.CreateStream("test-stream", common.CodecH264, common.CodecAAC)

	frame := common.MediaFrame{
		Type:      common.FrameVideo,
		Codec:     common.CodecH264,
		Payload:   []byte{0, 0, 0, 1, 0x67, 0x42, 0x00, 0x0a},
		Timestamp: 0,
		Marker:    true,
	}

	if err := st.pushFrame(frame); err != nil {
		t.Fatalf("pushFrame failed: %v", err)
	}
}

func TestSegmenterCutSegment(t *testing.T) {
	cfg := Config{
		Addr:            ":0",
		Path:            "/hls",
		SegmentDuration: 100 * time.Millisecond,
		PartDuration:    50 * time.Millisecond,
		MaxSegments:     3,
		Mode:            ModeClassic,
	}
	seg := NewSegmenter(cfg, nil)
	st := seg.CreateStream("test", common.CodecH264, common.CodecAAC)

	// Push frames with markers to trigger segment cuts
	for i := 0; i < 5; i++ {
		frame := common.MediaFrame{
			Type:      common.FrameVideo,
			Codec:     common.CodecH264,
			Payload:   []byte{0, 0, 0, 1, byte(i)},
			Timestamp: uint32(i * 3000),
			Marker:    true,
		}
		_ = st.pushFrame(frame)
		time.Sleep(60 * time.Millisecond)
	}

	st.mu.RLock()
	segCount := len(st.segments)
	st.mu.RUnlock()

	if segCount == 0 {
		t.Error("expected at least 1 segment after pushing frames")
	}
}

func TestPlaylistGeneration(t *testing.T) {
	seg := NewSegmenter(DefaultConfig(), nil)
	st := seg.CreateStream("test", common.CodecH264, common.CodecAAC)

	playlist := st.GeneratePlaylist()
	if !strings.HasPrefix(playlist, "#EXTM3U") {
		t.Error("playlist should start with #EXTM3U")
	}
	if !strings.Contains(playlist, "#EXT-X-VERSION:6") {
		t.Error("playlist should contain EXT-X-VERSION:6")
	}
}

func TestLLHLSPlaylistFeatures(t *testing.T) {
	cfg := DefaultConfig()
	seg := NewSegmenter(cfg, nil)
	st := seg.CreateStream("test", common.CodecH264, common.CodecAAC)

	playlist := st.GeneratePlaylist()
	if !strings.Contains(playlist, "#EXT-X-PART-INF") {
		t.Error("LL-HLS playlist should contain EXT-X-PART-INF")
	}
	if !strings.Contains(playlist, "#EXT-X-SERVER-CONTROL") {
		t.Error("LL-HLS playlist should contain EXT-X-SERVER-CONTROL")
	}
	if !strings.Contains(playlist, "CAN-BLOCK-RELOAD=YES") {
		t.Error("LL-HLS playlist should have CAN-BLOCK-RELOAD=YES")
	}
}

func TestHTTPHandler(t *testing.T) {
	seg := NewSegmenter(DefaultConfig(), nil)
	seg.CreateStream("test", common.CodecH264, common.CodecAAC)

	// Test playlist endpoint
	req := httptest.NewRequest(http.MethodGet, "/hls/test/playlist.m3u8", nil)
	rec := httptest.NewRecorder()
	seg.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/vnd.apple.mpegurl" {
		t.Errorf("expected mpegurl content type, got '%s'", rec.Header().Get("Content-Type"))
	}
}

func TestHTTPHandlerStreamNotFound(t *testing.T) {
	seg := NewSegmenter(DefaultConfig(), nil)

	req := httptest.NewRequest(http.MethodGet, "/hls/nonexistent/playlist.m3u8", nil)
	rec := httptest.NewRecorder()
	seg.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestRemoveStream(t *testing.T) {
	seg := NewSegmenter(DefaultConfig(), nil)
	seg.CreateStream("test", common.CodecH264, common.CodecAAC)

	seg.RemoveStream("test")
	if _, ok := seg.GetStream("test"); ok {
		t.Error("stream should be removed")
	}
}
