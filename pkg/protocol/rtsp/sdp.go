package rtsp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
)

// SDPMedia 描述 SDP 中一条媒体段（m= 行及其属性）。
type SDPMedia struct {
	Type    string // "audio" / "video"
	Port    int
	Proto   string // "RTP/AVP"
	PT      int    // payload type
	RTPMap  string // a=rtpmap 值
	Control string // a=control 值（trackID=...）
	Fmtp    string // a=fmtp 值
}

// SDP 描述一个完整的 SDP 会话描述。
type SDP struct {
	Origin      string // o= 行
	SessionName string // s= 行
	Attributes  []string // a= 全局属性
	Media       []*SDPMedia
}

// ParseSDP 解析 SDP 文本。
func ParseSDP(data []byte) (*SDP, error) {
	sdp := &SDP{}
	lines := strings.Split(string(data), "\n")
	var current *SDPMedia
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if len(line) < 2 || line[1] != '=' {
			continue
		}
		typ := line[0]
		val := line[2:]
		switch typ {
		case 'o':
			sdp.Origin = val
		case 's':
			sdp.SessionName = val
		case 'a':
			if current != nil {
				if strings.HasPrefix(val, "rtpmap:") {
					current.RTPMap = strings.TrimPrefix(val, "rtpmap:")
				} else if strings.HasPrefix(val, "control:") {
					current.Control = strings.TrimPrefix(val, "control:")
				} else if strings.HasPrefix(val, "fmtp:") {
					current.Fmtp = strings.TrimPrefix(val, "fmtp:")
				}
			} else {
				sdp.Attributes = append(sdp.Attributes, val)
			}
		case 'm':
			parts := strings.Fields(val)
			if len(parts) < 4 {
				continue
			}
			port, _ := strconv.Atoi(parts[1])
			pt, _ := strconv.Atoi(parts[3])
			current = &SDPMedia{
				Type:  parts[0],
				Port:  port,
				Proto: parts[2],
				PT:    pt,
			}
			sdp.Media = append(sdp.Media, current)
		}
	}
	return sdp, nil
}

// BuildSDP 根据会话的 tracks 生成 SDP 描述。
func BuildSDP(session *Session) string {
	var buf strings.Builder
	buf.WriteString("v=0\r\n")
	buf.WriteString(fmt.Sprintf("o=- 0 0 IN IP4 0.0.0.0\r\n"))
	buf.WriteString("s=LingVoice RTSP Stream\r\n")
	buf.WriteString("t=0 0\r\n")

	tracks := session.Tracks()
	for i, t := range tracks {
		pt := 96 + i
		buf.WriteString(fmt.Sprintf("m=%s 0 RTP/AVP %d\r\n", t.Kind.String(), pt))
		buf.WriteString(fmt.Sprintf("a=control:trackID=%s\r\n", t.ID))
		buf.WriteString(fmt.Sprintf("a=rtpmap:%d %s/%d", pt, t.Codec.String(), t.SampleRate))
		if t.Kind == common.TrackAudio && t.Channels > 1 {
			buf.WriteString(fmt.Sprintf("/%d", t.Channels))
		}
		buf.WriteString("\r\n")
		if t.Kind == common.TrackVideo {
			buf.WriteString(fmt.Sprintf("a=fmtp:%d packetization-mode=1\r\n", pt))
		}
		buf.WriteString("a=recvonly\r\n")
	}
	return buf.String()
}
