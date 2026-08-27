package dash

import (
	"encoding/binary"
)

// BuildInitSegment 构建 fMP4 init segment (ftyp + moov)。
//
// 参数:
//   - videoCodec: 视频编解码器 ("avc1" / "hvc1"), 空字符串表示无视频
//   - width, height: 视频分辨率
//   - audioCodec: 音频编解码器 ("mp4a"), 空字符串表示无音频
//   - sampleRate, channels: 音频采样率和声道数
//
// 返回的 init segment 可用于 DASH 的 initialization segment。
func BuildInitSegment(videoCodec string, width, height uint32, audioCodec string, sampleRate, channels uint32) []byte {
	var out []byte

	// ftyp box
	out = append(out, box("ftyp", ftypData())...)

	// moov box
	var moovData []byte
	moovData = append(moovData, box("mvhd", mvhdData())...)

	trackID := uint32(1)
	if videoCodec != "" {
		moovData = append(moovData, buildVideoTrak(trackID, videoCodec, width, height)...)
		trackID++
	}
	if audioCodec != "" {
		moovData = append(moovData, buildAudioTrak(trackID, audioCodec, sampleRate, channels)...)
		trackID++
	}

	// mvex (movie extends header) with trex for each track
	var mvexData []byte
	for tid := uint32(1); tid < trackID; tid++ {
		mvexData = append(mvexData, trexData(tid)...)
	}
	moovData = append(moovData, box("mvex", mvexData)...)

	out = append(out, box("moov", moovData)...)
	return out
}

// ─── box 构建工具 ──────────────────────────────────────────────────────────

// box 构建 ISO BMFF box: 4 字节 size + 4 字节 type + data
func box(boxType string, data []byte) []byte {
	size := uint32(8 + len(data))
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b[0:4], size)
	copy(b[4:8], []byte(boxType))
	return append(b, data...)
}

// fullBox 构建 fullbox: 4 字节 size + 4 字节 type + 1 字节 version + 3 字节 flags + data
func fullBox(boxType string, version byte, flags uint32, data []byte) []byte {
	inner := make([]byte, 4+len(data))
	inner[0] = version
	inner[1] = byte(flags >> 16)
	inner[2] = byte(flags >> 8)
	inner[3] = byte(flags)
	copy(inner[4:], data)
	return box(boxType, inner)
}

// u32 返回 4 字节大端 uint32
func u32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

// u16 返回 2 字节大端 uint16
func u16(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

// ─── ftyp ──────────────────────────────────────────────────────────────────

func ftypData() []byte {
	var d []byte
	d = append(d, []byte("iso5")...)        // major_brand
	d = append(d, u32(0x200)...)            // minor_version
	d = append(d, []byte("iso5")...)        // compatible_brands
	d = append(d, []byte("avc1")...)
	d = append(d, []byte("mp42")...)
	return d
}

// ─── mvhd ──────────────────────────────────────────────────────────────────

func mvhdData() []byte {
	var d []byte
	d = append(d, u32(0)...)        // creation_time
	d = append(d, u32(0)...)        // modification_time
	d = append(d, u32(1000)...)     // timescale (ms)
	d = append(d, u32(0)...)        // duration
	d = append(d, u32(0x00010000)...) // rate = 1.0 (fixed 16.16)
	d = append(d, u16(0x0100)...)   // volume = 1.0 (fixed 8.8)
	d = append(d, make([]byte, 10)...) // reserved
	d = append(d, identityMatrix()...) // matrix (36 bytes)
	d = append(d, make([]byte, 24)...) // pre_defined
	d = append(d, u32(3)...)        // next_track_ID
	return fullBox("mvhd", 0, 0, d)
}

// identityMatrix 返回 ISO BMFF 单位矩阵 (36 字节)。
// 1.0  0.0  0.0
// 0.0  1.0  0.0
// 0.0  0.0  1.0
// 每个值是 16.16 fixed point (前 6 个) 或 2.30 fixed point (后 3 个)。
func identityMatrix() []byte {
	var m []byte
	m = append(m, u32(0x00010000)...) // 1.0
	m = append(m, u32(0)...)          // 0.0
	m = append(m, u32(0)...)          // 0.0
	m = append(m, u32(0)...)          // 0.0
	m = append(m, u32(0x00010000)...) // 1.0
	m = append(m, u32(0)...)          // 0.0
	m = append(m, u32(0)...)          // 0.0 (2.30)
	m = append(m, u32(0)...)          // 0.0
	m = append(m, u32(0x40000000)...) // 1.0 (2.30)
	return m
}

// ─── trak (video) ──────────────────────────────────────────────────────────

func buildVideoTrak(trackID uint32, codec string, width, height uint32) []byte {
	var trakData []byte

	// tkhd
	trakData = append(trakData, tkhdData(trackID, width, height, 0)...)

	// mdia
	var mdiaData []byte
	mdiaData = append(mdiaData, mdhdData(1000)...) // video timescale = 1000 (ms)
	mdiaData = append(mdiaData, hdlrData("vide", "VideoHandler")...)
	mdiaData = append(mdiaData, buildVideoMinf(codec, width, height)...)
	trakData = append(trakData, box("mdia", mdiaData)...)

	return box("trak", trakData)
}

func buildVideoMinf(codec string, width, height uint32) []byte {
	var minfData []byte

	// vmhd (video media header)
	vmhd := fullBox("vmhd", 0, 1, append([]byte{0x00, 0x00}, make([]byte, 6)...))
	minfData = append(minfData, vmhd...)

	// dinf > dref > url
	dinfData := box("dref", fullBox("url ", 0, 1, nil))
	minfData = append(minfData, box("dinf", append(u32(1), dinfData...))...)

	// stbl
	minfData = append(minfData, buildVideoStbl(codec, width, height)...)

	return box("minf", minfData)
}

func buildVideoStbl(codec string, width, height uint32) []byte {
	var stblData []byte

	// stsd: visual sample entry
	visualEntry := buildVisualSampleEntry(codec, uint16(width), uint16(height))
	stsdData := append(u32(1), visualEntry...) // entry_count = 1
	stblData = append(stblData, fullBox("stsd", 0, 0, stsdData)...)

	// stts (empty)
	stblData = append(stblData, fullBox("stts", 0, 0, u32(0))...)
	// stsc (empty)
	stblData = append(stblData, fullBox("stsc", 0, 0, u32(0))...)
	// stsz (empty)
	stszData := append(u32(0), u32(0)...) // sample_size=0, sample_count=0
	stblData = append(stblData, fullBox("stsz", 0, 0, stszData)...)
	// stco (empty)
	stblData = append(stblData, fullBox("stco", 0, 0, u32(0))...)

	return box("stbl", stblData)
}

func buildVisualSampleEntry(codec string, width, height uint16) []byte {
	var d []byte
	d = append(d, make([]byte, 6)...)    // reserved (6 bytes)
	d = append(d, u16(1)...)             // data_reference_index
	d = append(d, u16(0)...)             // pre_defined
	d = append(d, u16(0)...)             // reserved
	d = append(d, make([]byte, 12)...)   // pre_defined
	d = append(d, u16(width)...)         // width
	d = append(d, u16(height)...)        // height
	d = append(d, u32(0x00480000)...)    // horizresolution = 72 dpi
	d = append(d, u32(0x00480000)...)    // vertresolution = 72 dpi
	d = append(d, u32(0)...)             // reserved
	d = append(d, u16(1)...)             // frame_count
	d = append(d, make([]byte, 32)...)   // compressorname (32 bytes, zero-filled)
	d = append(d, u16(0x0018)...)        // depth = 24
	d = append(d, u16(0xFFFF)...)        // pre_defined = -1
	return box(codec, d)
}

// ─── trak (audio) ──────────────────────────────────────────────────────────

func buildAudioTrak(trackID uint32, codec string, sampleRate, channels uint32) []byte {
	var trakData []byte

	// tkhd (audio: volume=1.0, width/height=0)
	trakData = append(trakData, tkhdData(trackID, 0, 0, 0x0100)...)

	// mdia
	var mdiaData []byte
	mdiaData = append(mdiaData, mdhdData(sampleRate)...) // audio timescale = sample_rate
	mdiaData = append(mdiaData, hdlrData("soun", "SoundHandler")...)
	mdiaData = append(mdiaData, buildAudioMinf(codec, sampleRate, channels)...)
	trakData = append(trakData, box("mdia", mdiaData)...)

	return box("trak", trakData)
}

func buildAudioMinf(codec string, sampleRate, channels uint32) []byte {
	var minfData []byte

	// smhd (sound media header)
	smhd := fullBox("smhd", 0, 0, append(u16(0), u16(0)...)) // balance=0, reserved=0
	minfData = append(minfData, smhd...)

	// dinf > dref > url
	dinfData := box("dref", fullBox("url ", 0, 1, nil))
	minfData = append(minfData, box("dinf", append(u32(1), dinfData...))...)

	// stbl
	minfData = append(minfData, buildAudioStbl(codec, sampleRate, channels)...)

	return box("minf", minfData)
}

func buildAudioStbl(codec string, sampleRate, channels uint32) []byte {
	var stblData []byte

	// stsd: audio sample entry
	audioEntry := buildAudioSampleEntry(codec, uint16(channels), uint16(sampleRate))
	stsdData := append(u32(1), audioEntry...) // entry_count = 1
	stblData = append(stblData, fullBox("stsd", 0, 0, stsdData)...)

	// stts (empty)
	stblData = append(stblData, fullBox("stts", 0, 0, u32(0))...)
	// stsc (empty)
	stblData = append(stblData, fullBox("stsc", 0, 0, u32(0))...)
	// stsz (empty)
	stszData := append(u32(0), u32(0)...)
	stblData = append(stblData, fullBox("stsz", 0, 0, stszData)...)
	// stco (empty)
	stblData = append(stblData, fullBox("stco", 0, 0, u32(0))...)

	return box("stbl", stblData)
}

func buildAudioSampleEntry(codec string, channels, sampleRate uint16) []byte {
	var d []byte
	d = append(d, make([]byte, 6)...)    // reserved (6 bytes)
	d = append(d, u16(1)...)             // data_reference_index
	d = append(d, make([]byte, 8)...)    // reserved (8 bytes)
	d = append(d, u16(channels)...)      // channelcount
	d = append(d, u16(16)...)            // samplesize (16 bits)
	d = append(d, u16(0)...)             // pre_defined
	d = append(d, u16(0)...)             // reserved
	// samplerate as 16.16 fixed point
	d = append(d, u32(uint32(sampleRate)<<16)...)
	return box(codec, d)
}

// ─── 共用 box ──────────────────────────────────────────────────────────────

// tkhdData 构建 tkhd (track header) 内容。
// volume 为 8.8 fixed point (0x0100 = 1.0, 用于音频; 0x0000 用于视频)。
func tkhdData(trackID uint32, width, height, volume uint32) []byte {
	var d []byte
	d = append(d, u32(0)...)        // creation_time
	d = append(d, u32(0)...)        // modification_time
	d = append(d, u32(trackID)...)  // track_ID
	d = append(d, u32(0)...)        // reserved
	d = append(d, u32(0)...)        // duration
	d = append(d, make([]byte, 8)...) // reserved
	d = append(d, u16(0)...)        // layer
	d = append(d, u16(0)...)        // alternate_group
	d = append(d, u16(uint16(volume))...) // volume
	d = append(d, u16(0)...)        // reserved
	d = append(d, identityMatrix()...) // matrix (36 bytes)
	d = append(d, u32(width<<16)...)  // width (16.16 fixed point)
	d = append(d, u32(height<<16)...) // height (16.16 fixed point)
	return fullBox("tkhd", 0, 0x000007, d) // flags: track_enabled | track_in_movie | track_in_preview
}

// mdhdData 构建 mdhd (media header) 内容。
func mdhdData(timescale uint32) []byte {
	var d []byte
	d = append(d, u32(0)...)         // creation_time
	d = append(d, u32(0)...)         // modification_time
	d = append(d, u32(timescale)...) // timescale
	d = append(d, u32(0)...)         // duration
	d = append(d, u16(0x55C4)...)    // language = "und" (undetermined)
	d = append(d, u16(0)...)         // pre_defined
	return fullBox("mdhd", 0, 0, d)
}

// hdlrData 构建 hdlr (handler reference) 内容。
func hdlrData(handlerType, name string) []byte {
	var d []byte
	d = append(d, u32(0)...)                  // pre_defined
	d = append(d, []byte(handlerType)...)     // handler_type (4 bytes)
	d = append(d, make([]byte, 12)...)        // reserved (12 bytes)
	d = append(d, append([]byte(name), 0)...) // name (null-terminated)
	return fullBox("hdlr", 0, 0, d)
}

// trexData 构建 trex (track extends) 内容。
func trexData(trackID uint32) []byte {
	var d []byte
	d = append(d, u32(trackID)...) // track_ID
	d = append(d, u32(1)...)       // default_sample_description_index
	d = append(d, u32(0)...)       // default_sample_duration
	d = append(d, u32(0)...)       // default_sample_size
	d = append(d, u32(0)...)       // default_sample_flags
	return fullBox("trex", 0, 0, d)
}
