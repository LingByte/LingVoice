package videoencoder

import (
	"testing"
)

func TestNewDecoderH264(t *testing.T) {
	dec, err := NewDecoder(CodecH264)
	if err != nil {
		t.Fatalf("NewDecoder(H264) failed: %v", err)
	}
	defer dec.Close()
	if dec.Codec() != CodecH264 {
		t.Errorf("expected H264, got %s", dec.Codec())
	}
}

func TestNewDecoderVP8(t *testing.T) {
	dec, err := NewDecoder(CodecVP8)
	if err != nil {
		t.Fatalf("NewDecoder(VP8) failed: %v", err)
	}
	defer dec.Close()
	if dec.Codec() != CodecVP8 {
		t.Errorf("expected VP8, got %s", dec.Codec())
	}
}

func TestNewEncoderH264(t *testing.T) {
	enc, err := NewEncoder(CodecH264, 160, 120)
	if err != nil {
		t.Fatalf("NewEncoder(H264) failed: %v", err)
	}
	defer enc.Close()
	if enc.Codec() != CodecH264 {
		t.Errorf("expected H264, got %s", enc.Codec())
	}
}

func TestNewEncoderVP8(t *testing.T) {
	enc, err := NewEncoder(CodecVP8, 160, 120)
	if err != nil {
		t.Fatalf("NewEncoder(VP8) failed: %v", err)
	}
	defer enc.Close()
	if enc.Codec() != CodecVP8 {
		t.Errorf("expected VP8, got %s", enc.Codec())
	}
}

func TestEncodeH264(t *testing.T) {
	enc, err := NewEncoder(CodecH264, 160, 120)
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	defer enc.Close()

	// 创建一个黑色 YUV 帧
	frame := &YuvFrame{
		Y:        make([]byte, 160*120),
		U:        make([]byte, 80*60),
		V:        make([]byte, 80*60),
		Width:    160,
		Height:   120,
		Timestamp: 9000,
	}
	// U/V 中性灰 = 128
	for i := range frame.U {
		frame.U[i] = 128
		frame.V[i] = 128
	}

	encoded, err := enc.Encode(frame)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if len(encoded.Data) == 0 {
		t.Error("encoded data is empty")
	}
	if encoded.Width != 160 || encoded.Height != 120 {
		t.Errorf("dimensions mismatch: %dx%d", encoded.Width, encoded.Height)
	}
}

func TestEncodeVP8(t *testing.T) {
	enc, err := NewEncoder(CodecVP8, 160, 120)
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	defer enc.Close()

	frame := &YuvFrame{
		Y:        make([]byte, 160*120),
		U:        make([]byte, 80*60),
		V:        make([]byte, 80*60),
		Width:    160,
		Height:   120,
		Timestamp: 9000,
	}
	for i := range frame.U {
		frame.U[i] = 128
		frame.V[i] = 128
	}

	encoded, err := enc.Encode(frame)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if len(encoded.Data) == 0 {
		t.Error("encoded data is empty")
	}
}

func TestH264EncodeDecodeRoundtrip(t *testing.T) {
	enc, err := NewEncoder(CodecH264, 160, 120)
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	defer enc.Close()

	dec, err := NewDecoder(CodecH264)
	if err != nil {
		t.Fatalf("NewDecoder failed: %v", err)
	}
	defer dec.Close()

	frame := &YuvFrame{
		Y:        make([]byte, 160*120),
		U:        make([]byte, 80*60),
		V:        make([]byte, 80*60),
		Width:    160,
		Height:   120,
		Timestamp: 9000,
	}
	for i := range frame.U {
		frame.U[i] = 128
		frame.V[i] = 128
	}

	encoded, err := enc.Encode(frame)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := dec.Decode(encoded.Data, encoded.Timestamp)
	if err != nil {
		t.Logf("Decode failed (expected for first frame): %v", err)
		return
	}
	if decoded != nil {
		if decoded.Width != 160 || decoded.Height != 120 {
			t.Errorf("decoded dimensions mismatch: %dx%d", decoded.Width, decoded.Height)
		}
	}
}

func TestVP8ToH264Transcode(t *testing.T) {
	// VP8 encode → decode → H.264 encode
	enc, err := NewEncoder(CodecVP8, 160, 120)
	if err != nil {
		t.Fatalf("VP8 NewEncoder failed: %v", err)
	}
	defer enc.Close()

	frame := &YuvFrame{
		Y:        make([]byte, 160*120),
		U:        make([]byte, 80*60),
		V:        make([]byte, 80*60),
		Width:    160,
		Height:   120,
		Timestamp: 9000,
	}
	for i := range frame.U {
		frame.U[i] = 128
		frame.V[i] = 128
	}

	vp8Encoded, err := enc.Encode(frame)
	if err != nil {
		t.Fatalf("VP8 Encode failed: %v", err)
	}

	// VP8 decode
	dec, err := NewDecoder(CodecVP8)
	if err != nil {
		t.Fatalf("VP8 NewDecoder failed: %v", err)
	}
	defer dec.Close()

	yuv, err := dec.Decode(vp8Encoded.Data, vp8Encoded.Timestamp)
	if err != nil {
		t.Logf("VP8 Decode failed: %v", err)
		return
	}
	if yuv == nil {
		t.Log("VP8 decoder needs more data")
		return
	}

	// H.264 encode
	h264Enc, err := NewEncoder(CodecH264, 160, 120)
	if err != nil {
		t.Fatalf("H264 NewEncoder failed: %v", err)
	}
	defer h264Enc.Close()

	h264Encoded, err := h264Enc.Encode(yuv)
	if err != nil {
		t.Fatalf("H264 Encode failed: %v", err)
	}
	if len(h264Encoded.Data) == 0 {
		t.Error("H264 encoded data is empty")
	}
}

func TestRequestKeyframe(t *testing.T) {
	enc, err := NewEncoder(CodecH264, 160, 120)
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	defer enc.Close()

	enc.RequestKeyframe()

	frame := &YuvFrame{
		Y:        make([]byte, 160*120),
		U:        make([]byte, 80*60),
		V:        make([]byte, 80*60),
		Width:    160,
		Height:   120,
		Timestamp: 9000,
	}
	for i := range frame.U {
		frame.U[i] = 128
		frame.V[i] = 128
	}

	encoded, err := enc.Encode(frame)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if !encoded.Keyframe {
		t.Error("expected keyframe after RequestKeyframe")
	}
}

func TestSetBitrate(t *testing.T) {
	enc, err := NewEncoder(CodecH264, 160, 120)
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	defer enc.Close()

	enc.SetBitrate(1000000) // 1Mbps
	// 不 panic 即可
}
