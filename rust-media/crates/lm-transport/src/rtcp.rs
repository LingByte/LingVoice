//! RTCP 包解析与编码（RFC 3550 / RFC 4585）
//!
//! 支持的包类型：
//! - Sender Report (SR) — PT=200
//! - Receiver Report (RR) — PT=201
//! - SDES — PT=202（仅 CNAME）
//! - BYE — PT=203
//! - PLI — PT=206, FMT=1
//! - FIR — PT=206, FMT=4
//! - NACK — PT=206, FMT=1

use bytes::{BufMut, BytesMut};
use thiserror::Error;

// ============================================================================
// 错误类型
// ============================================================================

/// RTCP 解析/编码错误
#[derive(Debug, Error, PartialEq, Eq)]
pub enum RtcpError {
    #[error("数据过短: 需要 {needed} 字节, 实际 {got}")]
    TooShort { needed: usize, got: usize },
    #[error("无效版本: {0} (应为 2)")]
    InvalidVersion(u8),
    #[error("不支持的包类型: PT={0}")]
    UnsupportedPacketType(u8),
    #[error("不支持的 FMT: {0}")]
    UnsupportedFmt(u8),
    #[error("长度字段与实际数据不匹配: 声明 {declared} 字节, 实际 {actual} 字节")]
    LengthMismatch { declared: usize, actual: usize },
    #[error("无效的 SDES 项类型: {0}")]
    InvalidSdesItem(u8),
    #[error("复合包为空")]
    EmptyCompound,
}

// ============================================================================
// 常量
// ============================================================================

const VERSION: u8 = 2;

const PT_SR: u8 = 200;
const PT_RR: u8 = 201;
const PT_SDES: u8 = 202;
const PT_BYE: u8 = 203;
const PT_RTPFB: u8 = 205; // RTP feedback (NACK)
const PT_PSFB: u8 = 206; // Payload-specific feedback (PLI, FIR)

const FMT_PLI: u8 = 1;
const FMT_NACK: u8 = 1;
const FMT_FIR: u8 = 4;

// ============================================================================
// Report Block（SR/RR 共享）
// ============================================================================

/// 接收报告块（24 字节）
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ReportBlock {
    pub ssrc: u32,
    /// 丢失分数（自上次 SR/RR 以来丢失包数 / 期望包数，定点 8 位）
    pub fraction_lost: u8,
    /// 累计丢失包数（24 位）
    pub cumulative_lost: u32,
    /// 最高接收到的 RTP 序列号
    pub highest_seq: u32,
    /// 到达时间抖动
    pub jitter: u32,
    /// 上次 SR 的 NTP 时间戳中 32 位（LSR）
    pub lsr: u32,
    /// 自上次 SR 以来的延迟（DLSR，单位 1/65536 秒）
    pub dlsr: u32,
}

impl ReportBlock {
    /// 解析单个报告块，返回 (ReportBlock, 消耗字节数)
    pub fn parse(data: &[u8]) -> Result<(Self, usize), RtcpError> {
        if data.len() < 24 {
            return Err(RtcpError::TooShort {
                needed: 24,
                got: data.len(),
            });
        }

        let ssrc = u32::from_be_bytes([data[0], data[1], data[2], data[3]]);
        let fraction_lost = data[4];
        // cumulative_lost 是 24 位
        let cumulative_lost = u32::from_be_bytes([0, data[5], data[6], data[7]]);
        let highest_seq = u32::from_be_bytes([data[8], data[9], data[10], data[11]]);
        let jitter = u32::from_be_bytes([data[12], data[13], data[14], data[15]]);
        let lsr = u32::from_be_bytes([data[16], data[17], data[18], data[19]]);
        let dlsr = u32::from_be_bytes([data[20], data[21], data[22], data[23]]);

        Ok((
            Self {
                ssrc,
                fraction_lost,
                cumulative_lost,
                highest_seq,
                jitter,
                lsr,
                dlsr,
            },
            24,
        ))
    }

    /// 编码为 24 字节
    pub fn encode(&self) -> Vec<u8> {
        let mut buf = Vec::with_capacity(24);
        buf.put_u32(self.ssrc);
        buf.put_u8(self.fraction_lost);
        // cumulative_lost 只取低 24 位
        buf.put_u8((self.cumulative_lost >> 16) as u8 & 0xFF);
        buf.put_u8((self.cumulative_lost >> 8) as u8 & 0xFF);
        buf.put_u8(self.cumulative_lost as u8 & 0xFF);
        buf.put_u32(self.highest_seq);
        buf.put_u32(self.jitter);
        buf.put_u32(self.lsr);
        buf.put_u32(self.dlsr);
        buf
    }

    /// 计算 RTT: RTT = arrival_ntp_mid - LSR - DLSR（单位 1/65536 秒）
    pub fn calculate_rtt(&self, arrival_ntp_mid: u32) -> u32 {
        if self.lsr == 0 || self.dlsr == 0 {
            return 0;
        }
        arrival_ntp_mid
            .wrapping_sub(self.lsr)
            .wrapping_sub(self.dlsr)
    }
}

// ============================================================================
// Sender Report (SR) — PT=200
// ============================================================================

/// 发送者报告
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SenderReport {
    pub ssrc: u32,
    /// NTP 时间戳（完整 64 位）
    pub ntp_timestamp: u64,
    /// RTP 时间戳
    pub rtp_timestamp: u32,
    /// 发送者包数
    pub sender_packet_count: u32,
    /// 发送者字节数
    pub sender_octet_count: u32,
    /// 接收报告块
    pub report_blocks: Vec<ReportBlock>,
}

impl SenderReport {
    pub fn parse(data: &[u8]) -> Result<Self, RtcpError> {
        // 最小: 4 (header) + 24 (sender info) = 28
        if data.len() < 28 {
            return Err(RtcpError::TooShort {
                needed: 28,
                got: data.len(),
            });
        }

        let rc = (data[0] & 0x1F) as usize;
        let pt = data[1];
        if pt != PT_SR {
            return Err(RtcpError::UnsupportedPacketType(pt));
        }
        let length = u16::from_be_bytes([data[2], data[3]]) as usize;
        let declared_bytes = (length + 1) * 4;
        if data.len() < declared_bytes {
            return Err(RtcpError::LengthMismatch {
                declared: declared_bytes,
                actual: data.len(),
            });
        }

        let ssrc = u32::from_be_bytes([data[4], data[5], data[6], data[7]]);
        let ntp_timestamp = u64::from_be_bytes([
            data[8], data[9], data[10], data[11], data[12], data[13], data[14], data[15],
        ]);
        let rtp_timestamp = u32::from_be_bytes([data[16], data[17], data[18], data[19]]);
        let sender_packet_count = u32::from_be_bytes([data[20], data[21], data[22], data[23]]);
        let sender_octet_count = u32::from_be_bytes([data[24], data[25], data[26], data[27]]);

        let mut offset = 28;
        let mut report_blocks = Vec::with_capacity(rc);
        for _ in 0..rc {
            let (block, consumed) = ReportBlock::parse(&data[offset..])?;
            report_blocks.push(block);
            offset += consumed;
        }

        Ok(Self {
            ssrc,
            ntp_timestamp,
            rtp_timestamp,
            sender_packet_count,
            sender_octet_count,
            report_blocks,
        })
    }

    pub fn encode(&self) -> Vec<u8> {
        let rc = self.report_blocks.len().min(31) as u8;
        let mut buf = BytesMut::with_capacity(28 + self.report_blocks.len() * 24);

        // header
        buf.put_u8((VERSION << 6) | rc);
        buf.put_u8(PT_SR);
        // length 填充占位
        buf.put_u16(0);

        // sender info
        buf.put_u32(self.ssrc);
        buf.put_u64(self.ntp_timestamp);
        buf.put_u32(self.rtp_timestamp);
        buf.put_u32(self.sender_packet_count);
        buf.put_u32(self.sender_octet_count);

        // report blocks
        for rb in &self.report_blocks {
            buf.put_slice(&rb.encode());
        }

        // 修正 length
        let words = buf.len() / 4;
        let length = (words - 1) as u16;
        buf[2] = (length >> 8) as u8;
        buf[3] = (length & 0xFF) as u8;

        buf.to_vec()
    }
}

// ============================================================================
// Receiver Report (RR) — PT=201
// ============================================================================

/// 接收者报告
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ReceiverReport {
    pub ssrc: u32,
    pub report_blocks: Vec<ReportBlock>,
}

impl ReceiverReport {
    pub fn parse(data: &[u8]) -> Result<Self, RtcpError> {
        // 最小: 4 (header) + 4 (ssrc) = 8
        if data.len() < 8 {
            return Err(RtcpError::TooShort {
                needed: 8,
                got: data.len(),
            });
        }

        let rc = (data[0] & 0x1F) as usize;
        let pt = data[1];
        if pt != PT_RR {
            return Err(RtcpError::UnsupportedPacketType(pt));
        }
        let length = u16::from_be_bytes([data[2], data[3]]) as usize;
        let declared_bytes = (length + 1) * 4;
        if data.len() < declared_bytes {
            return Err(RtcpError::LengthMismatch {
                declared: declared_bytes,
                actual: data.len(),
            });
        }

        let ssrc = u32::from_be_bytes([data[4], data[5], data[6], data[7]]);

        let mut offset = 8;
        let mut report_blocks = Vec::with_capacity(rc);
        for _ in 0..rc {
            let (block, consumed) = ReportBlock::parse(&data[offset..])?;
            report_blocks.push(block);
            offset += consumed;
        }

        Ok(Self {
            ssrc,
            report_blocks,
        })
    }

    pub fn encode(&self) -> Vec<u8> {
        let rc = self.report_blocks.len().min(31) as u8;
        let mut buf = BytesMut::with_capacity(8 + self.report_blocks.len() * 24);

        buf.put_u8((VERSION << 6) | rc);
        buf.put_u8(PT_RR);
        buf.put_u16(0); // length 占位

        buf.put_u32(self.ssrc);

        for rb in &self.report_blocks {
            buf.put_slice(&rb.encode());
        }

        let words = buf.len() / 4;
        let length = (words - 1) as u16;
        buf[2] = (length >> 8) as u8;
        buf[3] = (length & 0xFF) as u8;

        buf.to_vec()
    }
}

// ============================================================================
// SDES — PT=202 (仅 CNAME)
// ============================================================================

/// SDES 包（仅支持 CNAME）
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SdesPacket {
    pub ssrc: u32,
    pub cname: String,
}

impl SdesPacket {
    pub fn parse(data: &[u8]) -> Result<Self, RtcpError> {
        // 最小: 4 (header) + 4 (ssrc) + 2 (CNAME type+len) = 10
        if data.len() < 10 {
            return Err(RtcpError::TooShort {
                needed: 10,
                got: data.len(),
            });
        }

        let pt = data[1];
        if pt != PT_SDES {
            return Err(RtcpError::UnsupportedPacketType(pt));
        }
        let length = u16::from_be_bytes([data[2], data[3]]) as usize;
        let declared_bytes = (length + 1) * 4;
        if data.len() < declared_bytes {
            return Err(RtcpError::LengthMismatch {
                declared: declared_bytes,
                actual: data.len(),
            });
        }

        let ssrc = u32::from_be_bytes([data[4], data[5], data[6], data[7]]);

        // chunk: type(1) + len(1) + text(len)
        let mut offset = 8;

        // 第一个 chunk 的第一个 item 应该是 CNAME (type=1)
        let item_type = data[offset];
        if item_type != 1 {
            return Err(RtcpError::InvalidSdesItem(item_type));
        }
        offset += 1;
        let item_len = data[offset] as usize;
        offset += 1;
        if offset + item_len > data.len() {
            return Err(RtcpError::TooShort {
                needed: offset + item_len,
                got: data.len(),
            });
        }
        let cname = String::from_utf8_lossy(&data[offset..offset + item_len]).to_string();

        Ok(Self { ssrc, cname })
    }

    pub fn encode(&self) -> Vec<u8> {
        let cname_bytes = self.cname.as_bytes();
        let item_len = cname_bytes.len().min(255);
        // chunk: ssrc(4) + type(1) + len(1) + text(item_len) + null(1) + padding
        let chunk_len = 4 + 2 + item_len + 1; // +1 for null terminator item
        let padded_chunk_len = (chunk_len + 3) & !3; // 4 字节对齐

        let mut buf = BytesMut::with_capacity(4 + padded_chunk_len);

        // header: V=2, RC=1
        buf.put_u8((VERSION << 6) | 1);
        buf.put_u8(PT_SDES);
        buf.put_u16(0); // length 占位

        // chunk
        buf.put_u32(self.ssrc);
        buf.put_u8(1); // CNAME type
        buf.put_u8(item_len as u8);
        buf.put_slice(&cname_bytes[..item_len]);
        buf.put_u8(0); // null terminator item (type=0, len=0)

        // padding
        while buf.len() % 4 != 0 {
            buf.put_u8(0);
        }

        let words = buf.len() / 4;
        let length = (words - 1) as u16;
        buf[2] = (length >> 8) as u8;
        buf[3] = (length & 0xFF) as u8;

        buf.to_vec()
    }
}

// ============================================================================
// BYE — PT=203
// ============================================================================

/// BYE 包
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ByePacket {
    pub ssrcs: Vec<u32>,
    pub reason: String,
}

impl ByePacket {
    pub fn parse(data: &[u8]) -> Result<Self, RtcpError> {
        // 最小: 4 (header) + 4 (ssrc) = 8
        if data.len() < 8 {
            return Err(RtcpError::TooShort {
                needed: 8,
                got: data.len(),
            });
        }

        let sc = (data[0] & 0x1F) as usize;
        let pt = data[1];
        if pt != PT_BYE {
            return Err(RtcpError::UnsupportedPacketType(pt));
        }
        let length = u16::from_be_bytes([data[2], data[3]]) as usize;
        let declared_bytes = (length + 1) * 4;
        if data.len() < declared_bytes {
            return Err(RtcpError::LengthMismatch {
                declared: declared_bytes,
                actual: data.len(),
            });
        }

        let mut offset = 4;
        let mut ssrcs = Vec::with_capacity(sc);
        for _ in 0..sc {
            if offset + 4 > data.len() {
                return Err(RtcpError::TooShort {
                    needed: offset + 4,
                    got: data.len(),
                });
            }
            ssrcs.push(u32::from_be_bytes([
                data[offset],
                data[offset + 1],
                data[offset + 2],
                data[offset + 3],
            ]));
            offset += 4;
        }

        // 可选的 reason 字段
        let reason = if offset < declared_bytes && offset < data.len() {
            let reason_len = data[offset] as usize;
            offset += 1;
            if offset + reason_len <= data.len() {
                String::from_utf8_lossy(&data[offset..offset + reason_len]).to_string()
            } else {
                String::new()
            }
        } else {
            String::new()
        };

        Ok(Self { ssrcs, reason })
    }

    pub fn encode(&self) -> Vec<u8> {
        let sc = self.ssrcs.len().min(31) as u8;
        let reason_bytes = self.reason.as_bytes();
        let reason_len = reason_bytes.len().min(255);

        // header(4) + ssrcs + reason_len(1) + reason + padding
        let base = 4 + self.ssrcs.len() * 4 + 1 + reason_len;
        let padded = (base + 3) & !3;

        let mut buf = BytesMut::with_capacity(padded);

        buf.put_u8((VERSION << 6) | sc);
        buf.put_u8(PT_BYE);
        buf.put_u16(0); // length 占位

        for ssrc in &self.ssrcs {
            buf.put_u32(*ssrc);
        }

        if reason_len > 0 {
            buf.put_u8(reason_len as u8);
            buf.put_slice(&reason_bytes[..reason_len]);
        } else {
            buf.put_u8(0);
        }

        while buf.len() % 4 != 0 {
            buf.put_u8(0);
        }

        let words = buf.len() / 4;
        let length = (words - 1) as u16;
        buf[2] = (length >> 8) as u8;
        buf[3] = (length & 0xFF) as u8;

        buf.to_vec()
    }
}

// ============================================================================
// PLI — PT=206, FMT=1
// ============================================================================

/// Picture Loss Indication
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PliPacket {
    /// 发送方 SSRC
    pub sender_ssrc: u32,
    /// 媒体源 SSRC
    pub media_ssrc: u32,
}

impl PliPacket {
    pub fn parse(data: &[u8]) -> Result<Self, RtcpError> {
        // 4 (header) + 4 (sender) + 4 (media) = 12, PLI 无 FCI
        if data.len() < 12 {
            return Err(RtcpError::TooShort {
                needed: 12,
                got: data.len(),
            });
        }

        let fmt = data[0] & 0x1F;
        let pt = data[1];
        if pt != PT_PSFB {
            return Err(RtcpError::UnsupportedPacketType(pt));
        }
        if fmt != FMT_PLI {
            return Err(RtcpError::UnsupportedFmt(fmt));
        }

        let sender_ssrc = u32::from_be_bytes([data[4], data[5], data[6], data[7]]);
        let media_ssrc = u32::from_be_bytes([data[8], data[9], data[10], data[11]]);

        Ok(Self {
            sender_ssrc,
            media_ssrc,
        })
    }

    pub fn encode(&self) -> Vec<u8> {
        let mut buf = BytesMut::with_capacity(12);
        // V=2, P=0, FMT=1
        buf.put_u8((VERSION << 6) | FMT_PLI);
        buf.put_u8(PT_PSFB);
        buf.put_u16(2); // length = 3 words - 1 = 2
        buf.put_u32(self.sender_ssrc);
        buf.put_u32(self.media_ssrc);
        buf.to_vec()
    }
}

// ============================================================================
// FIR — PT=206, FMT=4
// ============================================================================

/// Full Intra Request
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FirEntry {
    pub ssrc: u32,
    pub seq: u8,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FirPacket {
    pub sender_ssrc: u32,
    pub media_ssrc: u32,
    pub entries: Vec<FirEntry>,
}

impl FirPacket {
    pub fn parse(data: &[u8]) -> Result<Self, RtcpError> {
        // 4 (header) + 4 (sender) + 4 (media) + 8 (entry) = 16
        if data.len() < 16 {
            return Err(RtcpError::TooShort {
                needed: 16,
                got: data.len(),
            });
        }

        let fmt = data[0] & 0x1F;
        let pt = data[1];
        if pt != PT_PSFB {
            return Err(RtcpError::UnsupportedPacketType(pt));
        }
        if fmt != FMT_FIR {
            return Err(RtcpError::UnsupportedFmt(fmt));
        }

        let length = u16::from_be_bytes([data[2], data[3]]) as usize;
        let declared_bytes = (length + 1) * 4;

        let sender_ssrc = u32::from_be_bytes([data[4], data[5], data[6], data[7]]);
        let media_ssrc = u32::from_be_bytes([data[8], data[9], data[10], data[11]]);

        // FCI entries: 每个 8 字节 (ssrc(4) + seq(1) + reserved(3))
        let mut offset = 12;
        let mut entries = Vec::new();
        while offset + 8 <= declared_bytes && offset + 8 <= data.len() {
            let ssrc = u32::from_be_bytes([
                data[offset],
                data[offset + 1],
                data[offset + 2],
                data[offset + 3],
            ]);
            let seq = data[offset + 4];
            entries.push(FirEntry { ssrc, seq });
            offset += 8;
        }

        Ok(Self {
            sender_ssrc,
            media_ssrc,
            entries,
        })
    }

    pub fn encode(&self) -> Vec<u8> {
        let mut buf = BytesMut::with_capacity(12 + self.entries.len() * 8);

        buf.put_u8((VERSION << 6) | FMT_FIR);
        buf.put_u8(PT_PSFB);
        buf.put_u16(0); // length 占位

        buf.put_u32(self.sender_ssrc);
        buf.put_u32(self.media_ssrc);

        for entry in &self.entries {
            buf.put_u32(entry.ssrc);
            buf.put_u8(entry.seq);
            buf.put_u8(0);
            buf.put_u8(0);
            buf.put_u8(0);
        }

        let words = buf.len() / 4;
        let length = (words - 1) as u16;
        buf[2] = (length >> 8) as u8;
        buf[3] = (length & 0xFF) as u8;

        buf.to_vec()
    }
}

// ============================================================================
// NACK — PT=206, FMT=1
// ============================================================================

/// NACK 项
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct NackItem {
    /// 丢失的 RTP 包的起始序列号
    pub pid: u16,
    /// BLP (bitmap of lost packets) — 跟随 pid 之后的 16 个包的丢失位图
    pub blp: u16,
}

impl NackItem {
    /// 返回 NACK 指示的所有丢失序列号
    pub fn lost_sequence_numbers(&self) -> Vec<u16> {
        let mut seqs = vec![self.pid];
        for i in 0..16u32 {
            if (self.blp >> i) & 1 == 1 {
                seqs.push(self.pid.wrapping_add((i + 1) as u16));
            }
        }
        seqs
    }
}

/// NACK 包
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct NackPacket {
    pub sender_ssrc: u32,
    pub media_ssrc: u32,
    pub items: Vec<NackItem>,
}

impl NackPacket {
    pub fn parse(data: &[u8]) -> Result<Self, RtcpError> {
        // 4 (header) + 4 (sender) + 4 (media) + 4 (item) = 16
        if data.len() < 16 {
            return Err(RtcpError::TooShort {
                needed: 16,
                got: data.len(),
            });
        }

        let fmt = data[0] & 0x1F;
        let pt = data[1];
        if pt != PT_RTPFB {
            return Err(RtcpError::UnsupportedPacketType(pt));
        }
        if fmt != FMT_NACK {
            return Err(RtcpError::UnsupportedFmt(fmt));
        }

        let length = u16::from_be_bytes([data[2], data[3]]) as usize;
        let declared_bytes = (length + 1) * 4;

        let sender_ssrc = u32::from_be_bytes([data[4], data[5], data[6], data[7]]);
        let media_ssrc = u32::from_be_bytes([data[8], data[9], data[10], data[11]]);

        let mut offset = 12;
        let mut items = Vec::new();
        while offset + 4 <= declared_bytes && offset + 4 <= data.len() {
            let pid = u16::from_be_bytes([data[offset], data[offset + 1]]);
            let blp = u16::from_be_bytes([data[offset + 2], data[offset + 3]]);
            items.push(NackItem { pid, blp });
            offset += 4;
        }

        Ok(Self {
            sender_ssrc,
            media_ssrc,
            items,
        })
    }

    pub fn encode(&self) -> Vec<u8> {
        let mut buf = BytesMut::with_capacity(12 + self.items.len() * 4);

        buf.put_u8((VERSION << 6) | FMT_NACK);
        buf.put_u8(PT_RTPFB);
        buf.put_u16(0); // length 占位

        buf.put_u32(self.sender_ssrc);
        buf.put_u32(self.media_ssrc);

        for item in &self.items {
            buf.put_u16(item.pid);
            buf.put_u16(item.blp);
        }

        let words = buf.len() / 4;
        let length = (words - 1) as u16;
        buf[2] = (length >> 8) as u8;
        buf[3] = (length & 0xFF) as u8;

        buf.to_vec()
    }
}

// ============================================================================
// RtcpPacket 枚举
// ============================================================================

/// RTCP 包枚举
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RtcpPacket {
    SenderReport(SenderReport),
    ReceiverReport(ReceiverReport),
    Sdes(SdesPacket),
    Bye(ByePacket),
    Pli(PliPacket),
    Fir(FirPacket),
    Nack(NackPacket),
}

impl RtcpPacket {
    /// 编码为字节流
    pub fn encode(&self) -> Vec<u8> {
        match self {
            Self::SenderReport(p) => p.encode(),
            Self::ReceiverReport(p) => p.encode(),
            Self::Sdes(p) => p.encode(),
            Self::Bye(p) => p.encode(),
            Self::Pli(p) => p.encode(),
            Self::Fir(p) => p.encode(),
            Self::Nack(p) => p.encode(),
        }
    }

    /// 解析单个 RTCP 包，返回 (RtcpPacket, 消耗字节数)
    pub fn parse(data: &[u8]) -> Result<(Self, usize), RtcpError> {
        if data.len() < 4 {
            return Err(RtcpError::TooShort {
                needed: 4,
                got: data.len(),
            });
        }

        let version = (data[0] >> 6) & 0x03;
        if version != VERSION {
            return Err(RtcpError::InvalidVersion(version));
        }

        let pt = data[1];
        let length = u16::from_be_bytes([data[2], data[3]]) as usize;
        let packet_bytes = (length + 1) * 4;
        if data.len() < packet_bytes {
            return Err(RtcpError::LengthMismatch {
                declared: packet_bytes,
                actual: data.len(),
            });
        }

        let pkt_data = &data[..packet_bytes];
        let pkt = match pt {
            PT_SR => Self::SenderReport(SenderReport::parse(pkt_data)?),
            PT_RR => Self::ReceiverReport(ReceiverReport::parse(pkt_data)?),
            PT_SDES => Self::Sdes(SdesPacket::parse(pkt_data)?),
            PT_BYE => Self::Bye(ByePacket::parse(pkt_data)?),
            PT_RTPFB => {
                let fmt = data[0] & 0x1F;
                match fmt {
                    FMT_NACK => Self::Nack(NackPacket::parse(pkt_data)?),
                    other => return Err(RtcpError::UnsupportedFmt(other)),
                }
            }
            PT_PSFB => {
                let fmt = data[0] & 0x1F;
                match fmt {
                    FMT_PLI => Self::Pli(PliPacket::parse(pkt_data)?),
                    FMT_FIR => Self::Fir(FirPacket::parse(pkt_data)?),
                    other => return Err(RtcpError::UnsupportedFmt(other)),
                }
            }
            other => return Err(RtcpError::UnsupportedPacketType(other)),
        };

        Ok((pkt, packet_bytes))
    }
}

// ============================================================================
// 复合包解析
// ============================================================================

/// 解析复合 RTCP 包（多个 RTCP 包拼接在一起）
pub fn parse_compound(data: &[u8]) -> Result<Vec<RtcpPacket>, RtcpError> {
    if data.is_empty() {
        return Err(RtcpError::EmptyCompound);
    }

    let mut packets = Vec::new();
    let mut offset = 0;

    while offset < data.len() {
        let (pkt, consumed) = RtcpPacket::parse(&data[offset..])?;
        packets.push(pkt);
        offset += consumed;
    }

    Ok(packets)
}

// ============================================================================
// 测试
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_sr_encode_decode_roundtrip() {
        let sr = SenderReport {
            ssrc: 0x12345678,
            ntp_timestamp: 0x0000000100000002,
            rtp_timestamp: 1600,
            sender_packet_count: 100,
            sender_octet_count: 6400,
            report_blocks: vec![ReportBlock {
                ssrc: 0xAABBCCDD,
                fraction_lost: 10,
                cumulative_lost: 5,
                highest_seq: 1000,
                jitter: 3,
                lsr: 0x12345678,
                dlsr: 0x00010000,
            }],
        };

        let encoded = sr.encode();
        let (parsed, consumed) = RtcpPacket::parse(&encoded).unwrap();
        assert_eq!(consumed, encoded.len());

        match parsed {
            RtcpPacket::SenderReport(p) => {
                assert_eq!(p, sr);
            }
            _ => panic!("应为 SenderReport"),
        }
    }

    #[test]
    fn test_rr_with_report_blocks() {
        let rr = ReceiverReport {
            ssrc: 0xDEADBEEF,
            report_blocks: vec![
                ReportBlock {
                    ssrc: 0x11111111,
                    fraction_lost: 0,
                    cumulative_lost: 0,
                    highest_seq: 500,
                    jitter: 2,
                    lsr: 0,
                    dlsr: 0,
                },
                ReportBlock {
                    ssrc: 0x22222222,
                    fraction_lost: 25,
                    cumulative_lost: 10,
                    highest_seq: 600,
                    jitter: 5,
                    lsr: 0xAAAAAAAA,
                    dlsr: 0x0000FFFF,
                },
            ],
        };

        let encoded = rr.encode();
        let (parsed, _) = RtcpPacket::parse(&encoded).unwrap();

        match parsed {
            RtcpPacket::ReceiverReport(p) => {
                assert_eq!(p.ssrc, rr.ssrc);
                assert_eq!(p.report_blocks.len(), 2);
                assert_eq!(p.report_blocks[0], rr.report_blocks[0]);
                assert_eq!(p.report_blocks[1], rr.report_blocks[1]);
            }
            _ => panic!("应为 ReceiverReport"),
        }
    }

    #[test]
    fn test_pli_encode_decode() {
        let pli = PliPacket {
            sender_ssrc: 0x11111111,
            media_ssrc: 0x22222222,
        };

        let encoded = pli.encode();
        assert_eq!(encoded.len(), 12);

        let (parsed, consumed) = RtcpPacket::parse(&encoded).unwrap();
        assert_eq!(consumed, 12);

        match parsed {
            RtcpPacket::Pli(p) => {
                assert_eq!(p, pli);
            }
            _ => panic!("应为 Pli"),
        }
    }

    #[test]
    fn test_fir_encode_decode() {
        let fir = FirPacket {
            sender_ssrc: 0xAAAAAAAA,
            media_ssrc: 0,
            entries: vec![FirEntry {
                ssrc: 0xBBBBBBBB,
                seq: 1,
            }],
        };

        let encoded = fir.encode();
        let (parsed, consumed) = RtcpPacket::parse(&encoded).unwrap();
        assert_eq!(consumed, encoded.len());

        match parsed {
            RtcpPacket::Fir(p) => {
                assert_eq!(p.sender_ssrc, fir.sender_ssrc);
                assert_eq!(p.media_ssrc, fir.media_ssrc);
                assert_eq!(p.entries.len(), 1);
                assert_eq!(p.entries[0], fir.entries[0]);
            }
            _ => panic!("应为 Fir"),
        }
    }

    #[test]
    fn test_nack_encode_decode() {
        // pid=100, blp=0b1010 -> 丢失 100, 102, 104
        let nack = NackPacket {
            sender_ssrc: 0x11111111,
            media_ssrc: 0x22222222,
            items: vec![NackItem {
                pid: 100,
                blp: 0b1010,
            }],
        };

        let encoded = nack.encode();
        let (parsed, consumed) = RtcpPacket::parse(&encoded).unwrap();
        assert_eq!(consumed, encoded.len());

        match parsed {
            RtcpPacket::Nack(p) => {
                assert_eq!(p.sender_ssrc, nack.sender_ssrc);
                assert_eq!(p.media_ssrc, nack.media_ssrc);
                assert_eq!(p.items.len(), 1);
                assert_eq!(p.items[0].pid, 100);
                assert_eq!(p.items[0].blp, 0b1010);

                let lost = p.items[0].lost_sequence_numbers();
                assert_eq!(lost, vec![100, 102, 104]);
            }
            _ => panic!("应为 Nack"),
        }
    }

    #[test]
    fn test_rtt_calculation() {
        let rb = ReportBlock {
            ssrc: 0,
            fraction_lost: 0,
            cumulative_lost: 0,
            highest_seq: 0,
            jitter: 0,
            lsr: 0x00010000,
            dlsr: 0x00005000,
        };

        // RTT = arrival - LSR - DLSR
        // arrival = 0x00020000
        // RTT = 0x00020000 - 0x00010000 - 0x00005000 = 0x0000B000
        let arrival = 0x00020000u32;
        let rtt = rb.calculate_rtt(arrival);
        assert_eq!(rtt, 0x00020000 - 0x00010000 - 0x00005000);

        // LSR=0 时返回 0
        let rb_zero = ReportBlock {
            lsr: 0,
            dlsr: 0,
            ..rb
        };
        assert_eq!(rb_zero.calculate_rtt(arrival), 0);
    }

    #[test]
    fn test_compound_packet_parse() {
        let sr = SenderReport {
            ssrc: 0x12345678,
            ntp_timestamp: 0x0000000100000002,
            rtp_timestamp: 1600,
            sender_packet_count: 100,
            sender_octet_count: 6400,
            report_blocks: vec![],
        };

        let sdes = SdesPacket {
            ssrc: 0x12345678,
            cname: "user@host".to_string(),
        };

        let bye = ByePacket {
            ssrcs: vec![0x12345678],
            reason: "Goodbye".to_string(),
        };

        let mut compound = Vec::new();
        compound.extend_from_slice(&sr.encode());
        compound.extend_from_slice(&sdes.encode());
        compound.extend_from_slice(&bye.encode());

        let packets = parse_compound(&compound).unwrap();
        assert_eq!(packets.len(), 3);

        assert!(matches!(packets[0], RtcpPacket::SenderReport(_)));
        match &packets[1] {
            RtcpPacket::Sdes(p) => {
                assert_eq!(p.ssrc, 0x12345678);
                assert_eq!(p.cname, "user@host");
            }
            _ => panic!("应为 Sdes"),
        }
        match &packets[2] {
            RtcpPacket::Bye(p) => {
                assert_eq!(p.ssrcs, vec![0x12345678]);
                assert_eq!(p.reason, "Goodbye");
            }
            _ => panic!("应为 Bye"),
        }
    }

    #[test]
    fn test_malformed_packet_error() {
        // 数据过短
        let short = [0x80u8, 200, 0, 0];
        assert!(matches!(
            RtcpPacket::parse(&short),
            Err(RtcpError::TooShort { .. })
        ));

        // 无效版本
        let bad_version = [0x40u8, 200, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
        assert!(matches!(
            RtcpPacket::parse(&bad_version),
            Err(RtcpError::InvalidVersion(1))
        ));

        // 不支持的包类型
        let unsupported = vec![0x80u8, 255, 0, 1, 0, 0, 0, 0];
        assert!(matches!(
            RtcpPacket::parse(&unsupported),
            Err(RtcpError::UnsupportedPacketType(255))
        ));

        // 长度字段超出实际数据
        let bad_len = [0x80u8, 200, 0, 100, 0, 0, 0, 0];
        assert!(matches!(
            RtcpPacket::parse(&bad_len),
            Err(RtcpError::LengthMismatch { .. })
        ));

        // 空复合包
        assert!(matches!(parse_compound(&[]), Err(RtcpError::EmptyCompound)));
    }
}
