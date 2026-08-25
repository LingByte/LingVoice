package encoder

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"github.com/LingByte/LingVoice/pkg/media"
)

// G.722 constants
const (
	G722_RATE_DEFAULT = 64000
	G722_DEFAULT      = 0
)

// g722QuantTable is a precomputed lookup table for the quantize()
// function. Index = abs(sample) (0..32767), value = quantization
// level (0..11). Replaces the 12-branch if-else chain on the encode
// hot path with a single array index.
var g722QuantTable [32768]int

// g722DequantLevels is the fixed dequantization level table used by
// dequantize(). Declared at package level to avoid allocating a new
// slice on every call.
var g722DequantLevels = [16]int16{8, 24, 40, 56, 80, 112, 160, 224, 320, 448, 640, 896, 1280, 1792, 2560, 3584}

func init() {
	thresholds := [11]int{16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384}
	for i := 0; i < 32768; i++ {
		q := 11
		for j, t := range thresholds {
			if i < t {
				q = j
				break
			}
		}
		g722QuantTable[i] = q
	}
}

// G.722 band structures
type G722Band0 struct {
	a  [2]int16
	b  [6]int16
	d  [7]int16
	s  [7]int16
	sz int16
	sp int16
}

type G722Band1 struct {
	a  [2]int16
	b  [6]int16
	d  [7]int16
	s  [7]int16
	sz int16
	sp int16
}

type G722Band2 struct {
	a  [2]int16
	b  [6]int16
	d  [7]int16
	s  [7]int16
	sz int16
	sp int16
}

// G722Encoder G.722 encoder
type G722Encoder struct {
	band0 *G722Band0
	band1 *G722Band1
	band2 *G722Band2
	rate  int
}

// G722Decoder G.722 decoder
type G722Decoder struct {
	band0 *G722Band0
	band1 *G722Band1
	band2 *G722Band2
	rate  int
}

func createG722Decode(src, pcm media.CodecConfig) media.EncoderFunc {
	// Use configured sample rate, if not set use G.722 standard sample rate 16000Hz
	sourceSampleRate := src.SampleRate
	if sourceSampleRate == 0 {
		sourceSampleRate = 16000 // G.722 standard sample rate
	}
	res := media.DefaultResampler(sourceSampleRate, pcm.SampleRate)
	dec := NewG722Decoder(G722_RATE_DEFAULT, G722_DEFAULT)

	return func(packet media.MediaPacket) ([]media.MediaPacket, error) {
		audioPacket, ok := packet.(*media.AudioPacket)
		if !ok {
			return []media.MediaPacket{packet}, nil
		}
		decodedData := dec.Decode(audioPacket.Payload)

		if _, err := res.Write(decodedData); err != nil {
			return nil, err
		}
		data := res.Samples()
		if data == nil {
			return nil, nil
		}
		audioPacket.Payload = data
		return []media.MediaPacket{audioPacket}, nil
	}
}

func createG722Encode(src, pcm media.CodecConfig) media.EncoderFunc {
	// Use configured target sample rate, if not set use G.722 standard sample rate 16000Hz
	targetSampleRate := src.SampleRate
	if targetSampleRate == 0 {
		targetSampleRate = 16000 // G.722 standard sample rate
	}
	res := media.DefaultResampler(pcm.SampleRate, targetSampleRate)
	enc := NewG722Encoder(G722_RATE_DEFAULT, G722_DEFAULT)
	return func(packet media.MediaPacket) ([]media.MediaPacket, error) {
		audioPacket, ok := packet.(*media.AudioPacket)
		if !ok {
			return []media.MediaPacket{packet}, nil
		}
		if _, err := res.Write(audioPacket.Payload); err != nil {
			return nil, err
		}
		data := res.Samples()
		if data == nil {
			return nil, nil
		}
		encodedData := enc.Encode(data)
		audioPacket.Payload = encodedData
		return []media.MediaPacket{packet}, nil
	}
}

// NewG722Encoder creates new G.722 encoder
func NewG722Encoder(rate, mode int) *G722Encoder {
	encoder := &G722Encoder{
		rate:  rate,
		band0: &G722Band0{},
		band1: &G722Band1{},
		band2: &G722Band2{},
	}
	encoder.init()
	return encoder
}

func (e *G722Encoder) init() {
	for i := range e.band0.a {
		e.band0.a[i] = 0
		e.band0.b[i] = 0
		e.band0.d[i] = 0
		e.band0.s[i] = 0
	}
	for i := range e.band1.a {
		e.band1.a[i] = 0
		e.band1.b[i] = 0
		e.band1.d[i] = 0
		e.band1.s[i] = 0
	}
	for i := range e.band2.a {
		e.band2.a[i] = 0
		e.band2.b[i] = 0
		e.band2.d[i] = 0
		e.band2.s[i] = 0
	}
}

func (e *G722Encoder) Encode(pcmData []byte) []byte {
	if len(pcmData) == 0 {
		return nil
	}
	if len(pcmData)%2 != 0 {
		pcmData = pcmData[:len(pcmData)-1]
	}
	samples := len(pcmData) / 2
	output := make([]byte, samples/2)
	for i := 0; i < samples; i += 2 {
		sample1 := int16(pcmData[i*2]) | int16(pcmData[i*2+1])<<8
		sample2 := int16(pcmData[(i+1)*2]) | int16(pcmData[(i+1)*2+1])<<8
		encoded := e.encodeSamples(sample1, sample2)
		output[i/2] = encoded
	}
	return output
}

func (e *G722Encoder) encodeSamples(s1, s2 int16) byte {
	q1 := e.quantize(s1)
	q2 := e.quantize(s2)
	return byte((q1 & 0x0F) | ((q2 & 0x0F) << 4))
}

func (e *G722Encoder) quantize(sample int16) int {
	abs := int(sample)
	if abs < 0 {
		abs = -abs
	}
	if abs >= 32768 {
		return 11
	}
	return g722QuantTable[abs]
}

// NewG722Decoder creates new G.722 decoder
func NewG722Decoder(rate, mode int) *G722Decoder {
	decoder := &G722Decoder{
		rate:  rate,
		band0: &G722Band0{},
		band1: &G722Band1{},
		band2: &G722Band2{},
	}
	decoder.init()
	return decoder
}

func (d *G722Decoder) init() {
	for i := range d.band0.a {
		d.band0.a[i] = 0
		d.band0.b[i] = 0
		d.band0.d[i] = 0
		d.band0.s[i] = 0
	}
	for i := range d.band1.a {
		d.band1.a[i] = 0
		d.band1.b[i] = 0
		d.band1.d[i] = 0
		d.band1.s[i] = 0
	}
	for i := range d.band2.a {
		d.band2.a[i] = 0
		d.band2.b[i] = 0
		d.band2.d[i] = 0
		d.band2.s[i] = 0
	}
}

func (d *G722Decoder) Decode(g722Data []byte) []byte {
	if len(g722Data) == 0 {
		return nil
	}
	output := make([]byte, len(g722Data)*4)
	for i, encoded := range g722Data {
		sample1, sample2 := d.decodeSamples(encoded)
		output[i*4] = byte(sample1 & 0xFF)
		output[i*4+1] = byte((sample1 >> 8) & 0xFF)
		output[i*4+2] = byte(sample2 & 0xFF)
		output[i*4+3] = byte((sample2 >> 8) & 0xFF)
	}
	return output
}

func (d *G722Decoder) decodeSamples(encoded byte) (int16, int16) {
	q1 := int(encoded & 0x0F)
	q2 := int((encoded >> 4) & 0x0F)
	sample1 := d.dequantize(q1)
	sample2 := d.dequantize(q2)
	return sample1, sample2
}

func (d *G722Decoder) dequantize(q int) int16 {
	if q >= len(g722DequantLevels) {
		q = len(g722DequantLevels) - 1
	}
	noise := int16((q % 3) - 1)
	return g722DequantLevels[q] + noise
}
