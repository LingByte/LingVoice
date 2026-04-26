package encoder

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

import (
	media2 "github.com/LingByte/LingVoice/pkg/media"
)

func PcmToPcm(src, pcm media2.CodecConfig) media2.EncoderFunc {
	res := media2.DefaultResampler(src.SampleRate, pcm.SampleRate)
	return func(packet media2.MediaPacket) ([]media2.MediaPacket, error) {
		audioPacket, ok := packet.(*media2.AudioPacket)
		if !ok {
			return []media2.MediaPacket{packet}, nil
		}
		if _, err := res.Write(audioPacket.Payload); err != nil {
			return nil, err
		}
		data := res.Samples()
		if len(data) == 0 {
			return nil, nil
		}
		audioPacket.Payload = data
		return []media2.MediaPacket{audioPacket}, nil
	}
}
