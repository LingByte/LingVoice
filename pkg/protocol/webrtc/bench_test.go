package webrtc

import (
	"encoding/json"
	"testing"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// BenchmarkSessionCreate 会话创建性能
func BenchmarkSessionCreate(b *testing.B) {
	cfg := newTestConfig()
	handler := newTestHandler()
	log := zap.NewNop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess, err := newSession(uuid.NewString(), cfg, handler, log)
		if err != nil {
			b.Fatal(err)
		}
		sess.Close()
	}
}

// BenchmarkAddTrack 添加轨道性能
func BenchmarkAddTrack(b *testing.B) {
	cfg := newTestConfig()
	handler := newTestHandler()
	log := zap.NewNop()

	sess, err := newSession(uuid.NewString(), cfg, handler, log)
	if err != nil {
		b.Fatal(err)
	}
	defer sess.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		trackID, err := sess.AddTrack(common.TrackConfig{
			Kind:       common.TrackAudio,
			Codec:      common.CodecOpus,
			SampleRate: 48000,
			Channels:   2,
			StreamID:   "bench",
			Label:      "audio",
		})
		if err != nil {
			b.Fatal(err)
		}
		_ = sess.RemoveTrack(trackID)
	}
}

// BenchmarkQoSCollect QoS 指标采集性能
func BenchmarkQoSCollect(b *testing.B) {
	cfg := newTestConfig()
	handler := newTestHandler()
	log := zap.NewNop()

	sess, err := newSession(uuid.NewString(), cfg, handler, log)
	if err != nil {
		b.Fatal(err)
	}
	defer sess.Close()

	// 添加一些轨道
	for i := 0; i < 4; i++ {
		_, _ = sess.AddTrack(common.TrackConfig{
			Kind:       common.TrackAudio,
			Codec:      common.CodecOpus,
			SampleRate: 48000,
			Channels:   2,
			StreamID:   "bench",
			Label:      "audio",
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sess.QoS()
	}
}

// BenchmarkMediaFrameConstruction MediaFrame 构造性能
func BenchmarkMediaFrameConstruction(b *testing.B) {
	payload := make([]byte, 160) // 20ms Opus frame

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = common.MediaFrame{
			Type:       common.FrameAudio,
			Codec:      common.CodecOpus,
			Payload:    payload,
			Timestamp:  uint32(i * 960),
			Sequence:   uint16(i),
			SampleRate: 48000,
			Channels:   2,
			SSRC:       12345,
			Marker:     true,
		}
	}
}

// BenchmarkSignalMessageMarshal 信令消息序列化性能
func BenchmarkSignalMessageMarshal(b *testing.B) {
	msg := signalMessage{
		Type: "pub_answer",
		SDP:  "v=0\r\no=- 123456 2 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\n",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(msg)
	}
}
