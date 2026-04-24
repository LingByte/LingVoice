package encoder

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

import (
	media2 "github.com/LingByte/LingVoice/pkg/media"
)

func createPCMADecode(src, pcm media2.CodecConfig) media2.EncoderFunc {
	sourceSampleRate := src.SampleRate
	if sourceSampleRate == 0 {
		sourceSampleRate = 8000
	}
	res := media2.DefaultResampler(sourceSampleRate, pcm.SampleRate)
	return func(packet media2.MediaPacket) ([]media2.MediaPacket, error) {
		audioPacket, ok := packet.(*media2.AudioPacket)
		if !ok {
			return []media2.MediaPacket{packet}, nil
		}
		data, err := pcma2pcm(audioPacket.Payload)
		if err != nil {
			return nil, err
		}
		if _, err = res.Write(data); err != nil {
			return nil, err
		}
		data = res.Samples()
		if data == nil {
			return nil, nil
		}
		audioPacket.Payload = data
		return []media2.MediaPacket{audioPacket}, nil
	}
}

func createPCMAEncode(src, pcm media2.CodecConfig) media2.EncoderFunc {
	// Use configured target sample rate, if not set use PCMA standard sample rate 8000Hz
	targetSampleRate := src.SampleRate
	if targetSampleRate == 0 {
		targetSampleRate = 8000 // PCMA standard sample rate
	}
	res := media2.DefaultResampler(pcm.SampleRate, targetSampleRate)

	return func(packet media2.MediaPacket) ([]media2.MediaPacket, error) {
		audioPacket, ok := packet.(*media2.AudioPacket)
		if !ok {
			return []media2.MediaPacket{packet}, nil
		}
		if _, err := res.Write(audioPacket.Payload); err != nil {
			return nil, err
		}
		data := res.Samples()
		if data == nil {
			return nil, nil
		}
		data, err := Pcm2pcma(data)
		if err != nil {
			return nil, err
		}
		return splitFrames(data, &src), nil
	}
}
