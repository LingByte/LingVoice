package encoder

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"github.com/LingByte/LingVoice/pkg/media"
)

func PcmToPcm(src, pcm media.CodecConfig) media.EncoderFunc {
	res := media.DefaultResampler(src.SampleRate, pcm.SampleRate)
	return func(packet media.MediaPacket) ([]media.MediaPacket, error) {
		audioPacket, ok := packet.(*media.AudioPacket)
		if !ok {
			return []media.MediaPacket{packet}, nil
		}
		if _, err := res.Write(audioPacket.Payload); err != nil {
			return nil, err
		}
		data := res.Samples()
		if len(data) == 0 {
			return nil, nil
		}
		// Return a new AudioPacket instead of mutating the input — callers may
		// reuse the same packet across calls (e.g. benchmarks, transport loops),
		// and mutating Payload would cause the resampler to re-process already
		// converted data on the next call, leading to unbounded buffer growth.
		return []media.MediaPacket{&media.AudioPacket{
			PlayID:         audioPacket.PlayID,
			Sequence:       audioPacket.Sequence,
			Payload:        data,
			RTPSamples:     audioPacket.RTPSamples,
			IsFirstPacket:  audioPacket.IsFirstPacket,
			IsEndPacket:    audioPacket.IsEndPacket,
			IsSynthesized:  audioPacket.IsSynthesized,
			IsSilence:      audioPacket.IsSilence,
			SourceText:     audioPacket.SourceText,
		}}, nil
	}
}
