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

/// MTU 常量 = 1400（留余量给 IP/UDP/RTP header）
const MTU: usize = 1400;

/// RTP header 固定长度（12 bytes）
const RTP_HEADER_LEN: usize = 12;

/// 单个 RTP 包最大 payload 长度
const MAX_RTP_PAYLOAD: usize = MTU - RTP_HEADER_LEN;

/// RTSP Remuxer
///
/// 将 MediaFrame 封装为 RTP 包（interleaved 格式）。
/// 支持根据 MTU 分片：超过 MTU 的帧会被拆分为多个 RTP 包。
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

    /// 根据 MTU 将 payload 分片为多个 RTP 包。
    ///
    /// - 所有包使用相同 timestamp
    /// - 第一包 marker=false，最后一包 marker=true
    /// - sequence number 从 `start_seq` 开始递增（wrapping）
    ///
    /// 返回 (RTP 包列表, 使用的包数)。
    fn create_rtp_packets(
        payload_type: u8,
        start_seq: u16,
        timestamp: u32,
        ssrc: u32,
        payload: &[u8],
    ) -> Vec<Vec<u8>> {
        // 如果 payload 在单个 RTP 包内，直接返回单包
        if payload.len() <= MAX_RTP_PAYLOAD {
            return vec![Self::create_rtp_packet(
                payload_type,
                true, // marker
                start_seq,
                timestamp,
                ssrc,
                payload,
            )];
        }

        // 分片：每个包最多 MAX_RTP_PAYLOAD 字节
        let chunks: Vec<&[u8]> = payload.chunks(MAX_RTP_PAYLOAD).collect();
        let total = chunks.len();

        chunks
            .into_iter()
            .enumerate()
            .map(|(i, chunk)| {
                let seq = start_seq.wrapping_add(i as u16);
                // 最后一包 marker=true，其余 marker=false
                let marker = i == total - 1;
                Self::create_rtp_packet(payload_type, marker, seq, timestamp, ssrc, chunk)
            })
            .collect()
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

        // 根据 MTU 分片为多个 RTP 包
        let rtp_packets =
            Self::create_rtp_packets(payload_type, *seq, frame.timestamp, frame.ssrc, &frame.data);

        // 序列号递增（按分片数量）
        *seq = seq.wrapping_add(rtp_packets.len() as u16);

        // 每个 RTP 包封装为 interleaved 格式并写入输出缓冲
        for rtp in &rtp_packets {
            let interleaved = Self::create_interleaved(channel, rtp);
            self.output_buffer.extend_from_slice(&interleaved);
        }

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

    #[test]
    fn test_rtp_single_packet_no_fragment() {
        // payload <= MAX_RTP_PAYLOAD → 单包，marker=true
        let payload = vec![0x01; 100];
        let packets = RtspRemuxer::create_rtp_packets(96, 0, 9000, 12345, &payload);
        assert_eq!(packets.len(), 1);
        // marker bit set
        assert_eq!(packets[0][1] & 0x80, 0x80);
        // seq = 0
        assert_eq!(u16::from_be_bytes([packets[0][2], packets[0][3]]), 0);
    }

    #[test]
    fn test_rtp_fragmentation_multiple_packets() {
        // payload > MAX_RTP_PAYLOAD → 多包
        let payload = vec![0xAB; MAX_RTP_PAYLOAD * 2 + 100];
        let packets = RtspRemuxer::create_rtp_packets(96, 10, 9000, 12345, &payload);
        // 3 包：MAX, MAX, 100
        assert_eq!(packets.len(), 3);

        // 所有包 timestamp 相同
        for p in &packets {
            let ts = u32::from_be_bytes([p[4], p[5], p[6], p[7]]);
            assert_eq!(ts, 9000);
        }

        // 所有包 ssrc 相同
        for p in &packets {
            let ssrc = u32::from_be_bytes([p[8], p[9], p[10], p[11]]);
            assert_eq!(ssrc, 12345);
        }

        // sequence number 递增: 10, 11, 12
        assert_eq!(u16::from_be_bytes([packets[0][2], packets[0][3]]), 10);
        assert_eq!(u16::from_be_bytes([packets[1][2], packets[1][3]]), 11);
        assert_eq!(u16::from_be_bytes([packets[2][2], packets[2][3]]), 12);

        // 第一包 marker=false
        assert_eq!(packets[0][1] & 0x80, 0x00);
        // 中间包 marker=false
        assert_eq!(packets[1][1] & 0x80, 0x00);
        // 最后一包 marker=true
        assert_eq!(packets[2][1] & 0x80, 0x80);

        // 验证 payload 大小
        assert_eq!(packets[0].len() - RTP_HEADER_LEN, MAX_RTP_PAYLOAD);
        assert_eq!(packets[1].len() - RTP_HEADER_LEN, MAX_RTP_PAYLOAD);
        assert_eq!(packets[2].len() - RTP_HEADER_LEN, 100);

        // 验证 payload 内容
        assert!(packets[0][RTP_HEADER_LEN..].iter().all(|&b| b == 0xAB));
        assert!(packets[1][RTP_HEADER_LEN..].iter().all(|&b| b == 0xAB));
        assert!(packets[2][RTP_HEADER_LEN..].iter().all(|&b| b == 0xAB));
    }

    #[test]
    fn test_rtp_fragmentation_exact_boundary() {
        // payload 恰好 = MAX_RTP_PAYLOAD → 单包
        let payload = vec![0x01; MAX_RTP_PAYLOAD];
        let packets = RtspRemuxer::create_rtp_packets(96, 0, 9000, 12345, &payload);
        assert_eq!(packets.len(), 1);
        assert_eq!(packets[0][1] & 0x80, 0x80); // marker=true
    }

    #[test]
    fn test_rtp_fragmentation_one_over_boundary() {
        // payload = MAX_RTP_PAYLOAD + 1 → 2 包
        let payload = vec![0x01; MAX_RTP_PAYLOAD + 1];
        let packets = RtspRemuxer::create_rtp_packets(96, 0, 9000, 12345, &payload);
        assert_eq!(packets.len(), 2);
        // 第一包 marker=false
        assert_eq!(packets[0][1] & 0x80, 0x00);
        // 最后一包 marker=true
        assert_eq!(packets[1][1] & 0x80, 0x80);
        // 第二包 payload = 1 byte
        assert_eq!(packets[1].len() - RTP_HEADER_LEN, 1);
    }

    #[test]
    fn test_remuxer_push_large_frame() {
        // 通过 push_frame 验证大帧分片输出多个 interleaved 包
        let mut remuxer = RtspRemuxer::new();
        let large_data = vec![0x42; MAX_RTP_PAYLOAD * 2 + 50];
        let frame = MediaFrame::video(
            CodecType::H264,
            9000,
            bytes::Bytes::from(large_data),
            12345,
            true,
        );
        let output = remuxer.push_frame(&frame);

        // 解析输出中的多个 interleaved 包
        let mut offset = 0;
        let mut packets = Vec::new();
        while offset + 4 <= output.len() {
            assert_eq!(output[offset], b'$');
            let channel = output[offset + 1];
            let len = u16::from_be_bytes([output[offset + 2], output[offset + 3]]) as usize;
            assert_eq!(channel, 0); // video
            let rtp = &output[offset + 4..offset + 4 + len];
            packets.push(rtp.to_vec());
            offset += 4 + len;
        }
        assert_eq!(offset, output.len()); // 完全消费
        assert_eq!(packets.len(), 3); // 3 个分片

        // 验证 marker：前两个 false，最后一个 true
        assert_eq!(packets[0][1] & 0x80, 0x00);
        assert_eq!(packets[1][1] & 0x80, 0x00);
        assert_eq!(packets[2][1] & 0x80, 0x80);

        // 验证 seq 递增: 0, 1, 2
        assert_eq!(u16::from_be_bytes([packets[0][2], packets[0][3]]), 0);
        assert_eq!(u16::from_be_bytes([packets[1][2], packets[1][3]]), 1);
        assert_eq!(u16::from_be_bytes([packets[2][2], packets[2][3]]), 2);

        // 验证所有 timestamp 相同
        for p in &packets {
            let ts = u32::from_be_bytes([p[4], p[5], p[6], p[7]]);
            assert_eq!(ts, 9000);
        }

        // 验证所有 ssrc 相同
        for p in &packets {
            let ssrc = u32::from_be_bytes([p[8], p[9], p[10], p[11]]);
            assert_eq!(ssrc, 12345);
        }

        // 下一个帧的 seq 应从 3 开始
        let small_frame = MediaFrame::video(
            CodecType::H264,
            9001,
            bytes::Bytes::from(vec![0x01]),
            12346,
            true,
        );
        let out2 = remuxer.push_frame(&small_frame);
        let seq = u16::from_be_bytes([out2[6], out2[7]]);
        assert_eq!(seq, 3);
    }
}
