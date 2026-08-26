//! HTTP-FLV Remuxer — MediaFrame → FLV 流
//!
//! 参考 Xiu FLV remuxer。
//!
//! FLV 格式：
//! - FLV header (9 bytes): "FLV" + version + flags + header_size
//! - Previous tag size (4 bytes)
//! - FLV tag: type + data size + timestamp + stream id + data
//!
//! Tag types:
//! - 8: audio
//! - 9: video
//! - 18: script data (onMetaData)

use crate::{Protocol, Remuxer};
use lm_core::{CodecType, MediaFrame, TrackKind};

/// FLV header (9 bytes)
#[derive(Debug, Clone)]
pub struct FlvHeader {
    pub has_audio: bool,
    pub has_video: bool,
}

impl FlvHeader {
    pub fn new(has_audio: bool, has_video: bool) -> Self {
        Self { has_audio, has_video }
    }

    /// 生成 FLV header bytes
    pub fn to_bytes(&self) -> Vec<u8> {
        let mut bytes = vec![
            b'F', b'L', b'V', // signature
            0x01, // version 1
            0x00, // flags (will set audio/video bits)
            0x00, 0x00, 0x00, 0x09, // header size = 9
        ];

        // flags: bit 2 = audio, bit 0 = video
        let mut flags = 0u8;
        if self.has_audio {
            flags |= 0x04;
        }
        if self.has_video {
            flags |= 0x01;
        }
        bytes[4] = flags;

        bytes
    }
}

/// FLV tag type
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum FlvTagType {
    Audio = 8,
    Video = 9,
    ScriptData = 18,
}

/// FLV video frame type
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum FlvFrameType {
    KeyFrame = 1,
    InterFrame = 2,
    DisposableInterFrame = 3,
    GeneratedKeyFrame = 4,
    VideoInfoCommandFrame = 5,
}

/// FLV video codec ID
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum FlvVideoCodecId {
    SorensonH263 = 2,
    ScreenVideo = 3,
    Vp6 = 4,
    Vp6Alpha = 5,
    ScreenVideoV2 = 6,
    Avc = 7,
    // HEVC = 12 (extended)
}

/// FLV audio codec ID
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum FlvAudioCodecId {
    LinearPcmHostEndian = 0,
    Adpcm = 1,
    Mp3 = 2,
    LinearPcmLittleEndian = 3,
    Nellymoser16K = 4,
    Nellymoser8K = 5,
    Nellymoser = 6,
    G711ALaw = 7,
    G711MuLaw = 8,
    Reserved = 9,
    Aac = 10,
    Speex = 11,
    Opus = 13, // extended
}

/// FLV Remuxer
pub struct FlvRemuxer {
    /// 是否已写入 FLV header
    header_written: bool,
    /// 是否有音频
    has_audio: bool,
    /// 是否有视频
    has_video: bool,
    /// 上一帧时间戳（用于计算连续性）
    last_timestamp: u32,
    /// 输出缓冲
    output_buffer: Vec<u8>,
}

impl FlvRemuxer {
    pub fn new(has_audio: bool, has_video: bool) -> Self {
        Self {
            header_written: false,
            has_audio,
            has_video,
            last_timestamp: 0,
            output_buffer: Vec::new(),
        }
    }

    /// 写入 FLV header
    fn write_header(&mut self) {
        let header = FlvHeader::new(self.has_audio, self.has_video);
        self.output_buffer.extend_from_slice(&header.to_bytes());
        // Previous tag size 0 (4 bytes)
        self.output_buffer.extend_from_slice(&[0, 0, 0, 0]);
        self.header_written = true;
    }

    /// 创建 FLV tag
    fn create_tag(tag_type: FlvTagType, timestamp: u32, data: &[u8]) -> Vec<u8> {
        let data_size = data.len() as u32;

        let mut tag = Vec::with_capacity(11 + data.len() + 4);

        // Tag header (11 bytes)
        tag.push(tag_type as u8); // type
        // data size (3 bytes, big endian)
        tag.push((data_size >> 16) as u8);
        tag.push((data_size >> 8) as u8);
        tag.push(data_size as u8);
        // timestamp (3 bytes) + timestamp extended (1 byte)
        tag.push((timestamp >> 16) as u8);
        tag.push((timestamp >> 8) as u8);
        tag.push(timestamp as u8);
        tag.push((timestamp >> 24) as u8); // extended
        // stream id (3 bytes, always 0)
        tag.push(0);
        tag.push(0);
        tag.push(0);

        // tag data
        tag.extend_from_slice(data);

        // previous tag size (4 bytes)
        let total_size = (11 + data.len()) as u32;
        tag.extend_from_slice(&total_size.to_be_bytes());

        tag
    }

    /// 编码视频帧为 FLV video tag data
    fn encode_video_frame(frame: &MediaFrame) -> Vec<u8> {
        let frame_type = if frame.keyframe {
            FlvFrameType::KeyFrame
        } else {
            FlvFrameType::InterFrame
        };

        let codec_id = match frame.codec {
            CodecType::H264 => FlvVideoCodecId::Avc,
            _ => FlvVideoCodecId::Avc, // 默认 AVC
        };

        // 第一个字节: frame_type (高4位) | codec_id (低4位)
        let first_byte = ((frame_type as u8) << 4) | (codec_id as u8);

        let mut data = vec![first_byte];

        // H.264/AVC: AVCPacketType + CompositionTime + NALU data
        if frame.codec == CodecType::H264 {
            data.push(0x01); // AVCPacketType = 1 (NALU)
            data.extend_from_slice(&[0, 0, 0]); // CompositionTime = 0
            data.extend_from_slice(&frame.data);
        } else {
            // 其他编解码：直接追加帧数据
            data.extend_from_slice(&frame.data);
        }

        data
    }

    /// 编码音频帧为 FLV audio tag data
    fn encode_audio_frame(frame: &MediaFrame) -> Vec<u8> {
        let codec_id = match frame.codec {
            CodecType::Opus => FlvAudioCodecId::Opus,
            CodecType::PcmU => FlvAudioCodecId::G711MuLaw,
            CodecType::PcmA => FlvAudioCodecId::G711ALaw,
            CodecType::Aac => FlvAudioCodecId::Aac,
            _ => FlvAudioCodecId::Opus,
        };

        // 第一个字节: sound format (高4位) | sample rate (2位) | sample size (1位) | sound type (1位)
        // 简化：48kHz, 16-bit, mono
        let sound_format = codec_id as u8;
        let sample_rate = 3; // 44.1kHz (0=5.5k, 1=11k, 2=22k, 3=44k)
        let sample_size = 1; // 16-bit
        let sound_type = 0; // mono
        let first_byte = (sound_format << 4) | (sample_rate << 2) | (sample_size << 1) | sound_type;

        let mut data = vec![first_byte];

        // AAC 需要 AACPacketType
        if frame.codec == CodecType::Aac {
            data.push(0x01); // AAC raw
        }

        data.extend_from_slice(&frame.data);
        data
    }
}

impl Remuxer for FlvRemuxer {
    fn protocol(&self) -> Protocol {
        Protocol::HttpFlv
    }

    fn push_frame(&mut self, frame: &MediaFrame) -> Vec<u8> {
        if !self.header_written {
            self.write_header();
        }

        // 时间戳从 RTP 90kHz 转为 FLV 毫秒
        let timestamp_ms = if frame.kind == TrackKind::Video {
            frame.timestamp / 90 // 90kHz → ms
        } else {
            // Opus 48kHz → ms
            frame.timestamp / 48
        };

        let tag_data = match frame.kind {
            TrackKind::Video => Self::encode_video_frame(frame),
            TrackKind::Audio => Self::encode_audio_frame(frame),
        };

        let tag = Self::create_tag(
            if frame.kind == TrackKind::Video {
                FlvTagType::Video
            } else {
                FlvTagType::Audio
            },
            timestamp_ms,
            &tag_data,
        );

        self.output_buffer.extend_from_slice(&tag);
        self.last_timestamp = timestamp_ms;

        // 返回当前输出的所有数据（FLV 是流式协议）
        std::mem::take(&mut self.output_buffer)
    }

    fn flush(&mut self) -> Vec<u8> {
        std::mem::take(&mut self.output_buffer)
    }

    fn reset(&mut self) {
        self.header_written = false;
        self.output_buffer.clear();
        self.last_timestamp = 0;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_flv_header() {
        let header = FlvHeader::new(true, true);
        let bytes = header.to_bytes();
        assert_eq!(bytes.len(), 9);
        assert_eq!(&bytes[0..3], b"FLV");
        assert_eq!(bytes[4], 0x05); // audio + video
    }

    #[test]
    fn test_flv_video_tag() {
        let mut remuxer = FlvRemuxer::new(false, true);

        let frame = MediaFrame::video(
            CodecType::H264,
            9000, // 100ms
            bytes::Bytes::from(vec![0, 0, 0, 1, 0x65, 0x88]),
            1,
            true,
        );

        let output = remuxer.push_frame(&frame);

        // FLV header (9) + previous tag size (4) + tag header (11) + tag data + previous tag size (4)
        assert!(output.len() > 9 + 4 + 11);
        assert_eq!(&output[0..3], b"FLV");
    }

    #[test]
    fn test_flv_audio_tag() {
        let mut remuxer = FlvRemuxer::new(true, false);

        let frame = MediaFrame::audio(
            CodecType::Opus,
            4800, // 100ms at 48kHz
            bytes::Bytes::from(vec![0x4f, 0x61]),
            1,
        );

        let output = remuxer.push_frame(&frame);

        // FLV header (9) + previous tag size 0 (4) + tag header (11) + tag data + previous tag size (4)
        assert!(output.len() > 9 + 4 + 11);
        // Audio tag type = 8 at position 9 (after FLV header) + 4 (previous tag size 0)
        assert_eq!(output[13], 8);
    }
}
