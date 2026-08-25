package media

import (
	"testing"

	"github.com/LingByte/LingVoice/pkg/media"
	"github.com/LingByte/LingVoice/pkg/protocol/common"
)

func TestCodecTypeToConfig(t *testing.T) {
	cfg := CodecTypeToConfig(common.CodecOpus, 48000, 2, 20)
	if cfg.Codec != "opus" {
		t.Fatalf("expected opus, got %s", cfg.Codec)
	}
	if cfg.SampleRate != 48000 {
		t.Fatalf("expected 48000, got %d", cfg.SampleRate)
	}
	if cfg.Channels != 2 {
		t.Fatalf("expected 2 channels, got %d", cfg.Channels)
	}
}

func TestCodecConfigToType(t *testing.T) {
	cfg := media.CodecConfig{Codec: "pcmu", SampleRate: 8000}
	ct := CodecConfigToType(cfg)
	if ct != common.CodecPCMU {
		t.Fatalf("expected CodecPCMU, got %v", ct)
	}
}

func TestNegotiateAudio_Match(t *testing.T) {
	result, err := NegotiateAudio(
		[]string{"opus", "pcmu", "pcma"}, // server prefs
		[]string{"pcmu", "pcma"},         // client offer
		[]uint32{8000},
		[]uint16{1},
		48000, // preferred SR (not in offer, will fallback to 8000)
		1,     // preferred channels
		20,    // frame ms
	)
	if err != nil {
		t.Fatalf("negotiation failed: %v", err)
	}
	// Server prefers opus but client doesn't offer it; pcmu is next
	if result.CodecName != "pcmu" {
		t.Fatalf("expected pcmu, got %s", result.CodecName)
	}
	if result.Audio.Codec != common.CodecPCMU {
		t.Fatalf("expected CodecPCMU, got %v", result.Audio.Codec)
	}
	if result.Audio.SampleRate != 8000 {
		t.Fatalf("expected 8000, got %d", result.Audio.SampleRate)
	}
}

func TestNegotiateAudio_NoMatch(t *testing.T) {
	_, err := NegotiateAudio(
		[]string{"opus"},
		[]string{"pcmu"}, // no overlap
		nil, nil, 48000, 1, 20,
	)
	if err == nil {
		t.Fatal("expected error for no matching codec")
	}
}

func TestNegotiateAudio_UnsupportedCodecSkipped(t *testing.T) {
	// "gsm" is in common.CodecType but NOT in encoder registry
	_, err := NegotiateAudio(
		[]string{"gsm", "pcmu"},
		[]string{"gsm", "pcmu"},
		nil, nil, 8000, 1, 20,
	)
	if err != nil {
		t.Fatalf("negotiation failed: %v", err)
	}
	// Should skip gsm (not registered) and select pcmu
}

func TestNegotiateFromSDP(t *testing.T) {
	sdp := []byte("v=0\r\n" +
		"o=- 123 1 IN IP4 0.0.0.0\r\n" +
		"s=call\r\n" +
		"c=IN IP4 0.0.0.0\r\n" +
		"m=audio 5004 RTP/AVP 0 8 96\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n" +
		"a=rtpmap:8 PCMA/8000\r\n" +
		"a=rtpmap:96 opus/48000/2\r\n")

	result, err := NegotiateFromSDP(sdp, []string{"opus", "pcmu", "pcma"}, 20)
	if err != nil {
		t.Fatalf("SDP negotiation failed: %v", err)
	}
	// Server prefers opus, SDP has it
	if result.CodecName != "opus" {
		t.Fatalf("expected opus, got %s", result.CodecName)
	}
	if result.Audio.SampleRate != 48000 {
		t.Fatalf("expected 48000, got %d", result.Audio.SampleRate)
	}
	if result.Audio.Channels != 2 {
		t.Fatalf("expected 2 channels, got %d", result.Audio.Channels)
	}
}

func TestNegotiateFromSDP_Empty(t *testing.T) {
	_, err := NegotiateFromSDP(nil, []string{"opus"}, 20)
	if err == nil {
		t.Fatal("expected error for empty SDP")
	}
}

func TestCreateEncoderDecoder(t *testing.T) {
	result, err := NegotiateAudio(
		[]string{"pcmu"},
		[]string{"pcmu"},
		[]uint32{8000},
		[]uint16{1},
		8000, 1, 20,
	)
	if err != nil {
		t.Fatalf("negotiation failed: %v", err)
	}

	pcmCfg := DefaultPCMConfig()
	enc, dec, err := CreateEncoderDecoder(result, pcmCfg)
	if err != nil {
		t.Fatalf("create encoder/decoder failed: %v", err)
	}
	if enc == nil {
		t.Fatal("encoder is nil")
	}
	if dec == nil {
		t.Fatal("decoder is nil")
	}
}

func TestDefaultPCMConfig(t *testing.T) {
	cfg := DefaultPCMConfig()
	if cfg.SampleRate != 16000 {
		t.Fatalf("expected 16000, got %d", cfg.SampleRate)
	}
	if cfg.Channels != 1 {
		t.Fatalf("expected 1 channel, got %d", cfg.Channels)
	}
}
