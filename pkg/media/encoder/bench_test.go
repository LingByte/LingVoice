// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package encoder

import (
	"fmt"
	"testing"

	"github.com/LingByte/LingVoice/pkg/media"
)

// --- helpers ---

func makePCMSamples(sampleRate int, ms int) []byte {
	n := sampleRate * ms / 1000
	out := make([]byte, n*2)
	for i := 0; i < n; i++ {
		v := int16(i % 32767)
		out[i*2] = byte(v & 0xFF)
		out[i*2+1] = byte((v >> 8) & 0xFF)
	}
	return out
}

var concurrencyLevels = []int{1, 4, 16, 64}

// --- G.711 PCMA ---

func BenchmarkPCMA_Encode(b *testing.B) {
	pcm := makePCMSamples(8000, 20) // 160 samples
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Pcm2pcma(pcm)
	}
}

func BenchmarkPCMA_Encode_Concurrent(b *testing.B) {
	pcm := makePCMSamples(8000, 20)
	for _, g := range concurrencyLevels {
		b.Run(fmt.Sprintf("g=%d", g), func(b *testing.B) {
			b.ReportAllocs()
			b.SetParallelism(g)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_, _ = Pcm2pcma(pcm)
				}
			})
		})
	}
}

func BenchmarkPCMA_Decode(b *testing.B) {
	pcm := makePCMSamples(8000, 20)
	alaw, _ := Pcm2pcma(pcm)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pcma2pcm(alaw)
	}
}

func BenchmarkPCMA_Decode_Concurrent(b *testing.B) {
	pcm := makePCMSamples(8000, 20)
	alaw, _ := Pcm2pcma(pcm)
	for _, g := range concurrencyLevels {
		b.Run(fmt.Sprintf("g=%d", g), func(b *testing.B) {
			b.ReportAllocs()
			b.SetParallelism(g)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_, _ = pcma2pcm(alaw)
				}
			})
		})
	}
}

// --- G.711 PCMU ---

func BenchmarkPCMU_Encode(b *testing.B) {
	pcm := makePCMSamples(8000, 20)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pcm2pcmu(pcm)
	}
}

func BenchmarkPCMU_Encode_Concurrent(b *testing.B) {
	pcm := makePCMSamples(8000, 20)
	for _, g := range concurrencyLevels {
		b.Run(fmt.Sprintf("g=%d", g), func(b *testing.B) {
			b.ReportAllocs()
			b.SetParallelism(g)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_, _ = pcm2pcmu(pcm)
				}
			})
		})
	}
}

func BenchmarkPCMU_Decode(b *testing.B) {
	pcm := makePCMSamples(8000, 20)
	ulaw, _ := pcm2pcmu(pcm)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pcmu2pcm(ulaw)
	}
}

func BenchmarkPCMU_Decode_Concurrent(b *testing.B) {
	pcm := makePCMSamples(8000, 20)
	ulaw, _ := pcm2pcmu(pcm)
	for _, g := range concurrencyLevels {
		b.Run(fmt.Sprintf("g=%d", g), func(b *testing.B) {
			b.ReportAllocs()
			b.SetParallelism(g)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_, _ = pcmu2pcm(ulaw)
				}
			})
		})
	}
}

// --- G.722 ---

func BenchmarkG722_Encode(b *testing.B) {
	enc := NewG722Encoder(64000, 0)
	pcm := makePCMSamples(16000, 20) // 320 samples @ 16k
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enc.Encode(pcm)
	}
}

func BenchmarkG722_Encode_Concurrent(b *testing.B) {
	pcm := makePCMSamples(16000, 20)
	for _, g := range concurrencyLevels {
		b.Run(fmt.Sprintf("g=%d", g), func(b *testing.B) {
			b.ReportAllocs()
			b.SetParallelism(g)
			b.RunParallel(func(pb *testing.PB) {
				enc := NewG722Encoder(64000, 0)
				for pb.Next() {
					enc.Encode(pcm)
				}
			})
		})
	}
}

func BenchmarkG722_Decode(b *testing.B) {
	dec := NewG722Decoder(64000, 0)
	enc := NewG722Encoder(64000, 0)
	pcm := makePCMSamples(16000, 20)
	g722data := enc.Encode(pcm)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dec.Decode(g722data)
	}
}

func BenchmarkG722_Decode_Concurrent(b *testing.B) {
	enc := NewG722Encoder(64000, 0)
	pcm := makePCMSamples(16000, 20)
	g722data := enc.Encode(pcm)
	for _, g := range concurrencyLevels {
		b.Run(fmt.Sprintf("g=%d", g), func(b *testing.B) {
			b.ReportAllocs()
			b.SetParallelism(g)
			b.RunParallel(func(pb *testing.PB) {
				dec := NewG722Decoder(64000, 0)
				for pb.Next() {
					dec.Decode(g722data)
				}
			})
		})
	}
}

// --- PCM passthrough via factory ---

func BenchmarkPcmToPcm_SameRate(b *testing.B) {
	src := media.CodecConfig{Codec: "pcm", SampleRate: 16000}
	pcmCfg := media.CodecConfig{Codec: "pcm", SampleRate: 16000}
	enc := PcmToPcm(src, pcmCfg)
	pkt := &media.AudioPacket{Payload: makePCMSamples(16000, 20)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = enc(pkt)
	}
}

func BenchmarkPcmToPcm_WithResample(b *testing.B) {
	src := media.CodecConfig{Codec: "pcm", SampleRate: 8000}
	pcmCfg := media.CodecConfig{Codec: "pcm", SampleRate: 16000}
	enc := PcmToPcm(src, pcmCfg)
	pkt := &media.AudioPacket{Payload: makePCMSamples(8000, 20)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = enc(pkt)
	}
}

func BenchmarkPcmToPcm_WithResample_Concurrent(b *testing.B) {
	src := media.CodecConfig{Codec: "pcm", SampleRate: 8000}
	pcmCfg := media.CodecConfig{Codec: "pcm", SampleRate: 16000}
	pkt := &media.AudioPacket{Payload: makePCMSamples(8000, 20)}
	for _, g := range concurrencyLevels {
		b.Run(fmt.Sprintf("g=%d", g), func(b *testing.B) {
			b.ReportAllocs()
			b.SetParallelism(g)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					enc := PcmToPcm(src, pcmCfg)
					_, _ = enc(pkt)
				}
			})
		})
	}
}

// --- Factory create + invoke (full pipeline) ---

func BenchmarkFactory_PCMA_RoundTrip(b *testing.B) {
	src := media.CodecConfig{Codec: "pcma", SampleRate: 8000}
	pcmCfg := media.CodecConfig{Codec: "pcm", SampleRate: 16000}
	encFn, _ := CreateEncode(src, pcmCfg)
	decFn, _ := CreateDecode(src, pcmCfg)
	pkt := &media.AudioPacket{Payload: makePCMSamples(16000, 20)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encoded, _ := encFn(pkt)
		for _, ep := range encoded {
			_, _ = decFn(ep)
		}
	}
}

func BenchmarkFactory_PCMA_RoundTrip_Concurrent(b *testing.B) {
	src := media.CodecConfig{Codec: "pcma", SampleRate: 8000}
	pcmCfg := media.CodecConfig{Codec: "pcm", SampleRate: 16000}
	pkt := &media.AudioPacket{Payload: makePCMSamples(16000, 20)}
	for _, g := range concurrencyLevels {
		b.Run(fmt.Sprintf("g=%d", g), func(b *testing.B) {
			b.ReportAllocs()
			b.SetParallelism(g)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					encFn, _ := CreateEncode(src, pcmCfg)
					decFn, _ := CreateDecode(src, pcmCfg)
					encoded, _ := encFn(pkt)
					for _, ep := range encoded {
						_, _ = decFn(ep)
					}
				}
			})
		})
	}
}
