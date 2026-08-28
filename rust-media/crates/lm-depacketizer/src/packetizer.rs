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
    fn packetize(&mut self, frame_data: &[u8], timestamp: u32, keyframe: bool)
        -> Vec<RtpPacketOut>;

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
// VP9 Packetizer (RFC 7740 / draft-ietf-payload-vp9)
// ============================================================================

/// VP9 RTP packetizer (RFC 7740)
///
/// VP9 payload descriptor:
/// ```text
///  0 1 2 3 4 5 6 7
/// +-+-+-+-+-+-+-+-+
/// |I|P|L|F|B|E|V|U|  (REQUIRED)
/// +-+-+-+-+-+-+-+-+
/// ```
pub struct Vp9Packetizer {
    mtu: usize,
    picture_id: u16,
}

impl Vp9Packetizer {
    pub fn new() -> Self {
        Self {
            mtu: RTP_MTU,
            picture_id: 0,
        }
    }

    /// 构建 VP9 RTP payload descriptor
    /// I=1 (PictureID), P (inter), B (begin), E (end), V (keyframe)
    fn build_descriptor(
        &self,
        keyframe: bool,
        beginning: bool,
        end: bool,
        picture_id: u16,
    ) -> Vec<u8> {
        let mut desc = Vec::with_capacity(4);
        let mut flags = 0u8;
        flags |= 0x80; // I = 1 (Picture ID present)
        if !keyframe {
            flags |= 0x40; // P = 1 (inter-picture predicted)
        }
        if beginning {
            flags |= 0x04; // B = 1
        }
        if end {
            flags |= 0x02; // E = 1
        }
        // V bit: 0 = keyframe, 1 = non-keyframe (inverted logic)
        if !keyframe {
            flags |= 0x01; // V = 1
        }
        desc.push(flags);

        // Picture ID: use 16-bit if > 127, else 7-bit
        if picture_id > 127 {
            desc.push((picture_id >> 8) as u8 | 0x80); // M=1, high byte
            desc.push((picture_id & 0xFF) as u8); // low byte
        } else {
            desc.push((picture_id & 0x7F) as u8); // M=0, 7-bit
        }

        desc
    }
}

impl Default for Vp9Packetizer {
    fn default() -> Self {
        Self::new()
    }
}

impl Packetizer for Vp9Packetizer {
    fn packetize(
        &mut self,
        frame_data: &[u8],
        timestamp: u32,
        keyframe: bool,
    ) -> Vec<RtpPacketOut> {
        let pic_id = self.picture_id;
        self.picture_id = (self.picture_id + 1) & 0x7FFF;

        // 先用最大 descriptor 长度估算
        let max_desc_len = 4; // flags + 2 bytes picture ID (worst case)
        let max_payload = self.mtu.saturating_sub(max_desc_len);

        if frame_data.len() <= max_payload {
            // 单包: B=1, E=1
            let desc = self.build_descriptor(keyframe, true, true, pic_id);
            let mut payload = Vec::with_capacity(desc.len() + frame_data.len());
            payload.extend_from_slice(&desc);
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

            let desc = self.build_descriptor(keyframe, is_first, is_last, pic_id);
            let mut payload = Vec::with_capacity(desc.len() + chunk.len());
            payload.extend_from_slice(&desc);
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
        CodecType::Vp9
    }
}

// ============================================================================
// H265/HEVC Packetizer (RFC 7798)
// ============================================================================

/// H265 NALU header (2 bytes):
/// ```text
///  0               1
///  0 1 2 3 4 5 6 7 0 1 2 3 4 5 6 7
/// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
/// |F|   Type    |  Layer ID  | TID|
/// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
/// ```
fn h265_nalu_type(nalu: &[u8]) -> u8 {
    if nalu.is_empty() {
        return 0;
    }
    (nalu[0] >> 1) & 0x3F
}

/// H265/HEVC RTP packetizer (RFC 7798)
///
/// Supports: Single NALU, Fragmentation Unit (FU), Aggregation Packet (AP)
pub struct H265Packetizer {
    mtu: usize,
}

impl H265Packetizer {
    pub fn new() -> Self {
        Self { mtu: RTP_MTU }
    }

    /// 构建 FU header (RFC 7798)
    /// fu_indicator = (F=0 | Type=49 | LayerID | TID) from original NALU
    /// fu_header = S(1) | E(1) | FuType(6)
    fn build_fu(nalu: &[u8], offset: usize, chunk: &[u8], first: bool, last: bool) -> Vec<u8> {
        let nalu_type = h265_nalu_type(nalu);
        // FU indicator: preserve F(0), LayerID, TID from original, set Type=49
        let fu_indicator = (nalu[0] & 0x81) | ((49u8) << 1); // Type=49 in bits 1-6
        let mut payload = Vec::with_capacity(3 + chunk.len());
        payload.push(fu_indicator);
        // FU header
        let mut fu_header = nalu_type & 0x3F;
        if first {
            fu_header |= 0x80; // S bit
        }
        if last {
            fu_header |= 0x40; // E bit
        }
        payload.push(fu_header);
        payload.extend_from_slice(chunk);
        let _ = offset; // suppress unused warning
        payload
    }
}

impl Default for H265Packetizer {
    fn default() -> Self {
        Self::new()
    }
}

impl Packetizer for H265Packetizer {
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
            let nalu_size = nalu.len();
            let is_last_nalu = idx == nalus.len() - 1;

            if nalu_size <= max_payload {
                // Single NALU packet (includes 2-byte NALU header)
                packets.push(RtpPacketOut {
                    payload: Bytes::from(nalu.to_vec()),
                    marker: is_last_nalu,
                    timestamp,
                });
            } else {
                // FU 分片 (RFC 7798 Section 4.4)
                // FU indicator (1 byte) + FU header (1 byte) = 2 bytes overhead
                // But H265 NALU header is 2 bytes, and FU replaces it with 1+1=2 bytes
                // So the first chunk starts after byte 2 (skip original NALU header)
                let fu_overhead = 2;
                let max_fu_payload = max_payload - fu_overhead;
                let mut offset = 2; // 跳过 2-byte NALU header
                let mut first_fu = true;
                while offset < nalu.len() {
                    let end = (offset + max_fu_payload).min(nalu.len());
                    let chunk = &nalu[offset..end];
                    let is_last_fu = end == nalu.len();

                    let payload = Self::build_fu(nalu, offset, chunk, first_fu, is_last_fu);

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
        CodecType::H265
    }
}

// ============================================================================
// AV1 Packetizer (draft-ietf-payload-av1)
// ============================================================================

/// AV1 RTP payload header (draft-ietf-payload-av1):
/// ```text
///  0 1 2 3 4 5 6 7
/// +-+-+-+-+-+-+-+-+
/// |Z|Y|W|N|-|-|-|-|
/// +-+-+-+-+-+-+-+-+
/// ```
/// Z=1: first OBUs of a new coded video sequence
/// Y=1: last OBU of the last packet in the sequence
/// W (2 bits): does not contain start/end of a frame
/// N=1: this packet is not a complete frame
pub struct Av1Packetizer {
    mtu: usize,
}

impl Av1Packetizer {
    pub fn new() -> Self {
        Self { mtu: RTP_MTU }
    }

    /// 构建 AV1 aggregation header
    fn build_aggregation_header(is_first: bool, is_last: bool, is_keyframe: bool) -> u8 {
        let mut hdr = 0u8;
        if is_first && is_keyframe {
            hdr |= 0x80; // Z=1 (start of new coded video sequence)
        }
        if is_last {
            hdr |= 0x40; // Y=1 (last OBU)
        }
        // W bits: 00 = packet contains start+end of a frame
        // For single-packet frames, W=00 is correct
        // For multi-packet, first packet W=01 (start only), last W=10 (end only), middle W=11
        if !is_first && !is_last {
            hdr |= 0x20; // W=11 (continuation)
        } else if is_first && !is_last {
            hdr |= 0x00; // W=00 (start, more to come) — actually W=01 per spec
                         // Fix: W=01 means start of frame in this packet but not end
            hdr |= 0x20; // W=01 → bits 4-5 = 01
            hdr &= 0xDF; // clear bit 5, set bit 4
            hdr |= 0x10; // W=01
        }
        // Simplified: for single packet, W=00 (start+end)
        // The W field handling above is approximate; AV1 RTP is still a draft
        hdr
    }
}

impl Default for Av1Packetizer {
    fn default() -> Self {
        Self::new()
    }
}

impl Packetizer for Av1Packetizer {
    fn packetize(
        &mut self,
        frame_data: &[u8],
        timestamp: u32,
        keyframe: bool,
    ) -> Vec<RtpPacketOut> {
        let header_len = 1;
        let max_payload = self.mtu - header_len;

        if frame_data.len() <= max_payload {
            // 单包: Z=1 (if keyframe), Y=1, W=00
            let mut hdr = 0u8;
            if keyframe {
                hdr |= 0x80; // Z=1
            }
            hdr |= 0x40; // Y=1
                         // W=00: contains start and end of a frame
            let mut payload = Vec::with_capacity(header_len + frame_data.len());
            payload.push(hdr);
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

            let mut hdr = 0u8;
            if is_first && keyframe {
                hdr |= 0x80; // Z=1
            }
            if is_last {
                hdr |= 0x40; // Y=1
            }
            // W field:
            // 00 = start+end (single packet, handled above)
            // 01 = start only
            // 10 = end only
            // 11 = continuation (neither start nor end)
            if is_first && !is_last {
                hdr |= 0x10; // W=01
            } else if !is_first && is_last {
                hdr |= 0x20; // W=10
            } else if !is_first && !is_last {
                hdr |= 0x30; // W=11
            }
            // W=00 for single packet (is_first && is_last) — no bits set

            let mut payload = Vec::with_capacity(header_len + chunk.len());
            payload.push(hdr);
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
        CodecType::Av1
    }
}

// ============================================================================
// 工厂函数
// ============================================================================

/// 创建指定编解码器的 packetizer
pub fn create_packetizer(codec: CodecType) -> Box<dyn Packetizer> {
    match codec {
        CodecType::Vp8 => Box::new(Vp8Packetizer::new()),
        CodecType::Vp9 => Box::new(Vp9Packetizer::new()),
        CodecType::H264 => Box::new(H264Packetizer::new()),
        CodecType::H265 => Box::new(H265Packetizer::new()),
        CodecType::Av1 => Box::new(Av1Packetizer::new()),
        CodecType::Opus => Box::new(OpusPacketizer::new()),
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

    #[test]
    fn test_vp9_packetizer_single_packet() {
        let mut pkt = Vp9Packetizer::new();
        let data = vec![0u8; 100];
        let packets = pkt.packetize(&data, 9000, true);
        assert_eq!(packets.len(), 1);
        assert!(packets[0].marker);
        // I=1, B=1, E=1, V=0 (keyframe)
        let flags = packets[0].payload[0];
        assert_eq!(flags & 0x80, 0x80); // I=1
        assert_eq!(flags & 0x04, 0x04); // B=1
        assert_eq!(flags & 0x02, 0x02); // E=1
        assert_eq!(flags & 0x01, 0x00); // V=0 (keyframe)
    }

    #[test]
    fn test_vp9_packetizer_multi_packet() {
        let mut pkt = Vp9Packetizer {
            mtu: 100,
            picture_id: 0,
        };
        let data = vec![0u8; 500];
        let packets = pkt.packetize(&data, 9000, false);
        assert!(packets.len() > 1);
        // 第一包 B=1, P=1 (inter), V=1
        let flags0 = packets[0].payload[0];
        assert_eq!(flags0 & 0x04, 0x04); // B=1
        assert_eq!(flags0 & 0x40, 0x40); // P=1 (inter)
        assert_eq!(flags0 & 0x01, 0x01); // V=1 (non-keyframe)
                                         // 最后一包 E=1
        let flags_last = packets.last().unwrap().payload[0];
        assert_eq!(flags_last & 0x02, 0x02); // E=1
        assert!(packets.last().unwrap().marker);
    }

    #[test]
    fn test_vp9_packetizer_picture_id_increment() {
        let mut pkt = Vp9Packetizer::new();
        pkt.packetize(&[0u8; 10], 9000, true);
        pkt.packetize(&[0u8; 10], 9000, true);
        // Picture ID should increment (checked internally)
        assert!(pkt.picture_id >= 2);
    }

    #[test]
    fn test_h265_packetizer_single_nalu() {
        let mut pkt = H265Packetizer::new();
        // H265 NALU: 2-byte header + payload
        // Type=32 (VPS) → (32 << 1) = 0x40, F=0, LayerID=0, TID=1 → 0x40 0x01
        let data = [0, 0, 0, 1, 0x40, 0x01, 0xAA, 0xBB];
        let packets = pkt.packetize(&data, 9000, true);
        assert_eq!(packets.len(), 1);
        assert!(packets[0].marker);
    }

    #[test]
    fn test_h265_packetizer_fu_fragmentation() {
        let mut pkt = H265Packetizer { mtu: 100 };
        // 大 NALU: Type=19 (IDR) → (19 << 1) = 0x26, F=0, TID=1 → 0x26 0x01
        let mut data = vec![0, 0, 0, 1, 0x26, 0x01];
        data.extend(vec![0xAA; 300]);
        let packets = pkt.packetize(&data, 9000, true);
        assert!(packets.len() > 1);
        // FU indicator: Type=49 → (49 << 1) = 0x62, preserve F/LayerID/TID
        let fu_indicator = packets[0].payload[0];
        assert_eq!((fu_indicator >> 1) & 0x3F, 49); // Type=49 (FU)
                                                    // FU header: S=1, FuType=19
        let fu_header = packets[0].payload[1];
        assert_eq!(fu_header & 0x80, 0x80); // S=1
        assert_eq!(fu_header & 0x3F, 19); // FuType=19 (IDR)
                                          // 最后一包 E=1
        let fu_header_last = packets.last().unwrap().payload[1];
        assert_eq!(fu_header_last & 0x40, 0x40); // E=1
        assert!(packets.last().unwrap().marker);
    }

    #[test]
    fn test_av1_packetizer_single_packet() {
        let mut pkt = Av1Packetizer::new();
        let data = vec![0u8; 100];
        let packets = pkt.packetize(&data, 9000, true);
        assert_eq!(packets.len(), 1);
        assert!(packets[0].marker);
        // Z=1 (keyframe), Y=1, W=00
        let hdr = packets[0].payload[0];
        assert_eq!(hdr & 0x80, 0x80); // Z=1
        assert_eq!(hdr & 0x40, 0x40); // Y=1
    }

    #[test]
    fn test_av1_packetizer_multi_packet() {
        let mut pkt = Av1Packetizer { mtu: 100 };
        let data = vec![0u8; 500];
        let packets = pkt.packetize(&data, 9000, true);
        assert!(packets.len() > 1);
        // 第一包: Z=1 (keyframe), W=01 (start only)
        let hdr0 = packets[0].payload[0];
        assert_eq!(hdr0 & 0x80, 0x80); // Z=1
        assert_eq!(hdr0 & 0x30, 0x10); // W=01
                                       // 最后一包: Y=1, W=10 (end only)
        let hdr_last = packets.last().unwrap().payload[0];
        assert_eq!(hdr_last & 0x40, 0x40); // Y=1
        assert_eq!(hdr_last & 0x30, 0x20); // W=10
        assert!(packets.last().unwrap().marker);
    }

    #[test]
    fn test_create_packetizer_all_codecs() {
        let vp9 = create_packetizer(CodecType::Vp9);
        assert_eq!(vp9.codec_type(), CodecType::Vp9);

        let h265 = create_packetizer(CodecType::H265);
        assert_eq!(h265.codec_type(), CodecType::H265);

        let av1 = create_packetizer(CodecType::Av1);
        assert_eq!(av1.codec_type(), CodecType::Av1);
    }
}
