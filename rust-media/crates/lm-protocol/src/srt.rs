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
}
