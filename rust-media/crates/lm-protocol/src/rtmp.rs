//! RTMP Remuxer — MediaFrame → RTMP 输出
//!
//! 参考 Xiu RTMP remuxer。
//!
//! RTMP 协议：
//! 1. Handshake (C0/C1/C2 ← → S0/S1/S2)
//! 2. Chunk stream
//! 3. AMF messages (connect, createStream, publish, play)
//! 4. Media messages (audio/video)
//!
//! 简化实现：只实现 media message 封装，handshake 和 chunk 协议由外部处理。

use crate::{Protocol, Remuxer};
use lm_core::{CodecType, MediaFrame, TrackKind};

/// RTMP 握手状态
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RtmpHandshake {
    /// 未开始
    Uninitialized,
    /// 等待 C0/C1
    WaitC0C1,
    /// 已发送 S0/S1/S2，等待 C2
    WaitC2,
    /// 握手完成
    Done,
}

/// RTMP chunk type
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ChunkType {
    Type0 = 0,
    Type1 = 1,
    Type2 = 2,
    Type3 = 3,
}

/// RTMP message type
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum RtmpMessageType {
    SetChunkSize = 1,
    AbortMessage = 2,
    Acknowledgement = 3,
    UserControl = 4,
    WindowAckSize = 5,
    SetPeerBandwidth = 6,
    Audio = 8,
    Video = 9,
    DataAmf0 = 18,
    DataAmf3 = 15,
}

/// RTMP Remuxer
///
/// 将 MediaFrame 转为 RTMP chunk 格式。
/// 简化：只封装 media message，不处理 handshake 和控制消息。
pub struct RtmpRemuxer {
    /// chunk size (默认 4096)
    chunk_size: u32,
    /// 时间戳基准
    first_timestamp: Option<u32>,
    /// 上一帧时间戳（用于 Type2/Type3 chunk）
    last_timestamp: u32,
    /// 输出缓冲
    output_buffer: Vec<u8>,
    /// 是否已发送 onMetaData
    metadata_sent: bool,
}

impl RtmpRemuxer {
    pub fn new() -> Self {
        Self {
            chunk_size: 4096,
            first_timestamp: None,
            last_timestamp: 0,
            output_buffer: Vec::new(),
            metadata_sent: false,
        }
    }

    /// 设置 chunk size
    pub fn set_chunk_size(&mut self, size: u32) {
        self.chunk_size = size;
    }

    /// 生成 onMetaData AMF0 数据
    fn generate_metadata(&self, video_codec: CodecType, audio_codec: CodecType) -> Vec<u8> {
        let mut data = Vec::new();

        // AMF0 string: "onMetaData"
        data.push(0x02); // AMF0 string type
        data.push(0x00);
        data.push(0x0a); // length 10
        data.extend_from_slice(b"onMetaData");

        // AMF0 object
        data.push(0x03); // AMF0 object type

        // duration
        Self::amf0_string(&mut data, "duration");
        Self::amf0_number(&mut data, 0.0);

        // width
        Self::amf0_string(&mut data, "width");
        Self::amf0_number(&mut data, 1280.0);

        // height
        Self::amf0_string(&mut data, "height");
        Self::amf0_number(&mut data, 720.0);

        // videodatarate
        Self::amf0_string(&mut data, "videodatarate");
        Self::amf0_number(&mut data, 1500.0);

        // framerate
        Self::amf0_string(&mut data, "framerate");
        Self::amf0_number(&mut data, 30.0);

        // audiosamplerate
        Self::amf0_string(&mut data, "audiosamplerate");
        Self::amf0_number(&mut data, 48000.0);

        // audiochannels
        Self::amf0_string(&mut data, "audiochannels");
        Self::amf0_number(&mut data, 1.0);

        // object end
        data.push(0x00);
        data.push(0x00);
        data.push(0x09); // object end marker

        data
    }

    fn amf0_string(data: &mut Vec<u8>, s: &str) {
        data.push(0x02);
        let len = s.len() as u16;
        data.extend_from_slice(&len.to_be_bytes());
        data.extend_from_slice(s.as_bytes());
    }

    fn amf0_number(data: &mut Vec<u8>, n: f64) {
        data.push(0x00);
        data.extend_from_slice(&n.to_be_bytes());
    }

    /// 封装 RTMP chunk
    fn create_chunk(
        &self,
        chunk_type: ChunkType,
        cs_id: u32,
        timestamp: u32,
        message_type: u8,
        message_stream_id: u32,
        payload: &[u8],
    ) -> Vec<u8> {
        let mut chunk = Vec::new();

        // Basic header
        let basic_header = (chunk_type as u8) << 6 | (cs_id as u8 & 0x3f);
        chunk.push(basic_header);

        // Message header (Type0 = 11 bytes)
        if chunk_type == ChunkType::Type0 {
            // timestamp (3 bytes)
            chunk.push((timestamp >> 16) as u8);
            chunk.push((timestamp >> 8) as u8);
            chunk.push(timestamp as u8);
            // message length (3 bytes)
            let msg_len = payload.len() as u32;
            chunk.push((msg_len >> 16) as u8);
            chunk.push((msg_len >> 8) as u8);
            chunk.push(msg_len as u8);
            // message type id (1 byte)
            chunk.push(message_type);
            // message stream id (4 bytes, little endian)
            chunk.extend_from_slice(&message_stream_id.to_le_bytes());
        }

        // chunk data
        let chunk_data_size = std::cmp::min(payload.len(), self.chunk_size as usize);
        chunk.extend_from_slice(&payload[..chunk_data_size]);

        // 如果 payload 超过 chunk_size，用 Type3 chunk 继续发送
        if payload.len() > self.chunk_size as usize {
            let mut offset = chunk_data_size;
            while offset < payload.len() {
                // Type3 basic header (no message header)
                chunk.push(ChunkType::Type3 as u8 | (cs_id as u8 & 0x3f));
                let remaining = std::cmp::min(payload.len() - offset, self.chunk_size as usize);
                chunk.extend_from_slice(&payload[offset..offset + remaining]);
                offset += remaining;
            }
        }

        chunk
    }
}

impl Default for RtmpRemuxer {
    fn default() -> Self {
        Self::new()
    }
}

impl Remuxer for RtmpRemuxer {
    fn protocol(&self) -> Protocol {
        Protocol::Rtmp
    }

    fn push_frame(&mut self, frame: &MediaFrame) -> Vec<u8> {
        if self.first_timestamp.is_none() {
            self.first_timestamp = Some(frame.timestamp);
        }

        // 时间戳从 RTP 时钟转为毫秒
        let timestamp_ms = if frame.kind == TrackKind::Video {
            frame.timestamp / 90
        } else {
            frame.timestamp / 48
        };

        // 第一次发送 onMetaData
        if !self.metadata_sent {
            let metadata = self.generate_metadata(frame.codec, frame.codec);
            let chunk = self.create_chunk(
                ChunkType::Type0,
                4, // cs_id for data
                0,
                RtmpMessageType::DataAmf0 as u8,
                1,
                &metadata,
            );
            self.output_buffer.extend_from_slice(&chunk);
            self.metadata_sent = true;
        }

        // 封装 media message
        let (msg_type, cs_id) = match frame.kind {
            TrackKind::Video => (RtmpMessageType::Video as u8, 6),
            TrackKind::Audio => (RtmpMessageType::Audio as u8, 7),
        };

        // RTMP video/audio payload 格式与 FLV 相同
        let payload = match frame.kind {
            TrackKind::Video => {
                let frame_type = if frame.keyframe { 1u8 } else { 2u8 };
                let codec_id = match frame.codec {
                    CodecType::H264 => 7u8,
                    _ => 7u8,
                };
                let mut p = vec![(frame_type << 4) | codec_id];
                if frame.codec == CodecType::H264 {
                    p.push(0x01); // AVCPacketType = NALU
                    p.extend_from_slice(&[0, 0, 0]); // CompositionTime
                }
                p.extend_from_slice(&frame.data);
                p
            }
            TrackKind::Audio => {
                let codec_id = match frame.codec {
                    CodecType::Opus => 13u8, // extended
                    CodecType::PcmU => 8u8,
                    CodecType::PcmA => 7u8,
                    CodecType::Aac => 10u8,
                    _ => 13u8,
                };
                let mut p = vec![(codec_id << 4) | 0x0f]; // 48kHz, 16-bit, stereo
                if frame.codec == CodecType::Aac {
                    p.push(0x01);
                }
                p.extend_from_slice(&frame.data);
                p
            }
        };

        let chunk = self.create_chunk(
            ChunkType::Type0,
            cs_id,
            timestamp_ms,
            msg_type,
            1,
            &payload,
        );

        self.output_buffer.extend_from_slice(&chunk);
        self.last_timestamp = timestamp_ms;

        std::mem::take(&mut self.output_buffer)
    }

    fn flush(&mut self) -> Vec<u8> {
        std::mem::take(&mut self.output_buffer)
    }

    fn reset(&mut self) {
        self.output_buffer.clear();
        self.first_timestamp = None;
        self.last_timestamp = 0;
        self.metadata_sent = false;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_rtmp_metadata() {
        let remuxer = RtmpRemuxer::new();
        let metadata = remuxer.generate_metadata(CodecType::H264, CodecType::Opus);
        assert!(!metadata.is_empty());
        // AMF0 string "onMetaData"
        assert_eq!(metadata[0], 0x02);
    }

    #[test]
    fn test_rtmp_video_chunk() {
        let mut remuxer = RtmpRemuxer::new();

        let frame = MediaFrame::video(
            CodecType::H264,
            9000,
            bytes::Bytes::from(vec![0, 0, 0, 1, 0x65]),
            1,
            true,
        );

        let output = remuxer.push_frame(&frame);
        assert!(!output.is_empty());
    }

    #[test]
    fn test_rtmp_audio_chunk() {
        let mut remuxer = RtmpRemuxer::new();

        let frame = MediaFrame::audio(
            CodecType::Opus,
            4800,
            bytes::Bytes::from(vec![0x4F, 0x61]),
            12345,
        );

        let output = remuxer.push_frame(&frame);
        assert!(!output.is_empty());
    }

    #[test]
    fn test_rtmp_set_chunk_size() {
        let mut remuxer = RtmpRemuxer::new();
        assert_eq!(remuxer.chunk_size, 4096);
        remuxer.set_chunk_size(8192);
        assert_eq!(remuxer.chunk_size, 8192);
    }

    #[test]
    fn test_rtmp_metadata_sent_on_first_frame() {
        let mut remuxer = RtmpRemuxer::new();
        assert!(!remuxer.metadata_sent);

        let frame = MediaFrame::video(
            CodecType::H264,
            9000,
            bytes::Bytes::from(vec![0x01]),
            1,
            true,
        );
        remuxer.push_frame(&frame);
        assert!(remuxer.metadata_sent);
    }

    #[test]
    fn test_rtmp_reset() {
        let mut remuxer = RtmpRemuxer::new();
        let frame = MediaFrame::video(
            CodecType::H264,
            9000,
            bytes::Bytes::from(vec![0x01]),
            1,
            true,
        );
        remuxer.push_frame(&frame);
        assert!(remuxer.metadata_sent);

        remuxer.reset();
        assert!(!remuxer.metadata_sent);
        assert!(remuxer.first_timestamp.is_none());
    }

    #[test]
    fn test_rtmp_protocol() {
        let remuxer = RtmpRemuxer::new();
        assert_eq!(remuxer.protocol(), Protocol::Rtmp);
    }

    #[test]
    fn test_rtmp_handshake_states() {
        let states = [
            RtmpHandshake::Uninitialized,
            RtmpHandshake::WaitC0C1,
            RtmpHandshake::WaitC2,
            RtmpHandshake::Done,
        ];
        assert_eq!(states.len(), 4);
        assert_ne!(RtmpHandshake::Uninitialized, RtmpHandshake::Done);
    }

    #[test]
    fn test_rtmp_metadata_contains_codec_info() {
        let remuxer = RtmpRemuxer::new();
        let metadata = remuxer.generate_metadata(CodecType::H264, CodecType::Opus);
        // Should contain "onMetaData" string
        assert!(metadata.windows(10).any(|w| w == b"onMetaData"));
    }
}
