package dash

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
	videoRep := &Representation{ID: "v0", Width: 640, Height: 360, Codec: common.CodecH264}
	audioRep := &Representation{ID: "a0", SampleRate: 48000, Channels: 2, Codec: common.CodecAAC}
	st := seg.CreateStream("test-stream", videoRep, audioRep)
	if st == nil {
		t.Fatal("expected stream, got nil")
	}
}

func TestSegmenterPushFrame(t *testing.T) {
	seg := NewSegmenter(DefaultConfig(), nil)
	st := seg.CreateStream("test", &Representation{ID: "v0", Width: 320, Height: 240, Codec: common.CodecH264}, nil)

	frame := common.MediaFrame{
		Type:    common.FrameVideo,
		Codec:   common.CodecH264,
		Payload: []byte{0, 0, 0, 1, 0x67},
		Marker:  true,
	}
	if err := st.pushFrame(frame); err != nil {
		t.Fatalf("pushFrame failed: %v", err)
	}
}

func TestSegmenterCutSegment(t *testing.T) {
	cfg := Config{
		Addr:            ":0",
		Path:            "/dash",
		SegmentDuration: 100 * time.Millisecond,
		MaxSegments:     3,
		MinBufferTime:   1 * time.Second,
	}
	seg := NewSegmenter(cfg, nil)
	st := seg.CreateStream("test", &Representation{ID: "v0", Width: 320, Height: 240, Codec: common.CodecH264}, nil)

	for i := 0; i < 5; i++ {
		frame := common.MediaFrame{
			Type:    common.FrameVideo,
			Codec:   common.CodecH264,
			Payload: []byte{0, 0, 0, 1, byte(i)},
			Marker:  true,
		}
		_ = st.pushFrame(frame)
		time.Sleep(60 * time.Millisecond)
	}

	st.mu.RLock()
	segCount := len(st.segments)
	st.mu.RUnlock()

	if segCount == 0 {
		t.Error("expected at least 1 segment")
	}
}

func TestMPDGeneration(t *testing.T) {
	seg := NewSegmenter(DefaultConfig(), nil)
	videoRep := &Representation{ID: "v0", Width: 640, Height: 360, Codec: common.CodecH264}
	audioRep := &Representation{ID: "a0", SampleRate: 48000, Channels: 2, Codec: common.CodecAAC}
	st := seg.CreateStream("test", videoRep, audioRep)

	mpd := st.GenerateMPD()
	if !strings.Contains(mpd, "<MPD") {
		t.Error("MPD should contain <MPD tag")
	}
	if !strings.Contains(mpd, `type="dynamic"`) {
		t.Error("MPD should be dynamic type for live")
	}
	if !strings.Contains(mpd, "<AdaptationSet") {
		t.Error("MPD should contain AdaptationSet")
	}
	if !strings.Contains(mpd, "<SegmentTemplate") {
		t.Error("MPD should contain SegmentTemplate")
	}
}

func TestHTTPHandler(t *testing.T) {
	seg := NewSegmenter(DefaultConfig(), nil)
	videoRep := &Representation{ID: "v0", Width: 640, Height: 360, Codec: common.CodecH264}
	seg.CreateStream("test", videoRep, nil)

	req := httptest.NewRequest(http.MethodGet, "/dash/test/manifest.mpd", nil)
	rec := httptest.NewRecorder()
	seg.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/dash+xml" {
		t.Errorf("expected dash+xml content type, got '%s'", rec.Header().Get("Content-Type"))
	}
}

func TestHTTPHandlerStreamNotFound(t *testing.T) {
	seg := NewSegmenter(DefaultConfig(), nil)

	req := httptest.NewRequest(http.MethodGet, "/dash/nonexistent/manifest.mpd", nil)
	rec := httptest.NewRecorder()
	seg.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestRemoveStream(t *testing.T) {
	seg := NewSegmenter(DefaultConfig(), nil)
	seg.CreateStream("test", &Representation{ID: "v0", Width: 320, Height: 240, Codec: common.CodecH264}, nil)

	seg.RemoveStream("test")
	if _, ok := seg.GetStream("test"); ok {
		t.Error("stream should be removed")
	}
}
