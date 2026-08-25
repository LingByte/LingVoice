package webrtc

import (
	"fmt"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/pion/webrtc/v4"
)

// audioRTCPFeedback 音频 RTCP 反馈配置
var audioRTCPFeedback = []webrtc.RTCPFeedback{
	{Type: webrtc.TypeRTCPFBNACK}, // NACK 重传
}

// videoRTCPFeedback 视频 RTCP 反馈配置
var videoRTCPFeedback = []webrtc.RTCPFeedback{
	{Type: webrtc.TypeRTCPFBTransportCC},       // TWCC 拥塞控制
	{Type: webrtc.TypeRTCPFBGoogREMB},          // REMB 带宽估计
	{Type: webrtc.TypeRTCPFBCCM, Parameter: "fir"}, // FIR 关键帧请求
	{Type: webrtc.TypeRTCPFBNACK},              // NACK 重传
	{Type: webrtc.TypeRTCPFBNACK, Parameter: "pli"}, // PLI 关键帧请求
}

// audioRTPExtensions 音频 RTP 头扩展
var audioRTPExtensions = []webrtc.RTPHeaderExtensionCapability{
	{URI: "urn:ietf:params:rtp-hdrext:ssrc-audio-level"}, // 音频级别（VAD）
	{URI: "urn:ietf:params:rtp-hdrext:sdes:mid"},         // SDES MID
}

// videoRTPExtensions 视频 RTP 头扩展
var videoRTPExtensions = []webrtc.RTPHeaderExtensionCapability{
	{URI: "urn:ietf:params:rtp-hdrext:sdes:mid"},              // SDES MID
	{URI: "urn:ietf:params:rtp-hdrext:sdes:rtp-stream-id"},    // RID（simulcast）
	{URI: "urn:ietf:params:rtp-hdrext:sdes:repaired-rtp-stream-id"}, // Repaired RID（RTX）
	{URI: "urn:ietf:params:rtp-hdrext:transport-cc"},          // TWCC
	{URI: "urn:ietf:params:rtp-hdrext:toffset"},               // 时间偏移
	{URI: "urn:ietf:params:rtp-hdrext:framemarking"},          // 帧标记
}

// createMediaEngine 根据方向配置创建 MediaEngine
func createMediaEngine(dir DirectionConfig) (*webrtc.MediaEngine, error) {
	me := &webrtc.MediaEngine{}

	// 注册音频编解码
	for _, codecName := range dir.AudioCodecs {
		if err := registerAudioCodec(me, codecName); err != nil {
			return nil, fmt.Errorf("register audio codec %s: %w", codecName, err)
		}
	}

	// 注册视频编解码
	for _, codecName := range dir.VideoCodecs {
		if err := registerVideoCodec(me, codecName); err != nil {
			return nil, fmt.Errorf("register video codec %s: %w", codecName, err)
		}
	}

	// 注册 RTP 头扩展
	for _, ext := range audioRTPExtensions {
		if err := me.RegisterHeaderExtension(ext, webrtc.RTPCodecTypeAudio); err != nil {
			// 扩展可能已注册，忽略错误
			_ = err
		}
	}
	for _, ext := range videoRTPExtensions {
		if err := me.RegisterHeaderExtension(ext, webrtc.RTPCodecTypeVideo); err != nil {
			_ = err
		}
	}

	return me, nil
}

// registerAudioCodec 注册单个音频编解码
func registerAudioCodec(me *webrtc.MediaEngine, name string) error {
	switch name {
	case "opus":
		return me.RegisterCodec(
			webrtc.RTPCodecParameters{
				RTPCodecCapability: webrtc.RTPCodecCapability{
					MimeType:     webrtc.MimeTypeOpus,
					ClockRate:    48000,
					Channels:     2,
					SDPFmtpLine:  "minptime=10;useinbandfec=1",
					RTCPFeedback: audioRTCPFeedback,
				},
				PayloadType: webrtc.PayloadType(111),
			},
			webrtc.RTPCodecTypeAudio,
		)
	case "pcmu":
		return me.RegisterCodec(
			webrtc.RTPCodecParameters{
				RTPCodecCapability: webrtc.RTPCodecCapability{
					MimeType:     webrtc.MimeTypePCMU,
					ClockRate:    8000,
					RTCPFeedback: audioRTCPFeedback,
				},
				PayloadType: webrtc.PayloadType(0),
			},
			webrtc.RTPCodecTypeAudio,
		)
	case "pcma":
		return me.RegisterCodec(
			webrtc.RTPCodecParameters{
				RTPCodecCapability: webrtc.RTPCodecCapability{
					MimeType:     webrtc.MimeTypePCMA,
					ClockRate:    8000,
					RTCPFeedback: audioRTCPFeedback,
				},
				PayloadType: webrtc.PayloadType(8),
			},
			webrtc.RTPCodecTypeAudio,
		)
	default:
		return fmt.Errorf("unsupported audio codec: %s", name)
	}
}

// registerVideoCodec 注册单个视频编解码
func registerVideoCodec(me *webrtc.MediaEngine, name string) error {
	switch name {
	case "h264":
		if err := me.RegisterCodec(
			webrtc.RTPCodecParameters{
				RTPCodecCapability: webrtc.RTPCodecCapability{
					MimeType:     webrtc.MimeTypeH264,
					ClockRate:    90000,
					SDPFmtpLine:  "level-asymmetry-allowed=1;profile-level-id=42e01f;packetization-mode=1",
					RTCPFeedback: videoRTCPFeedback,
				},
				PayloadType: webrtc.PayloadType(102),
			},
			webrtc.RTPCodecTypeVideo,
		); err != nil {
			return err
		}
		// 注册 RTX（重传）编解码
		return registerRTXCodec(me, 102, 103)
	case "vp8":
		if err := me.RegisterCodec(
			webrtc.RTPCodecParameters{
				RTPCodecCapability: webrtc.RTPCodecCapability{
					MimeType:     webrtc.MimeTypeVP8,
					ClockRate:    90000,
					RTCPFeedback: videoRTCPFeedback,
				},
				PayloadType: webrtc.PayloadType(96),
			},
			webrtc.RTPCodecTypeVideo,
		); err != nil {
			return err
		}
		return registerRTXCodec(me, 96, 97)
	case "vp9":
		if err := me.RegisterCodec(
			webrtc.RTPCodecParameters{
				RTPCodecCapability: webrtc.RTPCodecCapability{
					MimeType:     webrtc.MimeTypeVP9,
					ClockRate:    90000,
					SDPFmtpLine:  "profile-id=0",
					RTCPFeedback: videoRTCPFeedback,
				},
				PayloadType: webrtc.PayloadType(98),
			},
			webrtc.RTPCodecTypeVideo,
		); err != nil {
			return err
		}
		return registerRTXCodec(me, 98, 99)
	default:
		return fmt.Errorf("unsupported video codec: %s", name)
	}
}

// registerRTXCodec 注册 RTX 重传编解码
func registerRTXCodec(me *webrtc.MediaEngine, aptPayloadType uint8, rtxPayloadType uint8) error {
	return me.RegisterCodec(
		webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:    webrtc.MimeTypeRTX,
				ClockRate:   90000,
				SDPFmtpLine: fmt.Sprintf("apt=%d", aptPayloadType),
			},
			PayloadType: webrtc.PayloadType(rtxPayloadType),
		},
		webrtc.RTPCodecTypeVideo,
	)
}

// codecTypeFromMimeType 从 pion MimeType 转换为 CodecType
func codecTypeFromMimeType(mimeType string) common.CodecType {
	switch mimeType {
	case webrtc.MimeTypeOpus:
		return common.CodecOpus
	case webrtc.MimeTypePCMU:
		return common.CodecPCMU
	case webrtc.MimeTypePCMA:
		return common.CodecPCMA
	case webrtc.MimeTypeH264:
		return common.CodecH264
	case webrtc.MimeTypeVP8:
		return common.CodecVP8
	case webrtc.MimeTypeVP9:
		return common.CodecVP9
	case webrtc.MimeTypeAV1:
		return common.CodecAV1
	case webrtc.MimeTypeRTX:
		return common.CodecRTX
	default:
		return 0
	}
}

// mimeTypeFromCodecType 从 CodecType 转换为 pion MimeType
func mimeTypeFromCodecType(codec common.CodecType) string {
	switch codec {
	case common.CodecOpus:
		return webrtc.MimeTypeOpus
	case common.CodecPCMU:
		return webrtc.MimeTypePCMU
	case common.CodecPCMA:
		return webrtc.MimeTypePCMA
	case common.CodecH264:
		return webrtc.MimeTypeH264
	case common.CodecVP8:
		return webrtc.MimeTypeVP8
	case common.CodecVP9:
		return webrtc.MimeTypeVP9
	case common.CodecAV1:
		return webrtc.MimeTypeAV1
	default:
		return ""
	}
}
