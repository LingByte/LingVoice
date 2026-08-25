// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package media

import (
	"fmt"
	"testing"
)

// --- helpers ---

// makePCM generates `ms` milliseconds of 16-bit mono PCM at `sampleRate`.
func makePCM(sampleRate int, ms int) []byte {
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

// --- Resampler benchmarks ---

func BenchmarkResamplePCM_8kTo16k(b *testing.B) {
	in := makePCM(8000, 20) // 20ms @ 8k
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ResamplePCM(in, 8000, 16000)
	}
}

func BenchmarkResamplePCM_8kTo16k_Concurrent(b *testing.B) {
	in := makePCM(8000, 20)
	for _, g := range []int{1, 4, 16, 64} {
		b.Run(fmt.Sprintf("g=%d", g), func(b *testing.B) {
			b.ReportAllocs()
			b.SetParallelism(g)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_, _ = ResamplePCM(in, 8000, 16000)
				}
			})
		})
	}
}

func BenchmarkStreamResampler_8kTo16k_Reuse(b *testing.B) {
	rs := NewStreamResampler(8000, 16000)
	in := makePCM(8000, 20)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = rs.Resample(in)
	}
}

func BenchmarkStreamResampler_8kTo16k_Concurrent(b *testing.B) {
	in := makePCM(8000, 20)
	for _, g := range []int{1, 4, 16, 64} {
		b.Run(fmt.Sprintf("g=%d", g), func(b *testing.B) {
			b.ReportAllocs()
			b.SetParallelism(g)
			b.RunParallel(func(pb *testing.PB) {
				rs := NewStreamResampler(8000, 16000)
				for pb.Next() {
					_, _ = rs.Resample(in)
				}
			})
		})
	}
}

// --- Lowpass filter benchmarks ---

func BenchmarkLowPassFIR_16kTo8k(b *testing.B) {
	f := NewDownsamplingLowPass(16000, 8000)
	in := make([]int16, 320) // 20ms @ 16k
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.filter(in)
	}
}

func BenchmarkLowPassFIR_16kTo8k_Concurrent(b *testing.B) {
	in := make([]int16, 320)
	for _, g := range []int{1, 4, 16, 64} {
		b.Run(fmt.Sprintf("g=%d", g), func(b *testing.B) {
			b.ReportAllocs()
			b.SetParallelism(g)
			b.RunParallel(func(pb *testing.PB) {
				f := NewDownsamplingLowPass(16000, 8000)
				for pb.Next() {
					f.filter(in)
				}
			})
		})
	}
}

// --- Packet processing benchmarks ---
// The old EventBus has been removed entirely. Packet processing is now
// synchronous via processPacketDirect. State/error events are also
// synchronous via EmitState/CauseError.

// BenchmarkProcessPacketDirect measures the synchronous direct path:
// EmitPacket → processPacketDirect → processor chain → trySendPacket.
func BenchmarkProcessPacketDirect(b *testing.B) {
	s := NewDefaultSession()
	in := newMockTransport("pcmu", 8000)
	out := newMockTransport("pcmu", 8000)
	s.Input(in)
	s.Output(out)
	s.setupOutputRouter()

	pkt := &AudioPacket{Payload: makePCM(8000, 20)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.EmitPacket(s, pkt)
	}
}

// BenchmarkProcessPacketDirect_Concurrent measures the direct path under
// concurrent goroutines (simulating multiple input transports).
func BenchmarkProcessPacketDirect_Concurrent(b *testing.B) {
	for _, g := range concurrencyLevels {
		b.Run(fmt.Sprintf("g=%d", g), func(b *testing.B) {
			s := NewDefaultSession()
			out := newMockTransport("pcmu", 8000)
			s.Output(out)
			s.setupOutputRouter()

			pkt := &AudioPacket{Payload: makePCM(8000, 20)}
			b.ReportAllocs()
			b.SetParallelism(g)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					s.EmitPacket(s, pkt)
				}
			})
		})
	}
}

// --- Cache benchmarks (BuildKey only — disk I/O is not meaningful for concurrency test) ---

func BenchmarkMediaCache_BuildKey(b *testing.B) {
	c := &LocalMediaCache{Disabled: true}
	params := []string{"voice", "session-123", "tts", "hello"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.BuildKey(params...)
	}
}

func BenchmarkMediaCache_BuildKey_Concurrent(b *testing.B) {
	c := &LocalMediaCache{Disabled: true}
	params := []string{"voice", "session-123", "tts", "hello"}
	for _, g := range []int{1, 4, 16, 64} {
		b.Run(fmt.Sprintf("g=%d", g), func(b *testing.B) {
			b.ReportAllocs()
			b.SetParallelism(g)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					c.BuildKey(params...)
				}
			})
		})
	}
}
