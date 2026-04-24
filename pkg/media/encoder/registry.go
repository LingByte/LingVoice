package encoder

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"strings"
	"time"

	media2 "github.com/LingByte/LingVoice/pkg/media"
)

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

const (
	CodecPCM  = "pcm"
	CodecPCMU = "pcmu"
	CodecPCMA = "pcma"
	CodecG722 = "g722"
)

func init() {
	RegisterCodec(CodecPCMU, createPCMUEncode, createPCMUDecode)
	RegisterCodec(CodecPCMA, createPCMAEncode, createPCMADecode)
	RegisterCodec(CodecPCM, PcmToPcm, PcmToPcm)
	RegisterCodec(CodecG722, createG722Encode, createG722Decode)
}

// CodecFactory defines function type for creating codec encoders/decoders
type CodecFactory func(src, pcm media2.CodecConfig) media2.EncoderFunc

// codecRegistry stores encoder/decoder factory pairs
type codecRegistry struct {
	encoderFactory CodecFactory
	decoderFactory CodecFactory
}

var codecRegistryMap = make(map[string]codecRegistry)

// RegisterCodec registers a codec with encoder and decoder factories
func RegisterCodec(name string, encoderFactory, decoderFactory CodecFactory) {
	codecRegistryMap[strings.ToLower(name)] = codecRegistry{
		encoderFactory: encoderFactory,
		decoderFactory: decoderFactory,
	}
}

// BuildEncoder creates an encoder function for the specified codec
func CreateEncode(src, pcm media2.CodecConfig) (encode media2.EncoderFunc, err error) {
	registry, exists := codecRegistryMap[strings.ToLower(src.Codec)]
	if !exists {
		err = media2.ErrCodecNotSupported
		return
	}
	encode = registry.encoderFactory(src, pcm)
	return
}

// BuildDecoder creates a decoder function for the specified codec
func CreateDecode(src, pcm media2.CodecConfig) (decode media2.EncoderFunc, err error) {
	registry, exists := codecRegistryMap[strings.ToLower(src.Codec)]
	if !exists {
		err = media2.ErrCodecNotSupported
		return
	}
	decode = registry.decoderFactory(src, pcm)
	return
}

// IsCodecSupported checks if a codec is registered
func HasCodec(name string) bool {
	_, exists := codecRegistryMap[strings.ToLower(name)]
	return exists
}

// RemoveWavHeader removes WAV file header if present
func StripWavHeader(data []byte) []byte {
	const wavHeaderSize = 44
	const riffSignature = "RIFF"
	if len(data) > wavHeaderSize &&
		data[0] == riffSignature[0] &&
		data[1] == riffSignature[1] &&
		data[2] == riffSignature[2] &&
		data[3] == riffSignature[3] {
		return data[wavHeaderSize:]
	}
	return data
}

// splitFrames splits audio data into packets based on duration
func splitFrames(data []byte, src *media2.CodecConfig) []media2.MediaPacket {
	if src.FrameDuration == "" {
		return []media2.MediaPacket{&media2.AudioPacket{Payload: data}}
	}
	duration, _ := time.ParseDuration(src.FrameDuration)
	if duration < 10*time.Millisecond || duration > 300*time.Millisecond {
		duration = 20 * time.Millisecond
	}
	bytesPerFrame := int(duration.Milliseconds()) * src.SampleRate / 1000
	packets := make([]media2.MediaPacket, 0)

	for offset := 0; offset < len(data); offset += bytesPerFrame {
		frameEnd := offset + bytesPerFrame
		if frameEnd > len(data) {
			frameEnd = len(data)
		}
		packets = append(packets, &media2.AudioPacket{Payload: data[offset:frameEnd]})
	}
	return packets
}
