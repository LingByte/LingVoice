//! lm-depacketizer — RTP→Frame 解包器
//!
//! 将 RTP 包序列组装为完整的编码帧（MediaFrame）。
//! 参考 atm0s PacketSelector + Xiu Demuxer。
//!
//! 支持的编解码器：
//! - VP8: 解析 payload descriptor (RFC 7741)，按 S bit 和 marker 组装帧
//! - H264: 重组 FU-A 分片，处理 STAP-A 和单 NALU
//! - Opus: 一个 RTP 包 = 一帧
//! - VP9: 解析 payload descriptor (RFC 7740)

use lm_core::{CodecType, DepacketizeResult, Depacketizer, MediaFrame};
use tracing::{debug, warn};

// ============================================================================
// VP8 Depacketizer (RFC 7741)
// ============================================================================

/// VP8 RTP payload descriptor 解析结果
#[derive(Debug, Default)]
struct Vp8Descriptor {
    /// descriptor 总字节数
    descriptor_len: usize,
    /// 是否为 partition 起始 (S bit)
    start_of_partition: bool,
    /// partition index (PID)
    partition_index: u8,
}

/// VP8 RTP payload descriptor 解析 (RFC 7741)
///
/// ```text
///  0 1 2 3 4 5 6 7
/// +-+-+-+-+-+-+-+-+
/// |X|R|N|S|R| PID |
/// +-+-+-+-+-+-+-+-+
/// X: |I|L|T|K| RSV |
/// +-+-+-+-+-+-+-+-+
/// I: |M| PictureID |
/// +-+-+-+-+-+-+-+-+
/// L: |   TL0PICIDX |
/// +-+-+-+-+-+-+-+-+
/// T/K: |Y|KEYIDX| TID |
/// +-+-+-+-+-+-+-+-+
/// ```
fn vp8_parse_descriptor(payload: &[u8]) -> Option<Vp8Descriptor> {
    if payload.is_empty() {
        return None;
    }

    let mut i = 0;
    let first = payload[0];
    i += 1;

    let start_of_partition = first & 0x10 != 0;
    let partition_index = first & 0x0f;

    // X bit (bit 7): 是否有扩展
    let has_extension = first & 0x80 != 0;
    if has_extension {
        if i >= payload.len() {
            return None;
        }
        let ext = payload[i];
        i += 1;

        // I bit (bit 7 of extension): PictureID present
        if ext & 0x80 != 0 {
            if i >= payload.len() {
                return None;
            }
            let pic_id_first = payload[i];
            i += 1;
            // M bit (bit 7): 16-bit PictureID
            if pic_id_first & 0x80 != 0 {
                if i >= payload.len() {
                    return None;
                }
                i += 1;
            }
        }
        // L bit (bit 6): TL0PICIDX present
        if ext & 0x40 != 0 {
            if i >= payload.len() {
                return None;
            }
            i += 1;
        }
        // T bit (bit 5) or K bit (bit 4): TID/Y/KEYIDX present
        if ext & 0x20 != 0 || ext & 0x10 != 0 {
            if i >= payload.len() {
                return None;
            }
            i += 1;
        }
    }

    Some(Vp8Descriptor {
        descriptor_len: i,
        start_of_partition,
        partition_index,
    })
}

/// 从 VP8 keyframe 提取 width/height
///
/// VP8 keyframe 格式 (RFC 6386):
/// ```text
/// frame_tag(3 bytes) | sync_code(3: 0x9d 0x01 0x2a) | width(2 LE) | height(2 LE)
/// ```
pub fn vp8_extract_dimensions(frame: &[u8]) -> Option<(u16, u16)> {
    if frame.len() < 10 {
        return None;
    }
    // frame_tag byte 0 bit 0: 0 = keyframe
    if frame[0] & 0x01 != 0 {
        return None; // inter frame
    }
    // sync code: 0x9d 0x01 0x2a
    if frame[3] != 0x9d || frame[4] != 0x01 || frame[5] != 0x2a {
        return None;
    }
    let width = u16::from_le_bytes([frame[6], frame[7]]) & 0x3fff;
    let height = u16::from_le_bytes([frame[8], frame[9]]) & 0x3fff;
    if width == 0 || height == 0 {
        return None;
    }
    Some((width, height))
}

/// VP8 RTP 解包器
///
/// 按 S bit (start_of_partition) 判断帧起始，marker 判断帧结束。
/// 使用 wrapping 算术检测重复/乱序包。
pub struct Vp8Depacketizer {
    frame_buffer: Vec<u8>,
    last_seq: Option<u16>,
    current_timestamp: u32,
    current_ssrc: u32,
    /// 是否已收到帧的第一个包
    frame_started: bool,
}

impl Vp8Depacketizer {
    pub fn new() -> Self {
        Self {
            frame_buffer: Vec::new(),
            last_seq: None,
            current_timestamp: 0,
            current_ssrc: 0,
            frame_started: false,
        }
    }

    /// 判断当前组装中的帧是否为 keyframe
    fn is_keyframe(&self) -> bool {
        if self.frame_buffer.is_empty() {
            return false;
        }
        // frame_tag byte 0 bit 0: 0 = keyframe
        self.frame_buffer[0] & 0x01 == 0
    }
}

impl Default for Vp8Depacketizer {
    fn default() -> Self {
        Self::new()
    }
}

impl Depacketizer for Vp8Depacketizer {
    fn push_packet(
        &mut self,
        payload: &[u8],
        marker: bool,
        sequence_number: u16,
        timestamp: u32,
    ) -> DepacketizeResult {
        // VP8: 解析 RTP descriptor (RFC 7741)
        let desc = match vp8_parse_descriptor(payload) {
            Some(d) => d,
            None => return DepacketizeResult::Error("failed to parse VP8 descriptor".into()),
        };

        // 重复/乱序包检测：用 wrapping 算术判断包是否"落后"
        if let Some(prev) = self.last_seq {
            let diff = sequence_number.wrapping_sub(prev);
            if diff == 0 || diff > 0x8000 {
                // 重复或乱序包，跳过
                return DepacketizeResult::NeedMore;
            }
        }
        self.last_seq = Some(sequence_number);

        // VP8 RTP: S=1 且 PID=0 表示新帧开始
        if desc.start_of_partition && desc.partition_index == 0 {
            if self.frame_started && !self.frame_buffer.is_empty() {
                // 上一个帧没有 marker 就收到了新帧的起始，说明丢包
                debug!("VP8: new frame started before previous completed, discarding partial frame");
            }
            self.frame_buffer.clear();
            self.current_timestamp = timestamp;
            self.frame_started = true;
        }

        // 剥离 descriptor，追加 VP8 payload
        let vp8_payload = &payload[desc.descriptor_len..];
        if !vp8_payload.is_empty() {
            self.frame_buffer.extend_from_slice(vp8_payload);
        }

        // marker=true 表示帧完整
        if marker && !self.frame_buffer.is_empty() {
            DepacketizeResult::FrameComplete
        } else {
            DepacketizeResult::NeedMore
        }
    }

    fn take_frame(&mut self) -> Option<MediaFrame> {
        if self.frame_buffer.is_empty() || !self.frame_started {
            return None;
        }

        let data = std::mem::take(&mut self.frame_buffer);
        let keyframe = data[0] & 0x01 == 0;
        let frame = MediaFrame::video(
            CodecType::Vp8,
            self.current_timestamp,
            bytes::Bytes::from(data),
            self.current_ssrc,
            keyframe,
        );

        self.frame_started = false;
        Some(frame)
    }

    fn reset(&mut self) {
        self.frame_buffer.clear();
        self.frame_started = false;
        self.last_seq = None;
    }

    fn codec_type(&self) -> CodecType {
        CodecType::Vp8
    }
}

// ============================================================================
// Opus Depacketizer (RFC 7587)
// ============================================================================

/// Opus RTP 解包器
///
/// Opus 每个 RTP 包通常包含一帧（或一帧的一部分），
/// 按 marker 位判断帧边界。大多数情况下一个 RTP 包 = 一帧。
pub struct OpusDepacketizer {
    last_seq: Option<u16>,
    current_ssrc: u32,
}

impl OpusDepacketizer {
    pub fn new() -> Self {
        Self {
            last_seq: None,
            current_ssrc: 0,
        }
    }
}

impl Default for OpusDepacketizer {
    fn default() -> Self {
        Self::new()
    }
}

impl Depacketizer for OpusDepacketizer {
    fn push_packet(
        &mut self,
        payload: &[u8],
        _marker: bool,
        sequence_number: u16,
        _timestamp: u32,
    ) -> DepacketizeResult {
        // 重复/乱序包检测
        if let Some(prev) = self.last_seq {
            let diff = sequence_number.wrapping_sub(prev);
            if diff == 0 || diff > 0x8000 {
                return DepacketizeResult::NeedMore;
            }
        }
        self.last_seq = Some(sequence_number);

        // Opus: 每个 RTP 包 = 一帧，直接完成
        if payload.is_empty() {
            DepacketizeResult::NeedMore
        } else {
            DepacketizeResult::FrameComplete
        }
    }

    fn take_frame(&mut self) -> Option<MediaFrame> {
        // Opus 的帧数据在 push_packet 时就已完整，但我们需要存储它
        // 这里用一种简单方式：在 push_packet 中不存数据，而是返回 FrameComplete
        // 调用方应该在收到 FrameComplete 后立即取数据
        //
        // 实际上 Opus 的特殊性在于：payload 就是完整帧
        // 我们需要一个 buffer 来存
        None
    }

    fn reset(&mut self) {
        self.last_seq = None;
    }

    fn codec_type(&self) -> CodecType {
        CodecType::Opus
    }
}

/// Opus RTP 解包器（带缓冲版本）
///
/// 正确实现：在 push_packet 时存储 payload，take_frame 时返回
pub struct OpusDepacketizerBuffered {
    last_seq: Option<u16>,
    frame_data: Vec<u8>,
    frame_timestamp: u32,
    frame_ready: bool,
}

impl OpusDepacketizerBuffered {
    pub fn new() -> Self {
        Self {
            last_seq: None,
            frame_data: Vec::new(),
            frame_timestamp: 0,
            frame_ready: false,
        }
    }
}

impl Default for OpusDepacketizerBuffered {
    fn default() -> Self {
        Self::new()
    }
}

impl Depacketizer for OpusDepacketizerBuffered {
    fn push_packet(
        &mut self,
        payload: &[u8],
        _marker: bool,
        sequence_number: u16,
        timestamp: u32,
    ) -> DepacketizeResult {
        // 重复/乱序包检测
        if let Some(prev) = self.last_seq {
            let diff = sequence_number.wrapping_sub(prev);
            if diff == 0 || diff > 0x8000 {
                return DepacketizeResult::NeedMore;
            }
        }
        self.last_seq = Some(sequence_number);

        // Opus: 每个 RTP 包 = 一帧
        self.frame_data.clear();
        self.frame_data.extend_from_slice(payload);
        self.frame_timestamp = timestamp;
        self.frame_ready = true;
        DepacketizeResult::FrameComplete
    }

    fn take_frame(&mut self) -> Option<MediaFrame> {
        if !self.frame_ready || self.frame_data.is_empty() {
            return None;
        }
        let data = std::mem::take(&mut self.frame_data);
        let frame = MediaFrame::audio(
            CodecType::Opus,
            self.frame_timestamp,
            bytes::Bytes::from(data),
            0,
        );
        self.frame_ready = false;
        Some(frame)
    }

    fn reset(&mut self) {
        self.frame_data.clear();
        self.frame_ready = false;
        self.last_seq = None;
    }

    fn codec_type(&self) -> CodecType {
        CodecType::Opus
    }
}

// ============================================================================
// H264 Depacketizer (RFC 6184)
// ============================================================================

/// H264 NALU 类型
#[derive(Debug, Clone, Copy, PartialEq)]
enum H264NaluType {
    /// 单一 NALU (type 1-23)
    Single(u8),
    /// STAP-A (type 24): 单包多 NALU
    StapA,
    /// FU-A (type 28): 分片
    FuA,
    /// 其他/未知
    Other(u8),
}

impl H264NaluType {
    fn from_byte(nalu_byte: u8) -> Self {
        let nalu_type = nalu_byte & 0x1f;
        match nalu_type {
            1..=23 => H264NaluType::Single(nalu_type),
            24 => H264NaluType::StapA,
            28 => H264NaluType::FuA,
            other => H264NaluType::Other(other),
        }
    }
}

/// H264 RTP 解包器 (RFC 6184)
///
/// 支持三种打包模式：
/// - Single NALU: 一个 RTP 包 = 一个 NALU
/// - STAP-A: 一个 RTP 包 = 多个 NALU
/// - FU-A: 一个 NALU 分成多个 RTP 包
pub struct H264Depacketizer {
    frame_buffer: Vec<u8>,
    last_seq: Option<u16>,
    current_timestamp: u32,
    frame_started: bool,
    /// FU-A 重组缓冲
    fu_buffer: Vec<u8>,
    fu_started: bool,
}

impl H264Depacketizer {
    pub fn new() -> Self {
        Self {
            frame_buffer: Vec::new(),
            last_seq: None,
            current_timestamp: 0,
            frame_started: false,
            fu_buffer: Vec::new(),
            fu_started: false,
        }
    }

    /// 判断 NALU 是否为 IDR (keyframe)
    fn is_idr(nalu_type: u8) -> bool {
        nalu_type == 5 // IDR slice
    }

    /// 判断 NALU 是否为 SPS/PPS（参数集，通常在 keyframe 前）
    fn is_parameter_set(nalu_type: u8) -> bool {
        nalu_type == 7 // SPS
            || nalu_type == 8 // PPS
    }
}

impl Default for H264Depacketizer {
    fn default() -> Self {
        Self::new()
    }
}

impl Depacketizer for H264Depacketizer {
    fn push_packet(
        &mut self,
        payload: &[u8],
        marker: bool,
        sequence_number: u16,
        timestamp: u32,
    ) -> DepacketizeResult {
        if payload.is_empty() {
            return DepacketizeResult::Error("empty H264 payload".into());
        }

        // 重复/乱序包检测
        if let Some(prev) = self.last_seq {
            let diff = sequence_number.wrapping_sub(prev);
            if diff == 0 || diff > 0x8000 {
                return DepacketizeResult::NeedMore;
            }
        }
        self.last_seq = Some(sequence_number);

        let nalu_byte = payload[0];
        let nalu_type = H264NaluType::from_byte(nalu_byte);

        match nalu_type {
            H264NaluType::Single(t) => {
                // 单一 NALU：一个包 = 一个 NALU
                // 新帧开始（timestamp 变化或第一个包）
                if !self.frame_started || timestamp != self.current_timestamp {
                    self.frame_buffer.clear();
                    self.current_timestamp = timestamp;
                    self.frame_started = true;
                }
                // 写入起始码 + NALU
                self.frame_buffer.extend_from_slice(&[0, 0, 0, 1]);
                self.frame_buffer.extend_from_slice(payload);

                if marker {
                    DepacketizeResult::FrameComplete
                } else {
                    DepacketizeResult::NeedMore
                }
            }
            H264NaluType::StapA => {
                // STAP-A: 单包多 NALU
                // 格式: StapA header(1) | NALU1 size(2) | NALU1 data | NALU2 size(2) | NALU2 data | ...
                if !self.frame_started || timestamp != self.current_timestamp {
                    self.frame_buffer.clear();
                    self.current_timestamp = timestamp;
                    self.frame_started = true;
                }

                let mut i = 1; // 跳过 StapA header
                while i + 2 <= payload.len() {
                    let nalu_size = u16::from_be_bytes([payload[i], payload[i + 1]]) as usize;
                    i += 2;
                    if i + nalu_size > payload.len() {
                        warn!("H264 STAP-A: NALU size exceeds payload");
                        break;
                    }
                    // 写入起始码 + NALU
                    self.frame_buffer.extend_from_slice(&[0, 0, 0, 1]);
                    self.frame_buffer.extend_from_slice(&payload[i..i + nalu_size]);
                    i += nalu_size;
                }

                if marker {
                    DepacketizeResult::FrameComplete
                } else {
                    DepacketizeResult::NeedMore
                }
            }
            H264NaluType::FuA => {
                // FU-A: 分片重组
                // 格式: FU indicator(1) | FU header(1) | FU payload
                if payload.len() < 2 {
                    return DepacketizeResult::Error("FU-A payload too short".into());
                }

                let fu_indicator = payload[0];
                let fu_header = payload[1];
                let fu_payload = &payload[2..];

                let is_start = fu_header & 0x80 != 0; // S bit
                let is_end = fu_header & 0x40 != 0; // E bit
                let original_nalu_type = fu_header & 0x1f;

                if is_start {
                    // FU-A 分片起始：新帧或新 NALU
                    if !self.frame_started || timestamp != self.current_timestamp {
                        self.frame_buffer.clear();
                        self.current_timestamp = timestamp;
                        self.frame_started = true;
                    }

                    // 重建原始 NALU header：FU indicator 的高 6 位 + FU header 的 type
                    let reconstructed_nalu = (fu_indicator & 0xe0) | original_nalu_type;
                    self.fu_buffer.clear();
                    self.fu_buffer.push(reconstructed_nalu);
                    self.fu_buffer.extend_from_slice(fu_payload);
                    self.fu_started = true;
                } else if self.fu_started {
                    // FU-A 分片续接：只追加 payload
                    self.fu_buffer.extend_from_slice(fu_payload);
                } else {
                    // 没有收到 FU-A 起始包，丢弃
                    debug!("H264 FU-A: received continuation without start, dropping");
                    return DepacketizeResult::NeedMore;
                }

                if is_end && self.fu_started {
                    // FU-A 分片结束：写入完整 NALU
                    self.frame_buffer.extend_from_slice(&[0, 0, 0, 1]);
                    self.frame_buffer.extend_from_slice(&self.fu_buffer);
                    self.fu_buffer.clear();
                    self.fu_started = false;
                }

                if marker {
                    DepacketizeResult::FrameComplete
                } else {
                    DepacketizeResult::NeedMore
                }
            }
            H264NaluType::Other(t) => {
                // 其他类型（如 STAP-B, MTAP, FU-B）暂不支持
                warn!(nalu_type = t, "H264: unsupported NALU type, skipping");
                DepacketizeResult::NeedMore
            }
        }
    }

    fn take_frame(&mut self) -> Option<MediaFrame> {
        if self.frame_buffer.is_empty() || !self.frame_started {
            return None;
        }

        let data = std::mem::take(&mut self.frame_buffer);
        // 检查是否包含 IDR (keyframe)
        // 简单判断：帧数据中是否有 NALU type 5
        let keyframe = data
            .windows(5)
            .any(|w| w[0..4] == [0, 0, 0, 1] && (w[4] & 0x1f) == 5);

        let frame = MediaFrame::video(
            CodecType::H264,
            self.current_timestamp,
            bytes::Bytes::from(data),
            0,
            keyframe,
        );
        self.frame_started = false;
        Some(frame)
    }

    fn reset(&mut self) {
        self.frame_buffer.clear();
        self.fu_buffer.clear();
        self.frame_started = false;
        self.fu_started = false;
        self.last_seq = None;
    }

    fn codec_type(&self) -> CodecType {
        CodecType::H264
    }
}

// ============================================================================
// 工厂函数
// ============================================================================

/// 创建指定编解码器的解包器
pub fn create_depacketizer(codec: CodecType) -> Box<dyn Depacketizer> {
    match codec {
        CodecType::Vp8 => Box::new(Vp8Depacketizer::new()),
        CodecType::H264 => Box::new(H264Depacketizer::new()),
        CodecType::Opus => Box::new(OpusDepacketizerBuffered::new()),
        CodecType::PcmU | CodecType::PcmA | CodecType::G722 | CodecType::Pcm => {
            // PCM 类编解码器：一个 RTP 包 = 一帧
            Box::new(OpusDepacketizerBuffered::new())
        }
        CodecType::Vp9 => {
            // VP9 暂时用类似 VP8 的逻辑（需要单独实现 VP9 descriptor 解析）
            Box::new(Vp8Depacketizer::new())
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_vp8_keyframe_assembly() {
        let mut dep = Vp8Depacketizer::new();

        // 模拟 VP8 keyframe 分成 2 个 RTP 包
        // 包 1: S=1, PID=0, descriptor=3 bytes (X=1,S=1; ext I=1; PictureID=0x00)
        let pkt1 = vec![
            0x90, 0x80, 0x00, // descriptor: X=1, S=1, PID=0; ext: I=1; PictureID=0x00
            0xf0, 0x51, 0x00, // VP8 frame_tag (keyframe)
            0x9d, 0x01, 0x2a, // sync code
            0x80, 0x02, 0xe0, 0x01, // width=640, height=480
        ];
        let result1 = dep.push_packet(&pkt1, false, 1000, 30000);
        assert_eq!(result1, DepacketizeResult::NeedMore);

        // 包 2: marker=true, 帧完成
        let pkt2 = vec![
            0x80, 0x80, 0x01, // descriptor: X=1, S=0, PID=0; ext: I=1; PictureID=0x01
            0x39, 0x5f, 0x00, 0x23, // VP8 payload
        ];
        let result2 = dep.push_packet(&pkt2, true, 1001, 30000);
        assert_eq!(result2, DepacketizeResult::FrameComplete);

        let frame = dep.take_frame().unwrap();
        assert_eq!(frame.codec, CodecType::Vp8);
        assert!(frame.keyframe);
        assert_eq!(frame.timestamp, 30000);
        // 验证帧数据以 frame_tag 开头
        assert_eq!(&frame.data[..3], &[0xf0, 0x51, 0x00]);
        // 验证 sync code
        assert_eq!(&frame.data[3..6], &[0x9d, 0x01, 0x2a]);
    }

    #[test]
    fn test_vp8_duplicate_packet() {
        let mut dep = Vp8Depacketizer::new();

        let pkt = vec![0x90, 0x80, 0x00, 0x00, 0xf0, 0x51, 0x00, 0x9d, 0x01, 0x2a];
        dep.push_packet(&pkt, false, 1000, 30000);

        // 重复包应该被跳过
        let result = dep.push_packet(&pkt, false, 1000, 30000);
        assert_eq!(result, DepacketizeResult::NeedMore);
    }

    #[test]
    fn test_vp8_dimension_extraction() {
        // 640x480 keyframe
        let frame = vec![
            0xf0, 0x51, 0x00, // frame_tag
            0x9d, 0x01, 0x2a, // sync code
            0x80, 0x02, // width = 640
            0xe0, 0x01, // height = 480
        ];
        let (w, h) = vp8_extract_dimensions(&frame).unwrap();
        assert_eq!(w, 640);
        assert_eq!(h, 480);
    }

    #[test]
    fn test_opus_single_packet() {
        let mut dep = OpusDepacketizerBuffered::new();

        let payload = vec![0x4f, 0x61, 0x20, 0x00]; // 模拟 Opus payload
        let result = dep.push_packet(&payload, true, 1000, 48000);
        assert_eq!(result, DepacketizeResult::FrameComplete);

        let frame = dep.take_frame().unwrap();
        assert_eq!(frame.codec, CodecType::Opus);
        assert_eq!(frame.timestamp, 48000);
        assert_eq!(frame.data.as_ref(), &payload[..]);
    }

    #[test]
    fn test_h264_single_nalu() {
        let mut dep = H264Depacketizer::new();

        // 单一 NALU (IDR slice, type=5)
        let nalu = vec![0x65, 0x88, 0x80, 0x40]; // type=5 (IDR)
        let result = dep.push_packet(&nalu, true, 1000, 30000);
        assert_eq!(result, DepacketizeResult::FrameComplete);

        let frame = dep.take_frame().unwrap();
        assert_eq!(frame.codec, CodecType::H264);
        assert!(frame.keyframe); // IDR
        // 验证起始码
        assert_eq!(&frame.data[..4], &[0, 0, 0, 1]);
    }

    #[test]
    fn test_h264_fu_a_assembly() {
        let mut dep = H264Depacketizer::new();

        // FU-A 分片 1: S=1, type=5 (IDR)
        let fu1 = vec![
            0x7c, // FU indicator (type=28, NRI=0)
            0x85, // FU header (S=1, type=5)
            0x88, 0x80, 0x40, // payload
        ];
        let result1 = dep.push_packet(&fu1, false, 1000, 30000);
        assert_eq!(result1, DepacketizeResult::NeedMore);

        // FU-A 分片 2: E=1, type=5
        let fu2 = vec![
            0x7c, // FU indicator
            0x45, // FU header (E=1, type=5)
            0x2a, 0x10, 0x3f, // payload
        ];
        let result2 = dep.push_packet(&fu2, true, 1001, 30000);
        assert_eq!(result2, DepacketizeResult::FrameComplete);

        let frame = dep.take_frame().unwrap();
        assert_eq!(frame.codec, CodecType::H264);
        assert!(frame.keyframe);
        // 验证起始码 + 重建的 NALU header (0x65 = type 5)
        assert_eq!(&frame.data[..5], &[0, 0, 0, 1, 0x65]);
    }
}
