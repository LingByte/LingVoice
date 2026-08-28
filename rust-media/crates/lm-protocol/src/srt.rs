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

/// TS 同步字节（0x47）
const TS_SYNC_BYTE: u8 = 0x47;

/// TS 包大小（188 字节）
const TS_PKT_SIZE: usize = 188;

/// 视频 PID（与 ts.rs 中 VIDEO_PID 一致）
const TS_VIDEO_PID: u16 = 0x0100;

/// 音频 PID（与 ts.rs 中 AUDIO_PID 一致）
const TS_AUDIO_PID: u16 = 0x0101;

/// PES 视频 stream_id
const PES_STREAM_ID_VIDEO: u8 = 0xE0;

/// PES 音频 stream_id
const PES_STREAM_ID_AUDIO: u8 = 0xC0;

/// 检测 payload 是否为 MPEG-TS 格式
///
/// MPEG-TS 每个包 188 字节，首字节为 0x47 sync byte。
/// SRT 通常一次传输整数个 TS 包（如 7 个 = 1316 字节）。
fn is_mpeg_ts(payload: &[u8]) -> bool {
    if payload.is_empty() {
        return false;
    }
    // 首字节必须是 sync byte，且长度必须是 188 的整数倍
    payload[0] == TS_SYNC_BYTE && payload.len() % TS_PKT_SIZE == 0
}

/// 解码 PES 中的 PTS/DTS（5 字节）
fn decode_pts(b: &[u8]) -> u64 {
    ((b[0] as u64 >> 1) & 0x07) << 30
        | (b[1] as u64) << 22
        | ((b[2] as u64 >> 1) & 0x7F) << 15
        | (b[3] as u64) << 7
        | ((b[4] as u64 >> 1) & 0x7F)
}

/// 解析 PES 包，返回 (payload 数据, PTS)
///
/// 支持两种 PES 头格式：
/// - **标准 MPEG-2**（ISO/IEC 13818-1）：start_code(3) + stream_id(1) + length(2)
///   + flags1(1) + flags2(1, 含 PTS_DTS_flags) + header_data_length(1) + PTS/DTS + payload
/// - **ts.rs 简化格式**（crate 内 TsMuxer 生成）：start_code(3) + stream_id(1) + length(2)
///   + flags(1, 含 PTS/DTS) + header_data_length(1) + PTS/DTS + payload
///
/// 通过检查 byte[7] 的高 2 位来区分：标准格式中 byte[7] 含 PTS_DTS_flags（0x80/0xC0），
/// ts.rs 格式中 byte[7] 是较小的 header_data_length（< 0x40）。
fn parse_pes(data: &[u8]) -> (Vec<u8>, Option<u64>) {
    if data.len() < 8 {
        return (Vec::new(), None);
    }
    // 校验 PES start code: 0x00 0x00 0x01
    if data[0] != 0x00 || data[1] != 0x00 || data[2] != 0x01 {
        return (Vec::new(), None);
    }
    let _stream_id = data[3];
    let _pes_packet_length = u16::from_be_bytes([data[4], data[5]]);

    // 区分标准 MPEG-2 格式与 ts.rs 简化格式
    // 标准：byte[6] 标记位 '10' (0x80-0xBF)，byte[7] 高 2 位为 PTS_DTS_flags
    // ts.rs：byte[6] = 0x80 | PTS(0x80) | DTS(0x40)，byte[7] = header_data_length（小值）
    let is_standard = data[7] & 0xC0 != 0;

    let mut pts = None;
    let payload_start;

    if is_standard {
        // 标准 MPEG-2 PES
        let pts_dts_flags = data[7] >> 6;
        let pes_header_data_len = if data.len() > 8 { data[8] as usize } else { 0 };
        let mut offset = 9;
        // PTS_DTS_flags == 2 (PTS only) 或 3 (PTS + DTS)
        if pts_dts_flags >= 2 && offset + 5 <= data.len() {
            pts = Some(decode_pts(&data[offset..offset + 5]));
            offset += 5;
        }
        // DTS present (pts_dts_flags == 3)，跳过
        if pts_dts_flags == 3 && offset + 5 <= data.len() {
            offset += 5;
        }
        payload_start = 9 + pes_header_data_len;
    } else {
        // ts.rs 简化格式
        let flags = data[6];
        let pes_header_data_len = data[7] as usize;
        let mut offset = 8;
        // PTS present (flags & 0x80)
        if flags & 0x80 != 0 && offset + 5 <= data.len() {
            pts = Some(decode_pts(&data[offset..offset + 5]));
            offset += 5;
        }
        // DTS present (flags & 0x40)，跳过
        if flags & 0x40 != 0 && offset + 5 <= data.len() {
            offset += 5;
        }
        payload_start = 8 + pes_header_data_len;
    }

    if payload_start > data.len() {
        return (Vec::new(), pts);
    }
    (data[payload_start..].to_vec(), pts)
}

/// 跳过 TS payload 中的 pointer_field（仅 PSI 节或部分实现的 PES）
///
/// 标准 PES 包在 PUSI=1 时直接以 PES start code (0x00 0x00 0x01) 开头，无 pointer_field。
/// 但部分实现（含 crate 内 make_ts_packet）会插入 pointer_field=0x00。
/// 此函数检测并跳过 pointer_field，返回指向 PES start code 的切片。
fn skip_pointer_field(payload: &[u8]) -> &[u8] {
    // 检查是否直接以 PES start code 开头
    if payload.len() >= 3 && payload[0] == 0x00 && payload[1] == 0x00 && payload[2] == 0x01 {
        return payload;
    }
    // 否则假设首字节为 pointer_field，跳过 1 + pointer_field 字节
    if payload.is_empty() {
        return payload;
    }
    let pointer = payload[0] as usize;
    if 1 + pointer < payload.len() {
        &payload[1 + pointer..]
    } else {
        &payload[..0]
    }
}

/// 检测 H.264 Annex-B 数据是否为关键帧（包含 IDR NAL, type=5）
fn is_h264_keyframe(data: &[u8]) -> bool {
    let mut i = 0;
    while i + 3 < data.len() {
        // 4-byte start code: 0x00 0x00 0x00 0x01
        if i + 4 < data.len()
            && data[i] == 0
            && data[i + 1] == 0
            && data[i + 2] == 0
            && data[i + 3] == 1
        {
            let nal_type = data[i + 4] & 0x1F;
            if nal_type == 5 {
                // IDR slice
                return true;
            }
            i += 4;
        } else if data[i] == 0 && data[i + 1] == 0 && data[i + 2] == 1 {
            // 3-byte start code: 0x00 0x00 0x01
            let nal_type = data[i + 3] & 0x1F;
            if nal_type == 5 {
                return true;
            }
            i += 3;
        } else {
            i += 1;
        }
    }
    false
}

/// SRT Demuxer
///
/// 解析 SRT payload 中的媒体数据，支持两种格式：
/// - **MPEG-TS**（SRT 标准）：188 字节 TS 包，内含 PES
/// - **RTP**（兼容模式）：直接 RTP 解包
pub struct SrtDemuxer {
    video_depacketizer: Box<dyn lm_core::Depacketizer>,
    audio_depacketizer: Box<dyn lm_core::Depacketizer>,
    video_codec: CodecType,
    audio_codec: CodecType,
    buffer: Vec<u8>,
    // TS PES 组装状态
    video_pes: Vec<u8>,
    video_pts: Option<u64>,
    audio_pes: Vec<u8>,
    audio_pts: Option<u64>,
}

impl SrtDemuxer {
    pub fn new(video_codec: CodecType, audio_codec: CodecType) -> Self {
        Self {
            video_depacketizer: create_depacketizer(video_codec),
            audio_depacketizer: create_depacketizer(audio_codec),
            video_codec,
            audio_codec,
            buffer: Vec::new(),
            video_pes: Vec::new(),
            video_pts: None,
            audio_pes: Vec::new(),
            audio_pts: None,
        }
    }

    /// 刷新已组装的视频 PES 为 MediaFrame
    fn flush_video_pes(&mut self) -> Option<MediaFrame> {
        if self.video_pes.is_empty() {
            return None;
        }
        let data = std::mem::take(&mut self.video_pes);
        let pts = self.video_pts.take().unwrap_or(0) as u32;
        let keyframe = is_h264_keyframe(&data);
        Some(MediaFrame::video(
            self.video_codec,
            pts,
            bytes::Bytes::from(data),
            0,
            keyframe,
        ))
    }

    /// 刷新已组装的音频 PES 为 MediaFrame
    fn flush_audio_pes(&mut self) -> Option<MediaFrame> {
        if self.audio_pes.is_empty() {
            return None;
        }
        let data = std::mem::take(&mut self.audio_pes);
        let pts = self.audio_pts.take().unwrap_or(0) as u32;
        Some(MediaFrame::audio(
            self.audio_codec,
            pts,
            bytes::Bytes::from(data),
            0,
        ))
    }

    /// 解析 MPEG-TS payload，提取 PES 并组装为 MediaFrame
    fn demux_ts(&mut self, payload: &[u8]) -> Vec<MediaFrame> {
        let mut frames = Vec::new();
        let mut offset = 0;

        while offset + TS_PKT_SIZE <= payload.len() {
            let pkt = &payload[offset..offset + TS_PKT_SIZE];
            offset += TS_PKT_SIZE;

            // 校验 sync byte
            if pkt[0] != TS_SYNC_BYTE {
                continue;
            }

            // 解析 TS 头部（4 字节）
            let pusi = pkt[1] & 0x40 != 0; // payload_unit_start_indicator
            let pid = (((pkt[1] & 0x1F) as u16) << 8) | (pkt[2] as u16);
            let adaptation_control = (pkt[3] >> 4) & 0x03;

            // 跳过 adaptation field
            let mut payload_offset = 4;
            if adaptation_control == 0x02 || adaptation_control == 0x03 {
                // 存在 adaptation field
                if pkt.len() < 5 {
                    continue;
                }
                let adapt_len = pkt[4] as usize;
                payload_offset = 5 + adapt_len;
            }
            // adaptation_control == 0x02 表示仅 adaptation，无 payload
            if adaptation_control == 0x02 {
                continue;
            }
            if payload_offset >= TS_PKT_SIZE {
                continue;
            }
            let ts_payload = &pkt[payload_offset..TS_PKT_SIZE];

            // 只处理视频/音频 PID，忽略 PAT/PMT/PCR 等
            if pid != TS_VIDEO_PID && pid != TS_AUDIO_PID {
                continue;
            }

            if pusi {
                // 新 PES 开始：先 flush 旧 PES
                if pid == TS_VIDEO_PID {
                    if let Some(f) = self.flush_video_pes() {
                        frames.push(f);
                    }
                    // PES 包直接以 start code 开头（无 pointer_field）
                    let (pes_data, pts) = parse_pes(ts_payload);
                    self.video_pts = pts;
                    self.video_pes = pes_data;
                } else {
                    if let Some(f) = self.flush_audio_pes() {
                        frames.push(f);
                    }
                    // 音频 PES：部分实现（含 crate 内 make_ts_packet）在 PUSI 时
                    // 插入了 pointer_field（0x00），需要跳过后再解析 PES。
                    let pes_payload = skip_pointer_field(ts_payload);
                    let (pes_data, pts) = parse_pes(pes_payload);
                    self.audio_pts = pts;
                    self.audio_pes = pes_data;
                }
            } else {
                // PES 续包：追加 payload
                if pid == TS_VIDEO_PID {
                    self.video_pes.extend_from_slice(ts_payload);
                } else if pid == TS_AUDIO_PID {
                    self.audio_pes.extend_from_slice(ts_payload);
                }
            }
        }

        frames
    }

    /// 解析 RTP payload（兼容模式）
    fn demux_rtp(&mut self, payload: &[u8]) -> Vec<MediaFrame> {
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
        let result =
            self.video_depacketizer
                .push_packet(rtp_payload, marker, sequence_number, timestamp);

        if matches!(result, lm_core::DepacketizeResult::FrameComplete) {
            if let Some(frame) = self.video_depacketizer.take_frame() {
                frames.push(frame);
            }
        }

        frames
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

        // SRT 标准传输 MPEG-TS，但部分实现使用 RTP 封装。
        // 检测 payload 格式并分发到对应解析器。
        if is_mpeg_ts(payload) {
            self.demux_ts(payload)
        } else {
            self.demux_rtp(payload)
        }
    }

    fn reset(&mut self) {
        self.buffer.clear();
        self.video_depacketizer.reset();
        self.audio_depacketizer.reset();
        self.video_pes.clear();
        self.video_pts = None;
        self.audio_pes.clear();
        self.audio_pts = None;
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

        let frame = MediaFrame::audio(CodecType::Opus, 4800, bytes::Bytes::from(vec![0x01]), 1);

        remuxer.push_frame(&frame);
        let output2 = remuxer.push_frame(&frame);

        // Second packet should have sequence = 1
        let seq = u32::from_be_bytes([output2[0], output2[1], output2[2], output2[3]]);
        assert_eq!(seq, 1);
    }

    #[test]
    fn test_srt_remuxer_reset() {
        let mut remuxer = SrtRemuxer::new();

        let frame = MediaFrame::audio(CodecType::Opus, 4800, bytes::Bytes::from(vec![0x01]), 1);

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

    // ===== MPEG-TS payload 测试 =====

    /// 辅助：构造 SRT data packet（16 字节头 + payload）
    fn make_srt_packet(payload: &[u8]) -> Vec<u8> {
        let mut pkt = Vec::with_capacity(16 + payload.len());
        pkt.extend_from_slice(&0u32.to_be_bytes()); // sequence
        pkt.extend_from_slice(&1u32.to_be_bytes()); // message number
        pkt.extend_from_slice(&0u32.to_be_bytes()); // timestamp
        pkt.extend_from_slice(&0u32.to_be_bytes()); // dst socket id
        pkt.extend_from_slice(payload);
        pkt
    }

    #[test]
    fn test_is_mpeg_ts_detection() {
        // 188 字节、首字节 0x47 → MPEG-TS
        let mut ts = vec![0u8; 188];
        ts[0] = 0x47;
        assert!(is_mpeg_ts(&ts));

        // 7 个 TS 包（1316 字节）
        let mut ts7 = vec![0u8; 188 * 7];
        ts7[0] = 0x47;
        assert!(is_mpeg_ts(&ts7));

        // 非整数倍 → 不是 MPEG-TS
        let mut bad = vec![0u8; 189];
        bad[0] = 0x47;
        assert!(!is_mpeg_ts(&bad));

        // 首字节非 0x47 → 不是 MPEG-TS
        assert!(!is_mpeg_ts(&[0x80; 188]));

        // 空 → 不是
        assert!(!is_mpeg_ts(&[]));
    }

    #[test]
    fn test_srt_demuxer_ts_video_frame() {
        // 使用 TsMuxer 生成合法 MPEG-TS 流
        let mut muxer = crate::ts::TsMuxer::new(CodecType::H264, CodecType::Opus);
        let pat_pmt = muxer.write_pat_pmt();
        assert_eq!(pat_pmt.len() % TS_PKT_SIZE, 0);

        // IDR 关键帧（NAL type 5）
        let frame = MediaFrame::video(
            CodecType::H264,
            9000,
            bytes::Bytes::from(vec![0, 0, 0, 1, 0x65, 0x88, 0x80, 0x40]),
            1,
            true,
        );
        let ts_data = muxer.write_frame(&frame);
        assert_eq!(ts_data.len() % TS_PKT_SIZE, 0);

        // 拼接 PAT/PMT + 视频帧
        let mut payload = Vec::new();
        payload.extend_from_slice(&pat_pmt);
        payload.extend_from_slice(&ts_data);

        let srt_pkt = make_srt_packet(&payload);

        let mut demuxer = SrtDemuxer::new(CodecType::H264, CodecType::Opus);
        let frames = demuxer.push_data(&srt_pkt);

        // 第一帧的 PES 在第二个 PUSI 到来时才 flush；
        // 这里只有一帧视频，PES 仍在缓冲中，需再送一帧触发 flush。
        assert!(frames.is_empty());

        // 送第二帧触发第一帧 flush
        let frame2 = MediaFrame::video(
            CodecType::H264,
            18000,
            bytes::Bytes::from(vec![0, 0, 0, 1, 0x65, 0xAA]),
            1,
            true,
        );
        let ts_data2 = muxer.write_frame(&frame2);
        let srt_pkt2 = make_srt_packet(&ts_data2);
        let frames2 = demuxer.push_data(&srt_pkt2);

        assert!(!frames2.is_empty(), "应至少产出一帧");
        let f = &frames2[0];
        assert_eq!(f.kind, TrackKind::Video);
        assert_eq!(f.codec, CodecType::H264);
        // 关键帧检测
        assert!(f.keyframe);
        // 数据应包含原始 NAL（Annex-B start code + 0x65）
        assert!(f.data.starts_with(&[0, 0, 0, 1, 0x65]));
    }

    #[test]
    fn test_srt_demuxer_ts_audio_frame() {
        let mut muxer = crate::ts::TsMuxer::new(CodecType::H264, CodecType::Aac);

        // 先写一帧视频（触发 PAT/PMT + 视频 PES 开始）
        let vframe = MediaFrame::video(
            CodecType::H264,
            9000,
            bytes::Bytes::from(vec![0, 0, 0, 1, 0x65, 0x88]),
            1,
            true,
        );
        let _ = muxer.write_frame(&vframe);

        // 音频帧
        let aframe = MediaFrame::audio(
            CodecType::Aac,
            4800,
            bytes::Bytes::from(vec![0xFF, 0xF1, 0x50, 0x80, 0x01, 0x02]),
            1,
        );
        let ts_audio = muxer.write_frame(&aframe);
        assert_eq!(ts_audio.len() % TS_PKT_SIZE, 0);

        let srt_pkt = make_srt_packet(&ts_audio);
        let mut demuxer = SrtDemuxer::new(CodecType::H264, CodecType::Aac);
        let frames = demuxer.push_data(&srt_pkt);

        // 视频帧 PES 仍在缓冲（无后续 PUSI），音频同理。
        // 送第二帧音频触发音频 flush。
        let aframe2 = MediaFrame::audio(
            CodecType::Aac,
            7200,
            bytes::Bytes::from(vec![0xFF, 0xF1, 0x50, 0x80, 0x03, 0x04]),
            1,
        );
        let ts_audio2 = muxer.write_frame(&aframe2);
        let srt_pkt2 = make_srt_packet(&ts_audio2);
        let frames2 = demuxer.push_data(&srt_pkt2);

        // 应产出音频帧
        let audio_frames: Vec<_> = frames2
            .iter()
            .filter(|f| f.kind == TrackKind::Audio)
            .collect();
        assert!(
            !audio_frames.is_empty(),
            "应至少产出一帧音频, got {} frames total",
            frames2.len()
        );
        let af = audio_frames[0];
        assert_eq!(af.codec, CodecType::Aac);
        assert!(af.data.starts_with(&[0xFF, 0xF1]));
    }

    #[test]
    fn test_srt_demuxer_ts_multi_packet_pes() {
        // 测试跨多个 TS 包的 PES 重组
        let mut muxer = crate::ts::TsMuxer::new(CodecType::H264, CodecType::Opus);
        let _ = muxer.write_pat_pmt();

        // 构造大于 184 字节的视频帧，强制跨包
        let mut nal_data = vec![0, 0, 0, 1, 0x65]; // IDR
        nal_data.extend(std::iter::repeat(0xAA).take(400));
        let frame = MediaFrame::video(
            CodecType::H264,
            5000,
            bytes::Bytes::from(nal_data.clone()),
            1,
            true,
        );
        let ts1 = muxer.write_frame(&frame);
        assert!(ts1.len() > TS_PKT_SIZE, "应跨多个 TS 包");

        // 第二帧触发第一帧 flush
        let frame2 = MediaFrame::video(
            CodecType::H264,
            10000,
            bytes::Bytes::from(vec![0, 0, 0, 1, 0x65, 0xBB]),
            1,
            true,
        );
        let ts2 = muxer.write_frame(&frame2);

        let mut payload = Vec::new();
        payload.extend_from_slice(&ts1);
        payload.extend_from_slice(&ts2);

        let srt_pkt = make_srt_packet(&payload);
        let mut demuxer = SrtDemuxer::new(CodecType::H264, CodecType::Opus);
        let frames = demuxer.push_data(&srt_pkt);

        assert!(!frames.is_empty(), "应产出重组后的帧");
        let f = &frames[0];
        assert!(f.keyframe);
        // 重组后的数据应包含完整的原始 NAL
        assert!(f.data.starts_with(&[0, 0, 0, 1, 0x65]));
    }

    #[test]
    fn test_srt_demuxer_ts_reset() {
        let mut demuxer = SrtDemuxer::new(CodecType::H264, CodecType::Opus);
        // 模拟部分 PES 组装状态
        demuxer.video_pes.extend_from_slice(&[1, 2, 3]);
        demuxer.video_pts = Some(1000);
        demuxer.audio_pes.extend_from_slice(&[4, 5]);
        demuxer.audio_pts = Some(2000);

        demuxer.reset();
        assert!(demuxer.video_pes.is_empty());
        assert!(demuxer.video_pts.is_none());
        assert!(demuxer.audio_pes.is_empty());
        assert!(demuxer.audio_pts.is_none());
    }

    #[test]
    fn test_parse_pes_pts() {
        // 构造一个带 PTS 的 PES 包
        // start_code(3) + stream_id(1) + length(2) + flags(1) + header_len(1) + PTS(5) + payload
        let pts_value: u64 = 9000;
        // 手工编码 PTS (5 bytes)
        let pts_bytes = [
            (((pts_value >> 30) & 0x07) << 1 | 0x21) as u8,
            ((pts_value >> 22) & 0xFF) as u8,
            (((pts_value >> 14) & 0x7F) << 1 | 0x01) as u8,
            ((pts_value >> 7) & 0xFF) as u8,
            (((pts_value << 1) & 0xFE) | 0x01) as u8,
        ];
        let mut pes = vec![0x00, 0x00, 0x01, 0xE0, 0x00, 0x00, 0x80, 0x05];
        pes.extend_from_slice(&pts_bytes);
        pes.extend_from_slice(&[0xAA, 0xBB, 0xCC]);

        let (data, pts) = parse_pes(&pes);
        assert_eq!(pts, Some(9000));
        assert_eq!(data, vec![0xAA, 0xBB, 0xCC]);
    }

    #[test]
    fn test_is_h264_keyframe_detection() {
        // IDR (NAL type 5) → 关键帧
        let idr = vec![0, 0, 0, 1, 0x65, 0x88];
        assert!(is_h264_keyframe(&idr));

        // 非 IDR (NAL type 1) → 非关键帧
        let non_idr = vec![0, 0, 0, 1, 0x21, 0x88];
        assert!(!is_h264_keyframe(&non_idr));

        // 3-byte start code + IDR
        let idr3 = vec![0, 0, 1, 0x65];
        assert!(is_h264_keyframe(&idr3));

        // 空数据
        assert!(!is_h264_keyframe(&[]));
    }
}
