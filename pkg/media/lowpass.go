// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package media

// Anti-aliasing low-pass filter for downsampling.
//
// Why this exists: our resampler used to perform cubic / linear
// interpolation directly on the input samples without first
// removing energy above the destination Nyquist frequency. That's
// fine for upsampling but mathematically broken for downsampling —
// any energy in the input above (target_rate / 2) folds back into
// the audible band as aliasing. In production this manifested as
// a buzz / "electrical hum" overlay on 16k→8k legs (incident on
// 2026-04 — see docs/sip_gap_analysis.md §3 row "音频抗混叠 LPF").
//
// Design choices:
//
//   - **Windowed-sinc FIR** with a Hamming window. Linear phase,
//     no ringing surprises, fixed-cost per sample. 31 taps gives
//     ~60 dB stopband which is plenty for 8 kHz speech.
//   - **Cutoff = 0.45 × (target_rate / source_rate)** (normalised
//     to source rate). 0.45 leaves a 10 % transition band to roll
//     off cleanly before the new Nyquist.
//   - **Stateful between Write() chunks**: keeps the last (N-1)
//     input samples so consecutive frames don't introduce a
//     filter-edge transient at every chunk boundary.
//   - **No SIMD / no asm**: 31 muls/sample at 16 kHz = 496 K op/s
//     per leg; well under any hot-loop budget. Cache-friendly
//     because the tap array is 248 bytes.
//
// We intentionally DON'T do polyphase decimation here — the
// interpolation step downstream handles arbitrary ratios already,
// and stitching polyphase + arbitrary cubic interp would double
// the code surface for marginal CPU win on calls we already encode
// in real time.

import (
	"math"
	"unsafe"
)

const lowPassNTaps = 31

// lowPassFIR is a streaming FIR low-pass filter used as the
// anti-aliasing pre-stage when downsampling PCM16. Reusable across
// Write() calls — the history field carries the tail of the
// previous chunk so chunk boundaries don't introduce discontinuities.
type lowPassFIR struct {
	taps    []float64
	history []int16
	outBuf  []int16 // reusable output buffer, grows as needed
}

// NewDownsamplingLowPass returns a filter whose passband ends at
// roughly 0.45 × targetRate (normalised to sourceRate). Returns nil
// when no filtering is needed (sourceRate <= targetRate, i.e.
// upsampling or no-op — there's no aliasing to worry about there).
func NewDownsamplingLowPass(sourceRate, targetRate int) *lowPassFIR {
	if sourceRate <= 0 || targetRate <= 0 || sourceRate <= targetRate {
		return nil
	}
	// Normalised cutoff in cycles/sample at the SOURCE rate.
	// 0.45 instead of 0.5 leaves ~10 % transition margin.
	cutoff := 0.45 * float64(targetRate) / float64(sourceRate)
	return designFIRLowPass(cutoff)
}

// NewUpsamplingAntiImagingLowPass returns the *post*-interpolation
// FIR that suppresses spectral images produced by upsampling.
//
// Math note: when you upsample from sourceRate to targetRate (Y > X),
// linear / cubic interpolation is mathematically equivalent to
// zero-stuffing followed by a (mediocre) low-pass. Anything above
// the ORIGINAL Nyquist (X/2) in the new spectrum is an alias-image
// of the source band; a proper LPF at cutoff = X/2 (in the target
// rate) cleans them up.
//
// Practical note: in our common path (8 kHz PSTN → 16 kHz AI), the
// source is already band-limited to ~3.4 kHz by the telephony
// network, so this filter is mostly a no-op. We still apply it for
// correctness and to handle the 16 kHz → 48 kHz Opus path where
// images are real.
//
// Returns nil for downsampling / no-op cases.
func NewUpsamplingAntiImagingLowPass(sourceRate, targetRate int) *lowPassFIR {
	if sourceRate <= 0 || targetRate <= 0 || targetRate <= sourceRate {
		return nil
	}
	// Cutoff at original Nyquist, normalised to the target rate.
	// 0.45× margin matches the downsample design for symmetric
	// transition-band behaviour.
	cutoff := 0.45 * float64(sourceRate) / float64(targetRate)
	return designFIRLowPass(cutoff)
}

// designFIRLowPass is the shared Hamming-windowed sinc designer.
// `cutoff` is in normalised cycles/sample at whatever rate the
// filter will be APPLIED at (source rate for downsample pre-filter,
// target rate for upsample post-filter).
func designFIRLowPass(cutoff float64) *lowPassFIR {

	taps := make([]float64, lowPassNTaps)
	mid := float64(lowPassNTaps-1) / 2.0
	sum := 0.0
	for i := 0; i < lowPassNTaps; i++ {
		n := float64(i) - mid
		// Sinc with mid-tap defined as the limit (= 2*cutoff).
		var s float64
		if n == 0 {
			s = 2 * cutoff
		} else {
			s = math.Sin(2*math.Pi*cutoff*n) / (math.Pi * n)
		}
		// Hamming window — better stopband than rectangular without
		// the ringing of a Blackman.
		w := 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(lowPassNTaps-1))
		taps[i] = s * w
		sum += taps[i]
	}
	// Normalise to unity DC gain. Without this, slight design errors
	// would drift call volume up or down over long sessions.
	if sum != 0 {
		for i := range taps {
			taps[i] /= sum
		}
	}
	return &lowPassFIR{
		taps:    taps,
		history: make([]int16, lowPassNTaps-1),
		outBuf:  make([]int16, 0, 320), // will grow as needed
	}
}

// filter convolves in[] with the tap set and returns a slice of the
// same length. Causal delay is (N-1)/2 = 15 samples for a 31-tap
// filter — about 0.94 ms at 16 kHz, inaudible in conversation.
//
// Zero-allocation hot path: the output buffer (outBuf) is reused across
// calls, and the history is indexed directly instead of concatenating
// into a temporary `extended` slice. The returned slice aliases the
// filter's internal outBuf — callers must copy or consume before the
// next filter() call.
func (f *lowPassFIR) filter(in []int16) []int16 {
	if f == nil || len(in) == 0 {
		return in
	}
	histN := len(f.history)
	nTaps := len(f.taps)

	// Reuse output buffer — grows only if a larger chunk arrives.
	if cap(f.outBuf) < len(in) {
		f.outBuf = make([]int16, len(in))
	} else {
		f.outBuf = f.outBuf[:len(in)]
	}
	out := f.outBuf

	for i := 0; i < len(in); i++ {
		var acc float64
		for k := 0; k < nTaps; k++ {
			pos := i + k
			var sample int16
			if pos < histN {
				sample = f.history[pos]
			} else {
				sample = in[pos-histN]
			}
			acc += f.taps[k] * float64(sample)
		}
		// Saturate to int16 range. This is hit on rare loud peaks
		// (transient impulses near full scale); preferable to wrap-
		// around which would create a click.
		if acc > 32767 {
			acc = 32767
		} else if acc < -32768 {
			acc = -32768
		}
		out[i] = int16(acc)
	}

	// Update history: keep the last (N-1) samples of (history + input).
	// This is an O(histN) copy — histN is 30, negligible.
	if len(in) >= histN {
		copy(f.history, in[len(in)-histN:])
	} else {
		// Shift history left by len(in), then append the new samples.
		copy(f.history, f.history[len(in):])
		copy(f.history[histN-len(in):], in)
	}

	return out
}

// pcm16BytesToSamples / samplesToPCM16Bytes convert between the
// little-endian byte form used on the resampler boundary and the
// in-memory int16 form the FIR operates on. Both use unsafe pointer
// reinterpretation for zero-copy, zero-allocation conversion.
//
// The returned slice shares memory with the input — callers must not
// retain it beyond the input's lifetime (same semantics as a Rust
// zero-copy view). This is safe because:
//   - pcm16BytesToSamples: we only READ from the returned int16 slice.
//   - samplesToPCM16Bytes: we only WRITE the result to a file/socket.
//
// On little-endian platforms (all currently supported Go targets) the
// in-memory int16 layout is identical to the LE byte representation,
// so the reinterpretation is exact.
func pcm16BytesToSamples(b []byte) []int16 {
	n := len(b) / 2
	if n == 0 {
		return nil
	}
	// Fast path: if the byte slice is 2-byte aligned (which it always is
	// when allocated directly via make([]byte, ...) and not a sub-slice
	// at an odd offset), reinterpret as int16 with zero copy.
	if uintptr(unsafe.Pointer(&b[0]))%unsafe.Alignof(int16(0)) == 0 {
		return unsafe.Slice((*int16)(unsafe.Pointer(&b[0])), n)
	}
	// Fallback: copy for unaligned data (rare — only from odd-offset sub-slices)
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(b[i*2]) | int16(b[i*2+1])<<8
	}
	return out
}

func samplesToPCM16Bytes(s []int16) []byte {
	if len(s) == 0 {
		return nil
	}
	// int16 slices are always 2-byte aligned, so this reinterpretation is safe.
	return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), len(s)*2)
}

// DCBlockHPF is a one-pole IIR high-pass that removes DC offset and
// sub-audible (< ~30 Hz at 8 kHz) rumble that PSTN trunks often
// inject. NOT related to resampling: this is a separate quality
// stage that should run on any inbound telephony audio.
//
// Transfer function (RFC-less, classic DSP):
//
//	y[n] = x[n] - x[n-1] + R * y[n-1],     R ≈ 0.995 at 8 kHz
//
// The pole at z = R places the -3 dB corner at roughly
// (1-R) * sampleRate / (2π) ≈ 30 Hz with R = 0.995, sr = 8000.
// That's well below the 300 Hz lower edge of the telephony band
// so it won't audibly thin the speech.
//
// State (xPrev, yPrev) persists across Process calls so chunk
// boundaries don't introduce clicks.
type DCBlockHPF struct {
	r     float64
	xPrev float64
	yPrev float64
}

// NewDCBlockHPF builds a DC blocker tuned for the given sample
// rate. The pole coefficient is computed so the -3 dB corner sits
// at ~30 Hz regardless of rate (8k / 16k / 48k all OK).
func NewDCBlockHPF(sampleRate int) *DCBlockHPF {
	if sampleRate <= 0 {
		sampleRate = 8000
	}
	// R chosen so cornerHz ≈ 30 Hz: (1-R) ≈ 2π * cornerHz / sr.
	const cornerHz = 30.0
	r := 1.0 - 2*math.Pi*cornerHz/float64(sampleRate)
	if r < 0 {
		r = 0
	}
	if r > 0.999 {
		r = 0.999
	}
	return &DCBlockHPF{r: r}
}

// Process applies the filter in place and returns the same slice
// for chaining. Saturates to int16 range on overshoot (rare —
// the HPF can only attenuate, never amplify, so saturation is a
// numerical-edge concern only).
func (h *DCBlockHPF) Process(in []int16) []int16 {
	if h == nil || len(in) == 0 {
		return in
	}
	for i, v := range in {
		x := float64(v)
		y := x - h.xPrev + h.r*h.yPrev
		h.xPrev = x
		h.yPrev = y
		if y > 32767 {
			y = 32767
		} else if y < -32768 {
			y = -32768
		}
		in[i] = int16(y)
	}
	return in
}

// Reset clears filter state. Call on dialog reset (re-INVITE,
// transfer) to avoid carrying the previous leg's offset bias.
func (h *DCBlockHPF) Reset() {
	if h == nil {
		return
	}
	h.xPrev = 0
	h.yPrev = 0
}
