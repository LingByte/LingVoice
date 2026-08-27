//! RTSP Demuxer/Remuxer — MediaFrame ↔ RTSP
//!
//! 参考 Xiu RTSP demuxer/remuxer。
//!
//! RTSP 协议：
//! - 信令：DESCRIBE, SETUP, PLAY, TEARDOWN
//! - 媒体：RTP over TCP (interleaved) 或 RTP over UDP
//!
//! 简化实现：信令由外部处理，demuxer 解析 interleaved RTP，remuxer 封装 RTP。

use crate::{Demuxer, Protocol, Remuxer};
use lm_core::{CodecType, MediaFrame, TrackKind};
use lm_depacketizer::create_depacketizer;

/// RTSP Demuxer
///
/// 解析 RTSP interleaved 数据（`$\x00\x00\x00` + RTP payload）。
/// 信令（DESCRIBE/SETUP/PLAY）由外部处理。
pub struct RtspDemuxer {
    /// 视频解包器
    video_depacketizer: Box<dyn lm_core::Depacketizer>,
    /// 音频解包器
    audio_depacketizer: Box<dyn lm_core::Depacketizer>,
    /// 视频编解码
    video_codec: CodecType,
    /// 音频编解码
    audio_codec: CodecType,
    /// 缓冲（interleaved 数据可能不完整）
    buffer: Vec<u8>,
}

impl RtspDemuxer {
    pub fn new(video_codec: CodecType, audio_codec: CodecType) -> Self {
        Self {
            video_depacketizer: create_depacketizer(video_codec),
            audio_depacketizer: create_depacketizer(audio_codec),
            video_codec,
            audio_codec,
            buffer: Vec::new(),
        }
    }
}

impl Demuxer for RtspDemuxer {
    fn protocol(&self) -> Protocol {
        Protocol::Rtsp
    }

    fn push_data(&mut self, data: &[u8]) -> Vec<MediaFrame> {
        self.buffer.extend_from_slice(data);
        let mut frames = Vec::new();

        // 解析 interleaved RTP: `$` + channel(1) + length(2 BE) + RTP data
        while self.buffer.len() >= 4 {
            if self.buffer[0] != b'$' {
                // 不是 interleaved 数据，跳过
                self.buffer.remove(0);
                continue;
            }

            let channel = self.buffer[1];
            let length = u16::from_be_bytes([self.buffer[2], self.buffer[3]]) as usize;

            if self.buffer.len() < 4 + length {
                break; // 数据不完整
            }

            let rtp_data = &self.buffer[4..4 + length];

            // 解析 RTP header (12 bytes minimum)
            if rtp_data.len() >= 12 {
                let payload_type = rtp_data[1] & 0x7f;
                let sequence_number = u16::from_be_bytes([rtp_data[2], rtp_data[3]]);
                let timestamp =
                    u32::from_be_bytes([rtp_data[4], rtp_data[5], rtp_data[6], rtp_data[7]]);
                let ssrc =
                    u32::from_be_bytes([rtp_data[8], rtp_data[9], rtp_data[10], rtp_data[11]]);
                let marker = rtp_data[1] & 0x80 != 0;
                let payload = &rtp_data[12..];

                // channel 0 = video, channel 1 = audio（约定）
                let depacketizer = if channel == 0 {
                    &mut self.video_depacketizer
                } else {
                    &mut self.audio_depacketizer
                };

                let result = depacketizer.push_packet(payload, marker, sequence_number, timestamp);
                if matches!(result, lm_core::DepacketizeResult::FrameComplete) {
                    if let Some(frame) = depacketizer.take_frame() {
                        let mut frame = frame;
                        frame.ssrc = ssrc;
                        frames.push(frame);
                    }
                }
            }

            // 移除已处理的数据
            self.buffer.drain(0..4 + length);
        }

        frames
    }

    fn reset(&mut self) {
        self.buffer.clear();
        self.video_depacketizer.reset();
        self.audio_depacketizer.reset();
    }
}

/// RTSP Remuxer
///
/// 将 MediaFrame 封装为 RTP 包（interleaved 格式）。
pub struct RtspRemuxer {
    /// channel 计数
    video_channel: u8,
    audio_channel: u8,
    /// 序列号
    video_seq: u16,
    audio_seq: u16,
    /// 输出缓冲
    output_buffer: Vec<u8>,
}

impl RtspRemuxer {
    pub fn new() -> Self {
        Self {
            video_channel: 0,
            audio_channel: 1,
            video_seq: 0,
            audio_seq: 0,
            output_buffer: Vec::new(),
        }
    }

    /// 封装 RTP 包
    fn create_rtp_packet(
        payload_type: u8,
        marker: bool,
        seq: u16,
        timestamp: u32,
        ssrc: u32,
        payload: &[u8],
    ) -> Vec<u8> {
        let mut rtp = Vec::with_capacity(12 + payload.len());

        // V=2, P=0, X=0, CC=0
        rtp.push(0x80);
        // M + PT
        rtp.push((if marker { 0x80 } else { 0x00 }) | (payload_type & 0x7f));
        // sequence number
        rtp.extend_from_slice(&seq.to_be_bytes());
        // timestamp
        rtp.extend_from_slice(&timestamp.to_be_bytes());
        // SSRC
        rtp.extend_from_slice(&ssrc.to_be_bytes());
        // payload
        rtp.extend_from_slice(payload);

        rtp
    }

    /// 封装 interleaved 数据
    fn create_interleaved(channel: u8, data: &[u8]) -> Vec<u8> {
        let mut output = Vec::with_capacity(4 + data.len());
        output.push(b'$');
        output.push(channel);
        output.extend_from_slice(&(data.len() as u16).to_be_bytes());
        output.extend_from_slice(data);
        output
    }
}

impl Default for RtspRemuxer {
    fn default() -> Self {
        Self::new()
    }
}

impl Remuxer for RtspRemuxer {
    fn protocol(&self) -> Protocol {
        Protocol::Rtsp
    }

    fn push_frame(&mut self, frame: &MediaFrame) -> Vec<u8> {
        let (channel, payload_type, seq, clock_rate) = match frame.kind {
            TrackKind::Video => (self.video_channel, 96u8, &mut self.video_seq, 90000u32),
            TrackKind::Audio => (self.audio_channel, 97u8, &mut self.audio_seq, 48000u32),
        };

        // 简化：一个 MediaFrame = 一个 RTP 包
        // 实际需要根据 MTU 分片
        let rtp = Self::create_rtp_packet(
            payload_type,
            true, // marker
            *seq,
            frame.timestamp,
            frame.ssrc,
            &frame.data,
        );

        let interleaved = Self::create_interleaved(channel, &rtp);
        self.output_buffer.extend_from_slice(&interleaved);

        *seq = seq.wrapping_add(1);

        std::mem::take(&mut self.output_buffer)
    }

    fn flush(&mut self) -> Vec<u8> {
        std::mem::take(&mut self.output_buffer)
    }

    fn reset(&mut self) {
        self.output_buffer.clear();
        self.video_seq = 0;
        self.audio_seq = 0;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_rtsp_remuxer() {
        let mut remuxer = RtspRemuxer::new();

        let frame = MediaFrame::video(
            CodecType::H264,
            9000,
            bytes::Bytes::from(vec![0, 0, 0, 1, 0x65]),
            12345,
            true,
        );

        let output = remuxer.push_frame(&frame);
        assert!(!output.is_empty());
        assert_eq!(output[0], b'$'); // interleaved marker
        assert_eq!(output[1], 0); // video channel
    }

    #[test]
    fn test_rtsp_interleaved_format() {
        let data = vec![0xDE, 0xAD, 0xBE, 0xEF];
        let interleaved = RtspRemuxer::create_interleaved(2, &data);
        assert_eq!(interleaved[0], b'$');
        assert_eq!(interleaved[1], 2);
        // length = 4 in big-endian
        assert_eq!(interleaved[2], 0);
        assert_eq!(interleaved[3], 4);
        // payload
        assert_eq!(&interleaved[4..], &[0xDE, 0xAD, 0xBE, 0xEF]);
    }

    #[test]
    fn test_rtsp_rtp_packet() {
        let payload = vec![0x01, 0x02, 0x03];
        let rtp = RtspRemuxer::create_rtp_packet(96, true, 100, 9000, 12345, &payload);

        // V=2 → first byte = 0x80
        assert_eq!(rtp[0], 0x80);
        // M=1, PT=96 → 0x80 | 96 = 0xE0
        assert_eq!(rtp[1], 0xE0);
        // sequence = 100
        assert_eq!(u16::from_be_bytes([rtp[2], rtp[3]]), 100);
        // timestamp = 9000
        assert_eq!(u32::from_be_bytes([rtp[4], rtp[5], rtp[6], rtp[7]]), 9000);
        // ssrc = 12345
        assert_eq!(
            u32::from_be_bytes([rtp[8], rtp[9], rtp[10], rtp[11]]),
            12345
        );
        // payload
        assert_eq!(&rtp[12..], &[0x01, 0x02, 0x03]);
    }

    #[test]
    fn test_rtsp_audio_channel() {
        let mut remuxer = RtspRemuxer::new();
        let frame = MediaFrame::audio(CodecType::Opus, 4800, bytes::Bytes::from(vec![0x4F]), 999);
        let output = remuxer.push_frame(&frame);
        assert_eq!(output[0], b'$');
        assert_eq!(output[1], 1); // audio channel = 1
    }

    #[test]
    fn test_rtsp_seq_increment() {
        let mut remuxer = RtspRemuxer::new();
        let frame = MediaFrame::video(
            CodecType::H264,
            9000,
            bytes::Bytes::from(vec![0x01]),
            1,
            true,
        );
        let out1 = remuxer.push_frame(&frame);
        let out2 = remuxer.push_frame(&frame);

        // Parse interleaved → RTP → seq
        let seq1 = u16::from_be_bytes([out1[6], out1[7]]); // $+ch+len(2)+rtp[2:4]
        let seq2 = u16::from_be_bytes([out2[6], out2[7]]);
        assert_eq!(seq1, 0);
        assert_eq!(seq2, 1);
    }

    #[test]
    fn test_rtsp_reset() {
        let mut remuxer = RtspRemuxer::new();
        let frame = MediaFrame::video(
            CodecType::H264,
            9000,
            bytes::Bytes::from(vec![0x01]),
            1,
            true,
        );
        remuxer.push_frame(&frame);
        remuxer.reset();

        let out = remuxer.push_frame(&frame);
        let seq = u16::from_be_bytes([out[6], out[7]]);
        assert_eq!(seq, 0); // reset → seq = 0
    }

    #[test]
    fn test_rtsp_protocol() {
        let remuxer = RtspRemuxer::new();
        assert_eq!(remuxer.protocol(), Protocol::Rtsp);
    }
}
