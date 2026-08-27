package dash

import (
	"testing"
)

// TestInitSegmentFtyp 验证 init segment 以 ftyp box 开头。
func TestInitSegmentFtyp(t *testing.T) {
	data := BuildInitSegment("avc1", 640, 360, "mp4a", 48000, 2)
	if len(data) < 8 {
		t.Fatal("init segment too short")
	}

	// ftyp box: first 4 bytes = size, next 4 bytes = "ftyp"
	boxType := string(data[4:8])
	if boxType != "ftyp" {
		t.Errorf("first box type = %q, want \"ftyp\"", boxType)
	}

	// major_brand should be "iso5"
	if len(data) < 16 {
		t.Fatal("init segment too short for ftyp major_brand")
	}
	majorBrand := string(data[8:12])
	if majorBrand != "iso5" {
		t.Errorf("ftyp major_brand = %q, want \"iso5\"", majorBrand)
	}
}

// TestInitSegmentMoov 验证 init segment 包含 moov box。
func TestInitSegmentMoov(t *testing.T) {
	data := BuildInitSegment("avc1", 640, 360, "mp4a", 48000, 2)
	if len(data) < 16 {
		t.Fatal("init segment too short")
	}

	// ftyp box size
	ftypSize := uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	if int(ftypSize) >= len(data) {
		t.Fatal("ftyp size exceeds total init segment size")
	}

	// moov box should start right after ftyp
	moovOffset := int(ftypSize)
	if moovOffset+8 > len(data) {
		t.Fatal("no room for moov box after ftyp")
	}
	moovType := string(data[moovOffset+4 : moovOffset+8])
	if moovType != "moov" {
		t.Errorf("second box type = %q, want \"moov\"", moovType)
	}
}

// TestInitSegmentLength 验证 init segment 总长度 > 0。
func TestInitSegmentLength(t *testing.T) {
	data := BuildInitSegment("avc1", 1920, 1080, "mp4a", 44100, 2)
	if len(data) == 0 {
		t.Error("init segment should have non-zero length")
	}
	// 一个合理的 init segment 至少应有 ftyp + moov, 通常 > 200 字节
	if len(data) < 200 {
		t.Errorf("init segment length = %d, expected at least 200", len(data))
	}
}

// TestInitSegmentVideoOnly 验证只有视频时的 init segment。
func TestInitSegmentVideoOnly(t *testing.T) {
	data := BuildInitSegment("avc1", 320, 240, "", 0, 0)
	if len(data) == 0 {
		t.Error("video-only init segment should have non-zero length")
	}

	// 验证包含 moov
	ftypSize := uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	moovType := string(data[ftypSize+4 : ftypSize+8])
	if moovType != "moov" {
		t.Errorf("expected moov box, got %q", moovType)
	}
}

// TestInitSegmentAudioOnly 验证只有音频时的 init segment。
func TestInitSegmentAudioOnly(t *testing.T) {
	data := BuildInitSegment("", 0, 0, "mp4a", 48000, 2)
	if len(data) == 0 {
		t.Error("audio-only init segment should have non-zero length")
	}

	ftypSize := uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	moovType := string(data[ftypSize+4 : ftypSize+8])
	if moovType != "moov" {
		t.Errorf("expected moov box, got %q", moovType)
	}
}

// TestInitSegmentContainsTrak 验证 moov 中包含 trak box。
func TestInitSegmentContainsTrak(t *testing.T) {
	data := BuildInitSegment("avc1", 640, 360, "mp4a", 48000, 2)

	// 在整个 init segment 中搜索 "trak" box type
	found := false
	for i := 0; i < len(data)-4; i++ {
		if string(data[i:i+4]) == "trak" {
			found = true
			break
		}
	}
	if !found {
		t.Error("init segment should contain trak box")
	}
}

// TestInitSegmentContainsMvex 验证 moov 中包含 mvex box。
func TestInitSegmentContainsMvex(t *testing.T) {
	data := BuildInitSegment("avc1", 640, 360, "mp4a", 48000, 2)

	found := false
	for i := 0; i < len(data)-4; i++ {
		if string(data[i:i+4]) == "mvex" {
			found = true
			break
		}
	}
	if !found {
		t.Error("init segment should contain mvex box for fMP4")
	}
}
