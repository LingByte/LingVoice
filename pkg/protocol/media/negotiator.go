// Package media bridges the protocol layer's codec negotiation with the
// pkg/media encoder registry. It provides:
//
//   - CodecType ↔ CodecConfig mapping (common.CodecType to media.CodecConfig)
//   - NegotiateAudio: selects the best codec from an offer list, checking
//     actual support via encoder.HasCodec()
//   - CreateSession: builds a media.MediaSession with encoder/decoder wired
//     from the negotiated codec
//
// This replaces the protocol layer's previous standalone codec matching
// (which only checked a static string list) with a real check against the
// media encoder registry.
package media

import (
	"fmt"
	"strings"

	"github.com/LingByte/LingVoice/pkg/media"
	"github.com/LingByte/LingVoice/pkg/media/encoder"
	"github.com/LingByte/LingVoice/pkg/protocol/common"
)

// CodecTypeToConfig converts a common.CodecType + audio params to a
// media.CodecConfig suitable for the encoder registry.
func CodecTypeToConfig(codec common.CodecType, sampleRate uint32, channels uint16, frameMs uint16) media.CodecConfig {
	return media.CodecConfig{
		Codec:         codecTypeToName(codec),
		SampleRate:    int(sampleRate),
		Channels:      int(channels),
		BitDepth:      16,
		FrameDuration: fmt.Sprintf("%dms", frameMs),
	}
}

// CodecConfigToType converts a media.CodecConfig back to common.CodecType.
func CodecConfigToType(cfg media.CodecConfig) common.CodecType {
	return codecNameToType(cfg.Codec)
}

// codecTypeToName maps common.CodecType to the string name used by the
// encoder registry (encoder.CodecPCM, encoder.CodecPCMU, etc.).
func codecTypeToName(c common.CodecType) string {
	switch c {
	case common.CodecOpus:
		return encoder.CodecOPUS
	case common.CodecPCMU:
		return encoder.CodecPCMU
	case common.CodecPCMA:
		return encoder.CodecPCMA
	case common.CodecPCM16:
		return encoder.CodecPCM
	default:
		return strings.ToLower(c.String())
	}
}

// codecNameToType maps an encoder registry codec name back to common.CodecType.
func codecNameToType(name string) common.CodecType {
	switch strings.ToLower(name) {
	case "opus":
		return common.CodecOpus
	case "pcmu":
		return common.CodecPCMU
	case "pcma":
		return common.CodecPCMA
	case "pcm", "pcm16":
		return common.CodecPCM16
	default:
		// Try common.CodecFromString for any not covered above
		ct, _ := common.CodecFromString(name)
		return ct
	}
}

// NegotiationResult holds the outcome of a codec negotiation.
type NegotiationResult struct {
	// Audio is the protocol-layer media description (for ProtocolEvent).
	Audio *common.AudioMedia
	// CodecConfig is the media-layer codec config (for encoder/decoder creation).
	CodecConfig media.CodecConfig
	// CodecName is the encoder registry name (e.g. "opus", "pcmu").
	CodecName string
}

// NegotiateAudio selects the best audio codec from the client's offer list,
// checking actual support via the encoder registry (encoder.HasCodec).
//
// serverPrefs is the server's preferred codec order (highest priority first).
// offerCodecs is the client's supported codec list.
// The first codec in serverPrefs that is also in offerCodecs AND registered
// in the encoder registry is selected.
//
// sampleRate, channels, frameMs are the server's preferred audio parameters;
// they are validated against the offer if offerSampleRates/offerChannels are
// non-empty.
func NegotiateAudio(
	serverPrefs []string,
	offerCodecs []string,
	offerSampleRates []uint32,
	offerChannels []uint16,
	preferredSampleRate uint32,
	preferredChannels uint16,
	frameMs uint16,
) (*NegotiationResult, error) {
	if len(offerCodecs) == 0 {
		return nil, fmt.Errorf("no codec in offer")
	}

	offerSet := make(map[string]bool, len(offerCodecs))
	for _, c := range offerCodecs {
		offerSet[strings.ToLower(c)] = true
	}

	for _, pref := range serverPrefs {
		prefLower := strings.ToLower(pref)
		if !offerSet[prefLower] {
			continue
		}

		// Check actual support in the encoder registry
		if !encoder.HasCodec(prefLower) {
			continue
		}

		codecType, err := common.CodecFromString(prefLower)
		if err != nil {
			continue
		}

		// Validate sample rate against offer if provided
		sr := preferredSampleRate
		if len(offerSampleRates) > 0 {
			found := false
			for _, r := range offerSampleRates {
				if r == sr {
					found = true
					break
				}
			}
			if !found && len(offerSampleRates) > 0 {
				// Use the first offered sample rate as fallback
				sr = offerSampleRates[0]
			}
		}

		// Validate channels against offer if provided
		ch := preferredChannels
		if len(offerChannels) > 0 {
			found := false
			for _, c := range offerChannels {
				if c == ch {
					found = true
					break
				}
			}
			if !found && len(offerChannels) > 0 {
				ch = offerChannels[0]
			}
		}

		cfg := CodecTypeToConfig(codecType, sr, ch, frameMs)

		return &NegotiationResult{
			Audio: &common.AudioMedia{
				Codec:           codecType,
				SampleRate:      sr,
				Channels:        ch,
				FrameDurationMs: frameMs,
			},
			CodecConfig: cfg,
			CodecName:   prefLower,
		}, nil
	}

	return nil, fmt.Errorf("no supported codec in offer (offered: %v, server prefs: %v)", offerCodecs, serverPrefs)
}

// NegotiateFromSDP parses a simplified SDP body and negotiates using the
// encoder registry. Used by SIP.
//
// serverPrefs is the preferred codec order. The first codec in the SDP
// that matches a server preference AND is registered in the encoder
// registry is selected.
func NegotiateFromSDP(
	sdpBody []byte,
	serverPrefs []string,
	frameMs uint16,
) (*NegotiationResult, error) {
	if len(sdpBody) == 0 {
		return nil, fmt.Errorf("empty SDP")
	}

	// Parse rtpmap lines: a=rtpmap:<pt> <codec>/<clockrate>[/<channels>]
	type rtpCodec struct {
		name       string
		clockRate  uint32
		channels   uint16
	}
	codecs := make([]rtpCodec, 0)

	lines := strings.Split(string(sdpBody), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "a=rtpmap:") {
			continue
		}
		rest := line[9:] // strip "a=rtpmap:"
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) < 2 {
			continue
		}
		codecStr := parts[1]
		codecParts := strings.Split(codecStr, "/")
		if len(codecParts) < 2 {
			continue
		}
		name := strings.ToLower(codecParts[0])
		var cr uint32
		fmt.Sscanf(codecParts[1], "%d", &cr)
		ch := uint16(1)
		if len(codecParts) >= 3 {
			fmt.Sscanf(codecParts[2], "%d", &ch)
		}
		codecs = append(codecs, rtpCodec{name: name, clockRate: cr, channels: ch})
	}

	if len(codecs) == 0 {
		return nil, fmt.Errorf("no rtpmap in SDP")
	}

	// Match against server preferences
	for _, pref := range serverPrefs {
		prefLower := strings.ToLower(pref)
		for _, c := range codecs {
			if c.name != prefLower {
				continue
			}
			if !encoder.HasCodec(c.name) {
				continue
			}
			codecType, err := common.CodecFromString(c.name)
			if err != nil {
				continue
			}
			cfg := CodecTypeToConfig(codecType, c.clockRate, c.channels, frameMs)
			return &NegotiationResult{
				Audio: &common.AudioMedia{
					Codec:           codecType,
					SampleRate:      c.clockRate,
					Channels:        c.channels,
					FrameDurationMs: frameMs,
				},
				CodecConfig: cfg,
				CodecName:   c.name,
			}, nil
		}
	}

	return nil, fmt.Errorf("no supported codec in SDP")
}

// CreateEncoderDecoder creates encoder and decoder functions from a
// NegotiationResult using the media encoder registry.
//
// pcmConfig is the internal PCM format (usually 16kHz mono for ASR/TTS).
// The encoder converts PCM→codec for outbound, the decoder converts
// codec→PCM for inbound.
func CreateEncoderDecoder(result *NegotiationResult, pcmConfig media.CodecConfig) (
	encode media.EncoderFunc,
	decode media.EncoderFunc,
	err error,
) {
	encode, err = encoder.CreateEncode(result.CodecConfig, pcmConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("create encoder for %s: %w", result.CodecName, err)
	}
	decode, err = encoder.CreateDecode(result.CodecConfig, pcmConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("create decoder for %s: %w", result.CodecName, err)
	}
	return encode, decode, nil
}

// DefaultPCMConfig returns the default internal PCM configuration used by
// LingVoice for ASR/TTS processing: 16kHz mono 16-bit.
func DefaultPCMConfig() media.CodecConfig {
	return media.CodecConfig{
		Codec:      encoder.CodecPCM,
		SampleRate: 16000,
		Channels:   1,
		BitDepth:   16,
	}
}
