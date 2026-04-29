package encoder

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

import (
	"github.com/LingByte/LingVoice/pkg/utils/media"
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
		audioPacket.Payload = data
		return []media.MediaPacket{audioPacket}, nil
	}
}
