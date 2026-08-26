//! RTP Packetizer — 将完整编码帧拆分为 RTP 包序列
//!
//! 与 Depacketizer 相反：
//! - Depacketizer: RTP 包序列 → 完整编码帧
//! - Packetizer:   完整编码帧 → RTP 包序列
//!
//! 支持的编解码器：
//! - VP8: 添加 payload descriptor (RFC 7741)，按 MTU 分片
//! - H264: 按 MTU 分片为 FU-A / STAP-A / Single NALU (RFC 6184)
//! - Opus: 一个 RTP 包 = 一帧

use bytes::Bytes;
use lm_core::CodecType;

/// RTP 包（packetizer 输出）
#[derive(Debug, Clone)]
pub struct RtpPacketOut {
    /// RTP payload（含 codec-specific descriptor）
    pub payload: Bytes,
    /// RTP marker 位（帧最后一包 = true）
    pub marker: bool,
    /// RTP 时间戳
    pub timestamp: u32,
}

/// RTP packetizer trait
pub trait Packetizer: Send + Sync {
    /// 将一个完整编码帧打包为 RTP 包序列
    ///
    /// `frame_data` 是完整的编码帧（VP8 payload / H.264 Annex-B NALU）
    /// `timestamp` 是 RTP 时间戳
    /// `keyframe` 是否为关键帧
    fn packetize(
        &mut self,
        frame_data: &[u8],
        timestamp: u32,
        keyframe: bool,
    ) -> Vec<RtpPacketOut>;

    /// 编解码类型
    fn codec_type(&self) -> CodecType;
}

/// RTP MTU（最大传输单元），留余量给 RTP header (12 bytes) + IP/UDP header
const RTP_MTU: usize = 1200;

// ============================================================================
// VP8 Packetizer (RFC 7741)
// ============================================================================

/// VP8 RTP packetizer
pub struct Vp8Packetizer {
    mtu: usize,
}

impl Vp8Packetizer {
    pub fn new() -> Self {
        Self { mtu: RTP_MTU }
    }
}

impl Default for Vp8Packetizer {
    fn default() -> Self {
        Self::new()
    }
}

impl Packetizer for Vp8Packetizer {
    fn packetize(
        &mut self,
        frame_data: &[u8],
        timestamp: u32,
        _keyframe: bool,
    ) -> Vec<RtpPacketOut> {
        // VP8 payload descriptor:
        // byte 0: X=0, R=0, N=0, S=1, PartID=0 (start of partition)
        // 后续包: S=0
        let descriptor_len = 1;
        let max_payload = self.mtu - descriptor_len;

        if frame_data.len() <= max_payload {
            // 单包
            let mut payload = Vec::with_capacity(descriptor_len + frame_data.len());
            payload.push(0x10); // S=1, PartID=0
            payload.extend_from_slice(frame_data);
            return vec![RtpPacketOut {
                payload: Bytes::from(payload),
                marker: true,
                timestamp,
            }];
        }

        // 多包分片
        let mut packets = Vec::new();
        let mut offset = 0;
        while offset < frame_data.len() {
            let end = (offset + max_payload).min(frame_data.len());
            let chunk = &frame_data[offset..end];
            let is_first = offset == 0;
            let is_last = end == frame_data.len();

            let mut payload = Vec::with_capacity(descriptor_len + chunk.len());
            // VP8 descriptor: S bit set only on first packet
            payload.push(if is_first { 0x10 } else { 0x00 });
            payload.extend_from_slice(chunk);

            packets.push(RtpPacketOut {
                payload: Bytes::from(payload),
                marker: is_last,
                timestamp,
            });

            offset = end;
        }
        packets
    }

    fn codec_type(&self) -> CodecType {
        CodecType::Vp8
    }
}

// ============================================================================
// H264 Packetizer (RFC 6184)
// ============================================================================

/// H264 NALU 类型
fn h264_nalu_type(nalu: &[u8]) -> u8 {
    if nalu.is_empty() {
        return 0;
    }
    nalu[0] & 0x1f
}

/// 去除 Annex-B 起始码，提取 NALU
fn split_annexb_nalus(data: &[u8]) -> Vec<&[u8]> {
    let mut nalus = Vec::new();
    let mut i = 0;
    while i < data.len() {
        // 查找起始码 (0x000001 或 0x00000001)
        let start = if i + 4 <= data.len() && data[i..i + 4] == [0, 0, 0, 1] {
            i + 4
        } else if i + 3 <= data.len() && data[i..i + 3] == [0, 0, 1] {
            i + 3
        } else {
            i += 1;
            continue;
        };

        // 查找下一个起始码
        let mut end = start;
        while end + 3 <= data.len() {
            // 检查 3 字节起始码
            if data[end..end + 3] == [0, 0, 1] {
                break;
            }
            // 检查 4 字节起始码
            if end + 4 <= data.len() && data[end..end + 4] == [0, 0, 0, 1] {
                break;
            }
            end += 1;
        }
        // 如果没找到下一个起始码，取到末尾
        if end + 3 > data.len() {
            end = data.len();
        }

        if start < end {
            nalus.push(&data[start..end]);
        }
        i = end;
    }
    nalus
}

/// H264 RTP packetizer
pub struct H264Packetizer {
    mtu: usize,
}

impl H264Packetizer {
    pub fn new() -> Self {
        Self { mtu: RTP_MTU }
    }
}

impl Default for H264Packetizer {
    fn default() -> Self {
        Self::new()
    }
}

impl Packetizer for H264Packetizer {
    fn packetize(
        &mut self,
        frame_data: &[u8],
        timestamp: u32,
        _keyframe: bool,
    ) -> Vec<RtpPacketOut> {
        let nalus = split_annexb_nalus(frame_data);
        if nalus.is_empty() {
            return Vec::new();
        }

        let mut packets = Vec::new();
        let max_payload = self.mtu;

        for (idx, &nalu) in nalus.iter().enumerate() {
            let nalu_type = h264_nalu_type(nalu);
            let nalu_size = nalu.len();
            let is_last_nalu = idx == nalus.len() - 1;

            if nalu_size <= max_payload {
                // Single NALU packet
                packets.push(RtpPacketOut {
                    payload: Bytes::from(nalu.to_vec()),
                    marker: is_last_nalu,
                    timestamp,
                });
            } else {
                // FU-A 分片
                let fu_header_overhead = 2; // FU indicator + FU header
                let max_fu_payload = max_payload - fu_header_overhead;
                let fu_indicator = (nalu[0] & 0xe0) | 28; // NALU type 28 = FU-A
                let original_type = nalu_type;

                let mut offset = 1; // 跳过 NALU header byte
                let mut first_fu = true;
                while offset < nalu.len() {
                    let end = (offset + max_fu_payload).min(nalu.len());
                    let chunk = &nalu[offset..end];
                    let is_last_fu = end == nalu.len();

                    let mut payload = Vec::with_capacity(fu_header_overhead + chunk.len());
                    payload.push(fu_indicator);
                    // FU header: S(1) E(1) R(1) Type(5)
                    let mut fu_header = original_type;
                    if first_fu {
                        fu_header |= 0x80; // S bit
                    }
                    if is_last_fu {
                        fu_header |= 0x40; // E bit
                    }
                    payload.push(fu_header);
                    payload.extend_from_slice(chunk);

                    packets.push(RtpPacketOut {
                        payload: Bytes::from(payload),
                        marker: is_last_nalu && is_last_fu,
                        timestamp,
                    });

                    offset = end;
                    first_fu = false;
                }
            }
        }

        packets
    }

    fn codec_type(&self) -> CodecType {
        CodecType::H264
    }
}

// ============================================================================
// Opus Packetizer（一个 RTP 包 = 一帧）
// ============================================================================

pub struct OpusPacketizer;

impl OpusPacketizer {
    pub fn new() -> Self {
        Self
    }
}

impl Default for OpusPacketizer {
    fn default() -> Self {
        Self::new()
    }
}

impl Packetizer for OpusPacketizer {
    fn packetize(
        &mut self,
        frame_data: &[u8],
        timestamp: u32,
        _keyframe: bool,
    ) -> Vec<RtpPacketOut> {
        vec![RtpPacketOut {
            payload: Bytes::from(frame_data.to_vec()),
            marker: true,
            timestamp,
        }]
    }

    fn codec_type(&self) -> CodecType {
        CodecType::Opus
    }
}

// ============================================================================
// 工厂函数
// ============================================================================

/// 创建指定编解码器的 packetizer
pub fn create_packetizer(codec: CodecType) -> Box<dyn Packetizer> {
    match codec {
        CodecType::Vp8 => Box::new(Vp8Packetizer::new()),
        CodecType::H264 => Box::new(H264Packetizer::new()),
        CodecType::Opus => Box::new(OpusPacketizer::new()),
        CodecType::Vp9 => Box::new(Vp8Packetizer::new()), // 简化：复用 VP8
        CodecType::H265 | CodecType::Av1 => Box::new(H264Packetizer::new()), // 简化
        _ => Box::new(OpusPacketizer::new()), // 音频默认
    }
}

#[cfg(test)]
mod packetizer_tests {
    use super::*;

    #[test]
    fn test_vp8_packetizer_single_packet() {
        let mut pkt = Vp8Packetizer::new();
        let data = vec![0u8; 100]; // 小帧
        let packets = pkt.packetize(&data, 9000, true);
        assert_eq!(packets.len(), 1);
        assert!(packets[0].marker);
        // VP8 descriptor: S=1
        assert_eq!(packets[0].payload[0] & 0x10, 0x10);
    }

    #[test]
    fn test_vp8_packetizer_multi_packet() {
        let mut pkt = Vp8Packetizer { mtu: 100 };
        let data = vec![0u8; 500]; // 大帧，需要分片
        let packets = pkt.packetize(&data, 9000, true);
        assert!(packets.len() > 1);
        // 第一包 S=1
        assert_eq!(packets[0].payload[0] & 0x10, 0x10);
        // 后续包 S=0
        assert_eq!(packets[1].payload[0] & 0x10, 0x00);
        // 最后一包 marker=true
        assert!(packets.last().unwrap().marker);
    }

    #[test]
    fn test_h264_packetizer_single_nalu() {
        let mut pkt = H264Packetizer::new();
        // Annex-B 格式：起始码 + NALU
        let data = [0, 0, 0, 1, 0x65, 0xAA, 0xBB]; // IDR slice
        let packets = pkt.packetize(&data, 9000, true);
        assert_eq!(packets.len(), 1);
        assert!(packets[0].marker);
    }

    #[test]
    fn test_h264_packetizer_fu_a_fragmentation() {
        let mut pkt = H264Packetizer { mtu: 100 };
        // 大 NALU（需要 FU-A 分片）
        let mut data = vec![0, 0, 0, 1, 0x65]; // IDR slice header
        data.extend(vec![0xAA; 300]); // 大 payload
        let packets = pkt.packetize(&data, 9000, true);
        assert!(packets.len() > 1);
        // 第一包 FU indicator type = 28 (FU-A)
        assert_eq!(packets[0].payload[0] & 0x1f, 28);
        // 第一包 FU header S=1
        assert_eq!(packets[0].payload[1] & 0x80, 0x80);
        // 最后一包 FU header E=1
        assert_eq!(packets.last().unwrap().payload[1] & 0x40, 0x40);
        // 最后一包 marker=true
        assert!(packets.last().unwrap().marker);
    }

    #[test]
    fn test_h264_packetizer_multi_nalu() {
        let mut pkt = H264Packetizer::new();
        // SPS + PPS + IDR
        let mut data = vec![0, 0, 0, 1, 0x67]; // SPS
        data.extend(vec![0x11; 10]);
        data.extend([0, 0, 0, 1, 0x68]); // PPS
        data.extend(vec![0x22; 5]);
        data.extend([0, 0, 0, 1, 0x65]); // IDR
        data.extend(vec![0x33; 20]);

        let packets = pkt.packetize(&data, 9000, true);
        assert!(packets.len() >= 3); // 至少 3 个 NALU
        // 最后一包 marker=true
        assert!(packets.last().unwrap().marker);
    }

    #[test]
    fn test_opus_packetizer() {
        let mut pkt = OpusPacketizer::new();
        let data = vec![0xAA; 80];
        let packets = pkt.packetize(&data, 9000, false);
        assert_eq!(packets.len(), 1);
        assert!(packets[0].marker);
    }

    #[test]
    fn test_create_packetizer() {
        let vp8 = create_packetizer(CodecType::Vp8);
        assert_eq!(vp8.codec_type(), CodecType::Vp8);

        let h264 = create_packetizer(CodecType::H264);
        assert_eq!(h264.codec_type(), CodecType::H264);

        let opus = create_packetizer(CodecType::Opus);
        assert_eq!(opus.codec_type(), CodecType::Opus);
    }

    #[test]
    fn test_split_annexb_nalus() {
        // 3 字节起始码
        let data = [0, 0, 1, 0x67, 0xAA, 0xBB, 0, 0, 1, 0x68, 0xCC];
        let nalus = split_annexb_nalus(&data);
        assert_eq!(nalus.len(), 2);
        assert_eq!(nalus[0], &[0x67, 0xAA, 0xBB]);
        assert_eq!(nalus[1], &[0x68, 0xCC]);
    }

    #[test]
    fn test_split_annexb_4byte_startcode() {
        // 4 字节起始码
        let data = [0, 0, 0, 1, 0x67, 0xAA, 0, 0, 0, 1, 0x68, 0xCC];
        let nalus = split_annexb_nalus(&data);
        assert_eq!(nalus.len(), 2);
        assert_eq!(nalus[0], &[0x67, 0xAA]);
        assert_eq!(nalus[1], &[0x68, 0xCC]);
    }

    #[test]
    fn test_vp8_roundtrip_depacketize_packetize() {
        use lm_core::Depacketizer;
        // packetize → depacketize roundtrip
        let original = vec![0xAB; 2000]; // 大帧
        let mut pkt = Vp8Packetizer { mtu: 500 };
        let packets = pkt.packetize(&original, 9000, true);

        let mut dep = crate::Vp8Depacketizer::new();
        let mut got_frame = false;
        for (seq, p) in packets.iter().enumerate() {
            let result = dep.push_packet(&p.payload, p.marker, seq as u16, p.timestamp);
            if result == lm_core::DepacketizeResult::FrameComplete {
                let frame = dep.take_frame().unwrap();
                assert_eq!(frame.data.as_ref(), original.as_slice());
                got_frame = true;
            }
        }
        assert!(got_frame, "depacketizer should produce complete frame");
    }

    #[test]
    fn test_h264_roundtrip_depacketize_packetize() {
        use lm_core::Depacketizer;
        // 构造一个 H.264 帧（SPS + PPS + IDR）
        let mut original = vec![0, 0, 0, 1, 0x67]; // SPS
        original.extend(vec![0x11; 8]);
        original.extend([0, 0, 0, 1, 0x68]); // PPS
        original.extend(vec![0x22; 4]);
        original.extend([0, 0, 0, 1, 0x65]); // IDR
        original.extend(vec![0x33; 100]); // IDR payload

        let mut pkt = H264Packetizer { mtu: 50 }; // 小 MTU 触发 FU-A
        let packets = pkt.packetize(&original, 9000, true);

        let mut dep = crate::H264Depacketizer::new();
        let mut got_frame = false;
        for (seq, p) in packets.iter().enumerate() {
            let result = dep.push_packet(&p.payload, p.marker, seq as u16, p.timestamp);
            if result == lm_core::DepacketizeResult::FrameComplete {
                let _frame = dep.take_frame().unwrap();
                got_frame = true;
            }
        }
        assert!(got_frame, "H264 depacketizer should produce complete frame");
    }
}
