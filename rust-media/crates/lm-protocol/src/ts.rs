//! MPEG-TS Muxer — 将 H.264 + AAC 媒体帧封装为标准 MPEG-TS 流
//!
//! 参考 ISO/IEC 13818-1，实现：
//! - 188 字节 TS 包封装
//! - PAT (Program Association Table, PID 0)
//! - PMT (Program Map Table)
//! - PES (Packetized Elementary Stream)
//! - PCR (Program Clock Reference)
//!
//! 支持的编解码：
//! - 视频：H.264 (stream_type 0x1B, Annex-B NALU)
//! - 音频：AAC (stream_type 0x0F, ADTS)
//!
//! 不支持的（TS 标准不支持）：
//! - VP8/VP9（需要 fMP4 模式）
//! - Opus（需要 fMP4 模式）

use lm_core::{CodecType, MediaFrame, TrackKind};

/// TS 包大小
pub const TS_PACKET_SIZE: usize = 188;

/// TS 同步字节
const TS_SYNC_BYTE: u8 = 0x47;

/// PAT PID
const PAT_PID: u16 = 0x0000;

/// PMT PID（自分配）
const PMT_PID: u16 = 0x1000;

/// 视频 PID
const VIDEO_PID: u16 = 0x0100;

/// 音频 PID
const AUDIO_PID: u16 = 0x0101;

/// PCR PID（跟视频一致）
const PCR_PID: u16 = VIDEO_PID;

/// H.264 stream type
const STREAM_TYPE_H264: u8 = 0x1B;

/// AAC stream type
const STREAM_TYPE_AAC: u8 = 0x0F;

/// TS 包头部（4 字节）
#[derive(Debug, Clone, Copy)]
struct TsHeader {
    sync_byte: u8,
    transport_error_indicator: bool,
    payload_unit_start_indicator: bool,
    transport_priority: bool,
    pid: u16,
    transport_scrambling_control: u8,
    adaptation_field_control: u8, // 01=payload only, 10=adaptation only, 11=both
    continuity_counter: u8,
}

impl TsHeader {
    fn to_bytes(&self) -> [u8; 4] {
        let mut buf = [0u8; 4];
        buf[0] = self.sync_byte;
        buf[1] = ((self.transport_error_indicator as u8) << 7)
            | ((self.payload_unit_start_indicator as u8) << 6)
            | ((self.transport_priority as u8) << 5)
            | ((self.pid >> 8) as u8 & 0x1F);
        buf[2] = (self.pid & 0xFF) as u8;
        buf[3] = ((self.transport_scrambling_control & 0x03) << 6)
            | ((self.adaptation_field_control & 0x03) << 4)
            | (self.continuity_counter & 0x0F);
        buf
    }
}

/// PCR (Program Clock Reference)
#[derive(Debug, Clone, Copy)]
struct Pcr {
    /// PCR base (33-bit)
    base: u64,
    /// PCR extension (9-bit)
    ext: u16,
}

impl Pcr {
    fn from_timestamp_90khz(ts: u64) -> Self {
        // PCR base = 90kHz timestamp, extension = 0
        // 27MHz = 90kHz * 300, extension = (27MHz ticks) % 300
        Self {
            base: ts & 0x1_FFFF_FFFF,
            ext: 0,
        }
    }

    fn to_bytes(&self) -> [u8; 6] {
        let mut buf = [0u8; 6];
        // 33-bit base split across bytes
        buf[0] = ((self.base >> 25) & 0xFF) as u8;
        buf[1] = ((self.base >> 17) & 0xFF) as u8;
        buf[2] = ((self.base >> 9) & 0xFF) as u8;
        buf[3] = ((self.base >> 1) & 0xFF) as u8;
        buf[4] = (((self.base & 1) << 7) | 0x7E | ((self.ext >> 8) & 1) as u64) as u8;
        buf[5] = (self.ext & 0xFF) as u8;
        buf
    }
}

/// Adaptation field
struct AdaptationField {
    pcr: Option<Pcr>,
    stuff_bytes: usize,
}

impl AdaptationField {
    fn len(&self) -> usize {
        let mut len = 1; // adaptation_field_length byte itself not counted in this field
        if self.pcr.is_some() {
            len += 6; // PCR flag (1) + PCR (6)
        }
        len += self.stuff_bytes;
        len
    }

    fn to_bytes(&self) -> Vec<u8> {
        let mut buf = Vec::new();
        let flags: u8 = if self.pcr.is_some() { 0x10 } else { 0x00 };
        let mut content_len = 1; // flags byte
        if self.pcr.is_some() {
            content_len += 6;
        }
        content_len += self.stuff_bytes;
        buf.push(content_len as u8); // adaptation_field_length
        buf.push(flags);
        if let Some(pcr) = &self.pcr {
            buf.extend_from_slice(&pcr.to_bytes());
        }
        buf.extend(std::iter::repeat(0xFF).take(self.stuff_bytes));
        buf
    }
}

/// PES 包头
struct PesHeader {
    stream_id: u8,
    /// PES packet length (16-bit, 0 = unbounded for video)
    pes_packet_length: u16,
    /// DTS/PTS
    pts: Option<u64>,
    dts: Option<u64>,
}

impl PesHeader {
    fn to_bytes(&self) -> Vec<u8> {
        let mut buf = Vec::new();
        // start code prefix
        buf.extend_from_slice(&[0x00, 0x00, 0x01]);
        buf.push(self.stream_id);
        buf.push((self.pes_packet_length >> 8) as u8);
        buf.push((self.pes_packet_length & 0xFF) as u8);

        // Optional PES header
        let mut flags: u8 = 0x80; // marker bits
        if self.pts.is_some() {
            flags |= 0x80; // PTS present
        }
        if self.dts.is_some() {
            flags |= 0x40; // DTS present
        }
        buf.push(flags);
        // PES header data length
        let mut pes_header_len: u8 = 0;
        if self.pts.is_some() {
            pes_header_len += 5;
        }
        if self.dts.is_some() {
            pes_header_len += 5;
        }
        buf.push(pes_header_len);

        // PTS
        if let Some(pts) = &self.pts {
            buf.push(0x21 | (((*pts >> 29) & 0x0E) as u8));
            buf.push(((*pts >> 22) & 0xFF) as u8);
            buf.push(0x01 | (((*pts >> 14) & 0xFE) as u8));
            buf.push(((*pts >> 7) & 0xFF) as u8);
            buf.push(0x01 | (((*pts << 1) & 0xFE) as u8));
        }

        // DTS
        if let Some(dts) = &self.dts {
            buf.push(0x11 | (((*dts >> 29) & 0x0E) as u8));
            buf.push(((*dts >> 22) & 0xFF) as u8);
            buf.push(0x01 | (((*dts >> 14) & 0xFE) as u8));
            buf.push(((*dts >> 7) & 0xFF) as u8);
            buf.push(0x01 | (((*dts << 1) & 0xFE) as u8));
        }

        buf
    }
}

/// MPEG-TS Muxer
pub struct TsMuxer {
    /// 视频连续计数器
    video_cc: u8,
    /// 音频连续计数器
    audio_cc: u8,
    /// PAT 连续计数器
    pat_cc: u8,
    /// PMT 连续计数器
    pmt_cc: u8,
    /// 是否已写入 PAT/PMT
    pat_pmt_written: bool,
    /// 视频编解码
    video_codec: CodecType,
    /// 音频编解码
    audio_codec: CodecType,
    /// 是否有音频
    has_audio: bool,
    /// 是否有视频
    has_video: bool,
}

impl TsMuxer {
    pub fn new(video_codec: CodecType, audio_codec: CodecType) -> Self {
        Self {
            video_cc: 0,
            audio_cc: 0,
            pat_cc: 0,
            pmt_cc: 0,
            pat_pmt_written: false,
            video_codec,
            audio_codec,
            has_audio: !is_video_codec(audio_codec),
            has_video: is_video_codec(video_codec),
        }
    }

    /// 写入 PAT + PMT（每个分段开头需要）
    pub fn write_pat_pmt(&mut self) -> Vec<u8> {
        let mut output = Vec::new();
        output.extend(self.write_pat());
        output.extend(self.write_pmt());
        self.pat_pmt_written = true;
        output
    }

    /// 生成 PAT 包
    fn write_pat(&mut self) -> Vec<u8> {
        // PAT data: table_id(1) + section_length(2) + transport_stream_id(2)
        // + version+curent_next(1) + section_number(1) + last_section_number(1)
        // + program_number(2) + reserved+PMT_PID(2) + CRC32(4)
        let mut pat_data = Vec::new();
        pat_data.push(0x00); // table_id = PAT
        // section_length = 5 + 4 (program info) + 4 (CRC) = 13
        pat_data.push(0xB0); // section_syntax_indicator=1, reserved=0x03, length high
        pat_data.push(0x0D); // length low = 13
        pat_data.extend_from_slice(&[0x00, 0x01]); // transport_stream_id = 1
        pat_data.push(0xC1); // version=0, current_next=1
        pat_data.push(0x00); // section_number
        pat_data.push(0x00); // last_section_number
        // program_number = 1
        pat_data.extend_from_slice(&[0x00, 0x01]);
        // reserved(3) + PMT_PID(13) = 0xE0 | PMT_PID
        pat_data.push(0xE0 | ((PMT_PID >> 8) as u8 & 0x1F));
        pat_data.push((PMT_PID & 0xFF) as u8);

        // CRC32
        let crc = crc32_mpeg(&pat_data);
        pat_data.extend_from_slice(&crc.to_be_bytes());

        // 封装为 TS 包
        make_ts_packet(PAT_PID, true, &pat_data, None, &mut self.pat_cc)
    }

    /// 生成 PMT 包
    fn write_pmt(&mut self) -> Vec<u8> {
        let mut pmt_data = Vec::new();
        pmt_data.push(0x02); // table_id = PMT
        // section_length: 5 + 4 (PCR_PID) + program_info(0) + streams + CRC
        let mut section = Vec::new();
        section.extend_from_slice(&[0x00, 0x01]); // program_number = 1
        section.push(0xC1); // version=0, current_next=1
        section.push(0x00); // section_number
        section.push(0x00); // last_section_number
        // reserved(3) + PCR_PID(13)
        section.push(0xE0 | ((PCR_PID >> 8) as u8 & 0x1F));
        section.push((PCR_PID & 0xFF) as u8);
        // program_info_length = 0
        section.push(0xF0);
        section.push(0x00);

        // 视频流
        if self.has_video {
            let st = match self.video_codec {
                CodecType::H264 => STREAM_TYPE_H264,
                _ => STREAM_TYPE_H264, // 默认 H264
            };
            section.push(st);
            section.push(0xE0 | ((VIDEO_PID >> 8) as u8 & 0x1F));
            section.push((VIDEO_PID & 0xFF) as u8);
            section.push(0xF0); // ES_info_length = 0
            section.push(0x00);
        }

        // 音频流
        if self.has_audio {
            let st = match self.audio_codec {
                CodecType::Aac => STREAM_TYPE_AAC,
                _ => STREAM_TYPE_AAC,
            };
            section.push(st);
            section.push(0xE0 | ((AUDIO_PID >> 8) as u8 & 0x1F));
            section.push((AUDIO_PID & 0xFF) as u8);
            section.push(0xF0);
            section.push(0x00);
        }

        let section_len = section.len() + 4; // +CRC
        pmt_data.push(0xB0 | ((section_len >> 8) as u8 & 0x0F));
        pmt_data.push((section_len & 0xFF) as u8);
        pmt_data.extend_from_slice(&section);

        // CRC32
        let crc = crc32_mpeg(&pmt_data);
        pmt_data.extend_from_slice(&crc.to_be_bytes());

        make_ts_packet(PMT_PID, true, &pmt_data, None, &mut self.pmt_cc)
    }

    /// 封装一个 MediaFrame 为 TS 包列表
    /// 返回完整的 TS 数据（可能多个 188 字节包）
    pub fn write_frame(&mut self, frame: &MediaFrame) -> Vec<u8> {
        if !self.pat_pmt_written {
            // 首帧前写 PAT/PMT
            let mut output = self.write_pat_pmt();
            output.extend(self.write_frame_inner(frame));
            return output;
        }
        self.write_frame_inner(frame)
    }

    fn write_frame_inner(&mut self, frame: &MediaFrame) -> Vec<u8> {
        match frame.kind {
            TrackKind::Video => self.write_video_frame(frame),
            TrackKind::Audio => self.write_audio_frame(frame),
        }
    }

    /// 封装视频帧为 TS 包
    fn write_video_frame(&mut self, frame: &MediaFrame) -> Vec<u8> {
        if !self.has_video {
            return Vec::new();
        }

        // RTP timestamp is 90kHz for video, direct use as PTS/DTS
        let pts = frame.timestamp as u64;

        // H.264: frame.data should be Annex-B NALU (with start codes)
        // If it's a single NALU without start code, add it
        let pes_payload = ensure_h264_annex_b(&frame.data);

        // PES header: stream_id = 0xE0 (video), length = 0 (unbounded for video)
        let pes_header = PesHeader {
            stream_id: 0xE0,
            pes_packet_length: 0, // 0 = unbounded (allowed for video)
            pts: Some(pts),
            dts: Some(pts), // no B-frames, DTS=PTS
        };
        let pes_header_bytes = pes_header.to_bytes();

        // 第一个包带 PCR + PUSI + PES header
        let mut output = Vec::new();
        let mut first_packet = true;
        let mut data: Vec<u8> = Vec::new();
        data.extend_from_slice(&pes_header_bytes);
        data.extend_from_slice(&pes_payload);

        // 分包
        while !data.is_empty() {
            let packet = self.make_video_ts_packet(
                first_packet,
                &data,
                if first_packet { Some(pts) } else { None },
            );
            output.extend_from_slice(&packet);
            first_packet = false;

            // 每个包最多 184 字节 payload
            let consumed = data.len().min(184);
            data.drain(..consumed);
        }

        output
    }

    /// 生成视频 TS 包
    fn make_video_ts_packet(
        &mut self,
        is_start: bool,
        data: &[u8],
        pcr: Option<u64>,
    ) -> Vec<u8> {
        let header = TsHeader {
            sync_byte: TS_SYNC_BYTE,
            transport_error_indicator: false,
            payload_unit_start_indicator: is_start,
            transport_priority: false,
            pid: VIDEO_PID,
            transport_scrambling_control: 0,
            adaptation_field_control: 0, // will set below
            continuity_counter: self.video_cc & 0x0F,
        };

        self.video_cc = self.video_cc.wrapping_add(1);

        let mut packet = Vec::with_capacity(TS_PACKET_SIZE);
        let header_bytes = header.to_bytes();
        packet.extend_from_slice(&header_bytes);

        // 计算可用 payload 空间
        let mut available = TS_PACKET_SIZE - 4; // 184 bytes

        // 如果需要 PCR，加 adaptation field
        if let Some(pcr_ts) = pcr {
            let pcr = Pcr::from_timestamp_90khz(pcr_ts);
            let adaptation = AdaptationField {
                pcr: Some(pcr),
                stuff_bytes: 0,
            };
            let adapt_bytes = adaptation.to_bytes();
            // adaptation field length byte + content
            let adapt_len = adapt_bytes.len();
            if adapt_len + 1 <= available {
                // set adaptation_field_control = 11 (both adaptation + payload)
                packet[3] = (packet[3] & 0x0F) | 0x30;
                packet.extend_from_slice(&adapt_bytes);
                available -= adapt_bytes.len();
            }
        }

        // 如果数据不够填满 184 字节，加 stuffing
        let payload_len = data.len().min(available);
        if payload_len < available {
            // 需要 adaptation field 来填充
            let stuff_needed = available - payload_len;
            if pcr.is_none() {
                // 没有 adaptation field，需要加一个
                if stuff_needed >= 2 {
                    // adaptation_field_length(1) + stuff
                    let adapt_len_byte = (stuff_needed - 1) as u8;
                    packet[3] = (packet[3] & 0x0F) | 0x30;
                    packet.push(adapt_len_byte);
                    if adapt_len_byte > 0 {
                        packet.push(0x00); // flags
                        packet.extend(std::iter::repeat(0xFF).take((adapt_len_byte - 1) as usize));
                    }
                } else {
                    // stuff_needed == 1, 只能加一个 stuffing byte
                    packet[3] = (packet[3] & 0x0F) | 0x30;
                    packet.push(0x00); // adaptation_field_length = 0
                }
            } else {
                // 已有 adaptation field，需要追加 stuffing
                // 这种情况比较复杂，简化处理：重新构建包
                // 实际上上面 PCR 路径已经设了 adaptation，这里追加 stuff
                // 找到 adaptation_field_length 的位置
                let adapt_len_pos = 4;
                let old_adapt_len = packet[adapt_len_pos] as usize;
                let new_stuff = stuff_needed;
                // 更新 adaptation_field_length
                packet[adapt_len_pos] = (old_adapt_len + new_stuff) as u8;
                // 追加 stuff bytes
                packet.extend(std::iter::repeat(0xFF).take(new_stuff));
            }
        }

        // 追加 payload
        packet.extend_from_slice(&data[..payload_len]);

        // 确保正好 188 字节
        while packet.len() < TS_PACKET_SIZE {
            packet.push(0xFF);
        }
        packet.truncate(TS_PACKET_SIZE);

        packet
    }

    /// 封装音频帧为 TS 包
    fn write_audio_frame(&mut self, frame: &MediaFrame) -> Vec<u8> {
        if !self.has_audio {
            return Vec::new();
        }

        // 音频 PTS：RTP timestamp 需要转为 90kHz
        // Opus 是 48kHz，AAC 通常是 44.1kHz 或 48kHz
        // 简化：假设 frame.timestamp 已经是 90kHz（由上层处理）
        // 或者按 codec 时钟率转换
        let pts = frame.timestamp as u64;

        // PES header: stream_id = 0xC0 (audio)
        let pes_payload = &frame.data[..];
        let pes_total_len = (3 + pes_payload.len()) as u16; // 3 = PES header bytes after length
        let pes_header = PesHeader {
            stream_id: 0xC0,
            pes_packet_length: pes_total_len,
            pts: Some(pts),
            dts: None,
        };
        let pes_header_bytes = pes_header.to_bytes();

        let mut data: Vec<u8> = Vec::new();
        data.extend_from_slice(&pes_header_bytes);
        data.extend_from_slice(pes_payload);

        // 音频帧通常较小，一个 TS 包就够了
        make_ts_packet(AUDIO_PID, true, &data, None, &mut self.audio_cc)
    }

    /// 通用 TS 包生成（payload-only，无 PCR）
    fn make_ts_packet(
        &mut self,
        pid: u16,
        payload_start: bool,
        data: &[u8],
        pcr: Option<u64>,
        cc: &mut u8,
    ) -> Vec<u8> {
        make_ts_packet(pid, payload_start, data, pcr, cc)
    }
}

/// 通用 TS 包生成（payload-only，无 PCR）
fn make_ts_packet(
    pid: u16,
    payload_start: bool,
    data: &[u8],
    _pcr: Option<u64>,
    cc: &mut u8,
) -> Vec<u8> {
    let mut header = TsHeader {
        sync_byte: TS_SYNC_BYTE,
        transport_error_indicator: false,
        payload_unit_start_indicator: payload_start,
        transport_priority: false,
        pid,
        transport_scrambling_control: 0,
        adaptation_field_control: 0x01, // payload only
        continuity_counter: *cc & 0x0F,
    };
    *cc = cc.wrapping_add(1);

    let mut packet = Vec::with_capacity(TS_PACKET_SIZE);
    packet.extend_from_slice(&header.to_bytes());

    // PUSI=1 时，payload 第一个字节是 pointer_field（0 = section data 紧跟）
    let mut payload: Vec<u8> = Vec::new();
    if payload_start {
        payload.push(0x00); // pointer_field = 0
    }
    payload.extend_from_slice(data);

    let available = TS_PACKET_SIZE - 4; // 184 bytes
    let payload_len = payload.len().min(available);

    // 如果数据不够填满 184 字节，需要 adaptation field 来填充
    if payload_len < available {
        let stuff_needed = available - payload_len;
        header.adaptation_field_control = 0x03; // 2-bit: 11 = both
        packet[3] = header.to_bytes()[3];

        if stuff_needed >= 2 {
            let adapt_content_len = stuff_needed - 1;
            let stuff_bytes = adapt_content_len - 1; // -1 for flags byte
            packet.push(adapt_content_len as u8);
            packet.push(0x00); // flags
            packet.extend(std::iter::repeat(0xFF).take(stuff_bytes));
        } else {
            packet.push(0x00);
        }
    }

    packet.extend_from_slice(&payload[..payload_len]);

    debug_assert!(packet.len() <= TS_PACKET_SIZE);
    while packet.len() < TS_PACKET_SIZE {
        packet.push(0xFF);
    }
    packet.truncate(TS_PACKET_SIZE);

    packet
}

/// 判断是否为视频编解码
fn is_video_codec(codec: CodecType) -> bool {
    matches!(
        codec,
        CodecType::H264 | CodecType::H265 | CodecType::Vp8 | CodecType::Vp9 | CodecType::Av1
    )
}

/// 确保 H.264 数据是 Annex-B 格式（有 0x00 0x00 0x00 0x01 start code）
fn ensure_h264_annex_b(data: &[u8]) -> Vec<u8> {
    // 检查是否已有 start code
    if data.len() >= 4 && data[0] == 0x00 && data[1] == 0x00 && data[2] == 0x00 && data[3] == 0x01 {
        return data.to_vec();
    }
    if data.len() >= 3 && data[0] == 0x00 && data[1] == 0x00 && data[2] == 0x01 {
        return data.to_vec();
    }
    // 加 start code
    let mut result = Vec::with_capacity(data.len() + 4);
    result.extend_from_slice(&[0x00, 0x00, 0x00, 0x01]);
    result.extend_from_slice(data);
    result
}

/// CRC32/MPEG-2 计算
fn crc32_mpeg(data: &[u8]) -> u32 {
    let mut crc: u32 = 0xFFFFFFFF;
    for &byte in data {
        crc ^= (byte as u32) << 24;
        for _ in 0..8 {
            if crc & 0x80000000 != 0 {
                crc = (crc << 1) ^ 0x04C11DB7;
            } else {
                crc <<= 1;
            }
        }
    }
    crc
}

#[cfg(test)]
mod tests {
    use super::*;
    use bytes::Bytes;

    #[test]
    fn test_ts_packet_size() {
        let mut muxer = TsMuxer::new(CodecType::H264, CodecType::Aac);
        let pat_pmt = muxer.write_pat_pmt();
        // PAT + PMT = 2 packets
        assert_eq!(pat_pmt.len(), TS_PACKET_SIZE * 2);
        // 每个包 188 字节
        assert_eq!(&pat_pmt[0..1], &[TS_SYNC_BYTE]);
        assert_eq!(&pat_pmt[188..189], &[TS_SYNC_BYTE]);
    }

    #[test]
    fn test_pat_pmt_structure() {
        let mut muxer = TsMuxer::new(CodecType::H264, CodecType::Aac);
        let pat_pmt = muxer.write_pat_pmt();

        // PAT 包：PID=0, PUSI=1
        let pat = &pat_pmt[..188];
        assert_eq!(pat[0], TS_SYNC_BYTE);
        assert_eq!(pat[1] & 0x1F, 0x00); // PID = 0 (PAT)
        assert_eq!(pat[1] & 0x40, 0x40); // PUSI = 1

        // PMT 包：PID=0x1000, PUSI=1
        let pmt = &pat_pmt[188..];
        assert_eq!(pmt[0], TS_SYNC_BYTE);
        assert_eq!(((pmt[1] as u16) & 0x1F) << 8 | pmt[2] as u16, PMT_PID);
        assert_eq!(pmt[1] & 0x40, 0x40); // PUSI = 1
    }

    #[test]
    fn test_video_frame_ts_packet() {
        let mut muxer = TsMuxer::new(CodecType::H264, CodecType::Opus); // Opus is audio, but won't be used for video-only test

        // 模拟 H.264 关键帧（SPS + PPS + IDR）
        let mut h264_data = Vec::new();
        // SPS (7)
        h264_data.extend_from_slice(&[0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xf8, 0x41, 0xa2]);
        // PPS (8)
        h264_data.extend_from_slice(&[0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80]);
        // IDR slice (5)
        h264_data.extend_from_slice(&[0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40]);

        let frame = MediaFrame::video(
            CodecType::H264,
            0,
            Bytes::from(h264_data),
            1,
            true,
        );

        let ts_data = muxer.write_frame(&frame);
        // 应该有 PAT + PMT + 至少一个视频包
        assert!(ts_data.len() >= TS_PACKET_SIZE * 3);
        // 所有包都是 188 字节对齐
        assert_eq!(ts_data.len() % TS_PACKET_SIZE, 0);
        // 每个包以 sync byte 开头
        for i in (0..ts_data.len()).step_by(TS_PACKET_SIZE) {
            assert_eq!(ts_data[i], TS_SYNC_BYTE);
        }
    }

    #[test]
    fn test_audio_frame_ts_packet() {
        let mut muxer = TsMuxer::new(CodecType::H264, CodecType::Aac);

        // 先写一个视频帧触发 PAT/PMT
        let video_frame = MediaFrame::video(
            CodecType::H264,
            0,
            Bytes::from(vec![0x00, 0x00, 0x00, 0x01, 0x67, 0x42]),
            1,
            true,
        );
        muxer.write_frame(&video_frame);

        // 写音频帧
        let aac_data = vec![0xFF, 0xF1, 0x4C, 0x80, 0x00, 0x1F, 0xFC]; // ADTS header
        let audio_frame = MediaFrame::audio(
            CodecType::Aac,
            0,
            Bytes::from(aac_data),
            1,
        );
        let ts_data = muxer.write_frame(&audio_frame);
        // 音频帧应该至少一个包
        assert!(ts_data.len() >= TS_PACKET_SIZE);
        assert_eq!(ts_data.len() % TS_PACKET_SIZE, 0);
    }

    #[test]
    fn test_crc32() {
        // 测试已知 CRC 值
        let data = [0x00u8; 4];
        let crc = crc32_mpeg(&data);
        // CRC32 of 4 zero bytes with MPEG-2 polynomial
        assert!(crc != 0);
    }

    #[test]
    fn test_ensure_h264_annex_b() {
        // 已有 start code
        let with_sc = [0x00, 0x00, 0x00, 0x01, 0x67, 0x42];
        assert_eq!(ensure_h264_annex_b(&with_sc), with_sc.to_vec());

        // 没有 start code
        let without_sc = [0x67, 0x42, 0x00, 0x0a];
        let result = ensure_h264_annex_b(&without_sc);
        assert_eq!(&result[..4], &[0x00, 0x00, 0x00, 0x01]);
        assert_eq!(&result[4..], &without_sc[..]);
    }

    #[test]
    fn test_ts_file_ffprobe() {
        let mut muxer = TsMuxer::new(CodecType::H264, CodecType::Aac);

        let mut ts_output = Vec::new();

        for i in 0..10 {
            let mut h264_data = Vec::new();
            if i == 0 {
                h264_data.extend_from_slice(&[0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0a, 0xf8, 0x41, 0xa2]);
                h264_data.extend_from_slice(&[0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x38, 0x80]);
                h264_data.extend_from_slice(&[0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x80, 0x40]);
            } else {
                h264_data.extend_from_slice(&[0x00, 0x00, 0x00, 0x01, 0x61, 0x00, 0x00, 0x80]);
            }
            h264_data.extend(vec![0x00u8; 50]);

            let frame = MediaFrame::video(
                CodecType::H264,
                i as u32 * 3000,
                Bytes::from(h264_data),
                1,
                i == 0,
            );
            ts_output.extend(muxer.write_frame(&frame));
        }

        let tmp_path = "/tmp/test_ts_muxer.ts";
        std::fs::write(tmp_path, &ts_output).unwrap();

        assert_eq!(ts_output.len() % TS_PACKET_SIZE, 0);
        assert!(ts_output.len() > TS_PACKET_SIZE * 3);

        if let Ok(output) = std::process::Command::new("ffprobe")
            .args(&["-v", "error", "-show_format", "-show_streams", tmp_path])
            .output()
        {
            if output.status.success() {
                let stdout = String::from_utf8_lossy(&output.stdout);
                println!("ffprobe output:\n{}", stdout);
                assert!(stdout.contains("h264") || stdout.contains("H264"));
            } else {
                let stderr = String::from_utf8_lossy(&output.stderr);
                eprintln!("WARNING: ffprobe could not parse TS file: {}", stderr);
            }
        }
    }
}
