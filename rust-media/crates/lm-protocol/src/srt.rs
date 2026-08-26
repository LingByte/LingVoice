//! SRT Demuxer/Remuxer — MediaFrame ↔ SRT
//!
//! 参考 Xiu SRT 支持。
//!
//! SRT 协议：
//! - 基于 UDP
//! - Handshake + ACK + NAK + KeepAlive
//! - Payload: sequence(4) + message(4) + timestamp(4) + dst_socket_id(4) + data
//!
//! 简化实现：只处理 payload 数据部分，handshake 由外部处理。

use crate::{Demuxer, Protocol, Remuxer};
use lm_core::{CodecType, MediaFrame, TrackKind};
use lm_depacketizer::create_depacketizer;

/// SRT Demuxer
///
/// 解析 SRT payload 中的 RTP 数据。
pub struct SrtDemuxer {
    video_depacketizer: Box<dyn lm_core::Depacketizer>,
    audio_depacketizer: Box<dyn lm_core::Depacketizer>,
    buffer: Vec<u8>,
}

impl SrtDemuxer {
    pub fn new(video_codec: CodecType, audio_codec: CodecType) -> Self {
        Self {
            video_depacketizer: create_depacketizer(video_codec),
            audio_depacketizer: create_depacketizer(audio_codec),
            buffer: Vec::new(),
        }
    }
}

impl Demuxer for SrtDemuxer {
    fn protocol(&self) -> Protocol {
        Protocol::Srt
    }

    fn push_data(&mut self, data: &[u8]) -> Vec<MediaFrame> {
        // SRT data packet header: 16 bytes
        // sequence(4) + message(4) + timestamp(4) + dst_socket_id(4)
        if data.len() < 16 {
            return Vec::new();
        }

        let payload = &data[16..];

        // 简化：假设 payload 是 MPEG-TS 格式
        // 实际 SRT 通常传输 MPEG-TS，需要 TS demux
        // 这里简化为直接 RTP 解包
        if payload.len() < 12 {
            return Vec::new();
        }

        // 尝试作为 RTP 解析
        let timestamp = u32::from_be_bytes([payload[4], payload[5], payload[6], payload[7]]);
        let sequence_number = u16::from_be_bytes([payload[2], payload[3]]);
        let marker = payload[1] & 0x80 != 0;
        let rtp_payload = &payload[12..];

        let mut frames = Vec::new();

        // 尝试视频解包
        let result = self.video_depacketizer.push_packet(
            rtp_payload,
            marker,
            sequence_number,
            timestamp,
        );

        if matches!(result, lm_core::DepacketizeResult::FrameComplete) {
            if let Some(frame) = self.video_depacketizer.take_frame() {
                frames.push(frame);
            }
        }

        frames
    }

    fn reset(&mut self) {
        self.buffer.clear();
        self.video_depacketizer.reset();
        self.audio_depacketizer.reset();
    }
}

/// SRT Remuxer
///
/// 将 MediaFrame 封装为 SRT data packet。
pub struct SrtRemuxer {
    sequence: u32,
    output_buffer: Vec<u8>,
}

impl SrtRemuxer {
    pub fn new() -> Self {
        Self {
            sequence: 0,
            output_buffer: Vec::new(),
        }
    }
}

impl Default for SrtRemuxer {
    fn default() -> Self {
        Self::new()
    }
}

impl Remuxer for SrtRemuxer {
    fn protocol(&self) -> Protocol {
        Protocol::Srt
    }

    fn push_frame(&mut self, frame: &MediaFrame) -> Vec<u8> {
        // SRT data packet header (16 bytes)
        let mut packet = Vec::with_capacity(16 + frame.data.len());

        // sequence number (4 bytes)
        packet.extend_from_slice(&self.sequence.to_be_bytes());
        self.sequence = self.sequence.wrapping_add(1);

        // message number (4 bytes): bit 0-1 = position, bit 2 = encryption, etc.
        packet.extend_from_slice(&1u32.to_be_bytes());

        // timestamp (4 bytes): SRT 使用微秒时间戳
        let ts_us = (frame.timestamp as u64 * 1_000_000 / 90000) as u32; // 90kHz → μs
        packet.extend_from_slice(&ts_us.to_be_bytes());

        // destination socket id (4 bytes)
        packet.extend_from_slice(&0u32.to_be_bytes());

        // payload
        packet.extend_from_slice(&frame.data);

        self.output_buffer.extend_from_slice(&packet);
        std::mem::take(&mut self.output_buffer)
    }

    fn flush(&mut self) -> Vec<u8> {
        std::mem::take(&mut self.output_buffer)
    }

    fn reset(&mut self) {
        self.output_buffer.clear();
        self.sequence = 0;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_srt_remuxer() {
        let mut remuxer = SrtRemuxer::new();

        let frame = MediaFrame::video(
            CodecType::H264,
            9000,
            bytes::Bytes::from(vec![0, 0, 0, 1, 0x65]),
            1,
            true,
        );

        let output = remuxer.push_frame(&frame);
        assert!(output.len() > 16); // header + payload
    }

    #[test]
    fn test_srt_remuxer_header_fields() {
        let mut remuxer = SrtRemuxer::new();

        let frame = MediaFrame::video(
            CodecType::H264,
            9000, // 90kHz → 100ms → 100000μs
            bytes::Bytes::from(vec![0xAB, 0xCD]),
            1,
            true,
        );

        let output = remuxer.push_frame(&frame);
        assert_eq!(output.len(), 16 + 2); // 16 header + 2 payload

        // sequence number = 0 (first packet)
        let seq = u32::from_be_bytes([output[0], output[1], output[2], output[3]]);
        assert_eq!(seq, 0);

        // message number = 1
        let msg = u32::from_be_bytes([output[4], output[5], output[6], output[7]]);
        assert_eq!(msg, 1);

        // timestamp = 9000 * 1000000 / 90000 = 100000μs
        let ts = u32::from_be_bytes([output[8], output[9], output[10], output[11]]);
        assert_eq!(ts, 100000);

        // dst socket id = 0
        let dst = u32::from_be_bytes([output[12], output[13], output[14], output[15]]);
        assert_eq!(dst, 0);

        // payload
        assert_eq!(&output[16..], &[0xAB, 0xCD]);
    }

    #[test]
    fn test_srt_remuxer_sequence_increment() {
        let mut remuxer = SrtRemuxer::new();

        let frame = MediaFrame::audio(
            CodecType::Opus,
            4800,
            bytes::Bytes::from(vec![0x01]),
            1,
        );

        remuxer.push_frame(&frame);
        let output2 = remuxer.push_frame(&frame);

        // Second packet should have sequence = 1
        let seq = u32::from_be_bytes([output2[0], output2[1], output2[2], output2[3]]);
        assert_eq!(seq, 1);
    }

    #[test]
    fn test_srt_remuxer_reset() {
        let mut remuxer = SrtRemuxer::new();

        let frame = MediaFrame::audio(
            CodecType::Opus,
            4800,
            bytes::Bytes::from(vec![0x01]),
            1,
        );

        remuxer.push_frame(&frame);
        remuxer.reset();

        let output = remuxer.push_frame(&frame);
        let seq = u32::from_be_bytes([output[0], output[1], output[2], output[3]]);
        assert_eq!(seq, 0); // reset → seq starts at 0
    }

    #[test]
    fn test_srt_demuxer_short_data() {
        let mut demuxer = SrtDemuxer::new(CodecType::H264, CodecType::Opus);
        // Too short for SRT header (< 16 bytes)
        let frames = demuxer.push_data(&[0, 1, 2, 3]);
        assert!(frames.is_empty());
    }

    #[test]
    fn test_srt_demuxer_short_payload() {
        let mut demuxer = SrtDemuxer::new(CodecType::H264, CodecType::Opus);
        // 16 bytes header but payload too short for RTP (< 12 bytes)
        let data = vec![0u8; 20]; // 16 header + 4 payload
        let frames = demuxer.push_data(&data);
        assert!(frames.is_empty());
    }

    #[test]
    fn test_srt_protocol() {
        let remuxer = SrtRemuxer::new();
        assert_eq!(remuxer.protocol(), Protocol::Srt);

        let demuxer = SrtDemuxer::new(CodecType::H264, CodecType::Opus);
        assert_eq!(demuxer.protocol(), Protocol::Srt);
    }
}
