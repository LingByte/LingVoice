//! TWCC (Transport-Wide Congestion Control)
//!
//! 基于 RFC 8888 / draft-holmer-rmcat-catapult:
//! - 发送方在每个 RTP 包的 transport-wide sequence number (TWCC header extension) 中写入递增序号
//! - 接收方记录每个包的到达时间
//! - 接收方周期性发送 TWCC 反馈 RTCP 包

use crate::rtcp::RtcpError;
use bytes::{BufMut, BytesMut};

// ============================================================================
// 常量
// ============================================================================

const VERSION: u8 = 2;
const PT_RTPFB: u8 = 205;
const FMT_TWCC: u8 = 15;

/// 1 TWCC reference time unit = 64ms = 64000us
const REF_TIME_UNIT_US: i64 = 64_000;
/// 1 delta tick = 250us
const DELTA_TICK_US: i64 = 250;

// ============================================================================
// TWCC 序号分配器 (发送方用)
// ============================================================================

/// TWCC 序号分配器 (发送方用)
pub struct TwccSeqAllocator {
    next_seq: u32,
}

impl TwccSeqAllocator {
    pub fn new() -> Self {
        Self { next_seq: 0 }
    }

    pub fn next(&mut self) -> u32 {
        let s = self.next_seq;
        self.next_seq = self.next_seq.wrapping_add(1);
        s
    }
}

impl Default for TwccSeqAllocator {
    fn default() -> Self {
        Self::new()
    }
}

// ============================================================================
// TWCC 接收方记录器
// ============================================================================

/// TWCC 接收方记录器
pub struct TwccReceiver {
    /// seq -> arrival_time_us
    packets: Vec<(u32, i64)>,
    base_seq: u32,
    last_feedback_seq: u8,
    initialized: bool,
}

impl TwccReceiver {
    pub fn new() -> Self {
        Self {
            packets: Vec::new(),
            base_seq: 0,
            last_feedback_seq: 0,
            initialized: false,
        }
    }

    pub fn record_packet(&mut self, seq: u32, arrival_time_us: i64) {
        if !self.initialized {
            self.base_seq = seq;
            self.initialized = true;
        }
        self.packets.push((seq, arrival_time_us));
    }

    /// 构建一个 TWCC 反馈包字节流
    pub fn build_feedback(&mut self, reference_time_us: i64) -> Vec<u8> {
        // reference_time: 24bit, 64ms 为单位
        let reference_time = ((reference_time_us / REF_TIME_UNIT_US) as u32) & 0x00FF_FFFF;

        // 排序 packets 按 seq
        self.packets.sort_by_key(|(s, _)| *s);

        let base_seq = if let Some(&(s, _)) = self.packets.first() {
            s as u16
        } else {
            0
        };

        // 统计接收到的包数 (在 base_seq..=last_seq 范围内)
        let last_seq = if let Some(&(s, _)) = self.packets.last() {
            s as u16
        } else {
            0
        };

        // 构建接收状态数组 (从 base_seq 到 last_seq)
        let range_len = (last_seq.wrapping_sub(base_seq) as u32 + 1) as usize;
        let mut received = vec![false; range_len];
        let mut arrival_times = vec![0i64; range_len];
        for &(s, t) in &self.packets {
            let idx = (s as u16).wrapping_sub(base_seq) as usize;
            if idx < range_len {
                received[idx] = true;
                arrival_times[idx] = t;
            }
        }

        // packet_count: 实际接收到的包数
        let packet_count = received.iter().filter(|&&r| r).count() as u16;

        // 构建 status chunks (run-length encoded)
        // Run-length chunk: symbol(2 bits) + run length(13 bits)
        //   symbol: 00=not received, 01=small delta, 10=large or negative delta
        let mut status_chunks: Vec<u8> = Vec::new();
        let mut i = 0;
        while i < range_len {
            let r = received[i];
            // 计算 run length
            let mut run_len = 0usize;
            while i + run_len < range_len && run_len < 8191 && received[i + run_len] == r {
                run_len += 1;
            }
            // symbol: 0=not received, 1=received (we use 01 for received, 00 for not received)
            let symbol: u16 = if r { 0b01 } else { 0b00 };
            let chunk: u16 = (symbol << 13) | (run_len as u16 & 0x1FFF);
            status_chunks.put_u16(chunk);
            i += run_len;
        }

        // 构建 deltas: 每个接收到的包相对于前一个接收包的时间差
        // 第一个包的 delta 相对于 reference_time
        let mut deltas: Vec<i16> = Vec::new();
        let mut prev_time = reference_time_us;
        let mut first = true;
        for idx in 0..range_len {
            if !received[idx] {
                continue;
            }
            let t = arrival_times[idx];
            let base = if first { reference_time_us } else { prev_time };
            let delta_us = t - base;
            let delta_ticks = delta_us / DELTA_TICK_US;
            if delta_ticks >= i8::MIN as i64 && delta_ticks <= i8::MAX as i64 {
                // small delta (8bit) — we store as i16 but mark via status
                // For simplicity in this implementation, we store all as 16bit deltas
                deltas.push(delta_ticks as i16);
            } else {
                deltas.push(delta_ticks as i16);
            }
            prev_time = t;
            first = false;
        }

        let fb_count = self.last_feedback_seq;
        self.last_feedback_seq = self.last_feedback_seq.wrapping_add(1);

        let feedback = TwccFeedback {
            sender_ssrc: 0,
            media_ssrc: 0,
            base_seq,
            packet_count,
            reference_time,
            fb_count,
            status_chunks,
            deltas,
        };

        // 清空已反馈的包
        self.packets.clear();

        feedback.encode()
    }
}

impl Default for TwccReceiver {
    fn default() -> Self {
        Self::new()
    }
}

// ============================================================================
// TWCC 反馈包 (RTCP RTPFB, FMT=15)
// ============================================================================

/// TWCC 反馈包 (RTCP RTPFB, FMT=15)
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TwccFeedback {
    pub sender_ssrc: u32,
    pub media_ssrc: u32,
    pub base_seq: u16,
    pub packet_count: u16,
    /// 24bit, 64ms 为单位
    pub reference_time: u32,
    pub fb_count: u8,
    pub status_chunks: Vec<u8>,
    pub deltas: Vec<i16>,
}

impl TwccFeedback {
    pub fn parse(data: &[u8]) -> Result<Self, RtcpError> {
        // 最小: 4 (header) + 8 (ssrcs) + 8 (base_seq, packet_count, reference_time, fb_count) = 20
        if data.len() < 20 {
            return Err(RtcpError::TooShort {
                needed: 20,
                got: data.len(),
            });
        }

        let fmt = data[0] & 0x1F;
        let pt = data[1];
        if pt != PT_RTPFB {
            return Err(RtcpError::UnsupportedPacketType(pt));
        }
        if fmt != FMT_TWCC {
            return Err(RtcpError::UnsupportedFmt(fmt));
        }

        let length = u16::from_be_bytes([data[2], data[3]]) as usize;
        let declared_bytes = (length + 1) * 4;
        if data.len() < declared_bytes {
            return Err(RtcpError::LengthMismatch {
                declared: declared_bytes,
                actual: data.len(),
            });
        }

        let sender_ssrc = u32::from_be_bytes([data[4], data[5], data[6], data[7]]);
        let media_ssrc = u32::from_be_bytes([data[8], data[9], data[10], data[11]]);
        let base_seq = u16::from_be_bytes([data[12], data[13]]);
        let packet_count = u16::from_be_bytes([data[14], data[15]]);
        // reference_time: 24bit
        let reference_time = u32::from_be_bytes([0, data[16], data[17], data[18]]) & 0x00FF_FFFF;
        let fb_count = data[19];

        // status chunks + deltas 从 offset 20 开始
        let mut offset = 20;
        let mut status_chunks: Vec<u8> = Vec::new();

        // 解析 status chunks 直到用完或遇到 deltas
        // 每个 status chunk 是 2 字节 (16bit)
        // 我们需要根据 packet_count 来确定有多少 delta
        // 简化: 解析所有剩余数据, status chunks 在前, deltas 在后
        // 由于 status chunk 和 delta 混在一起难以分离, 我们采用一种简化策略:
        // status chunks 数量 = (declared_bytes - 20 - delta_bytes) / 2
        // 但我们不知道 delta_bytes, 所以这里采用: 解析 run-length chunks 直到覆盖 packet_count 个包

        let mut covered_packets: u32 = 0;
        while offset + 2 <= declared_bytes && offset + 2 <= data.len() {
            let chunk = u16::from_be_bytes([data[offset], data[offset + 1]]);
            let symbol = (chunk >> 13) & 0x3;
            let run_len = (chunk & 0x1FFF) as u32;
            status_chunks.push(data[offset]);
            status_chunks.push(data[offset + 1]);
            offset += 2;

            if symbol == 0b00 {
                // not received
                covered_packets += run_len;
            } else if symbol == 0b01 {
                // small delta (8bit each)
                covered_packets += run_len;
                // 后面跟 run_len 个 8bit delta
                // 我们跳过 8bit delta, 但这里简化为读取
                // 实际上 8bit delta 紧跟在 status chunk 之后
                // 但我们的编码器使用 16bit delta, 所以这里简化处理
                break;
            } else if symbol == 0b10 {
                // large delta (16bit each)
                covered_packets += run_len;
                break;
            } else {
                // 11 = reserved
                break;
            }

            if covered_packets >= packet_count as u32 {
                break;
            }
        }

        // 剩余作为 deltas (16bit)
        let mut deltas: Vec<i16> = Vec::new();
        while offset + 2 <= declared_bytes && offset + 2 <= data.len() {
            let d = i16::from_be_bytes([data[offset], data[offset + 1]]);
            deltas.push(d);
            offset += 2;
        }

        Ok(Self {
            sender_ssrc,
            media_ssrc,
            base_seq,
            packet_count,
            reference_time,
            fb_count,
            status_chunks,
            deltas,
        })
    }

    pub fn encode(&self) -> Vec<u8> {
        let mut buf =
            BytesMut::with_capacity(20 + self.status_chunks.len() + self.deltas.len() * 2);

        // header: V=2, FMT=15
        buf.put_u8((VERSION << 6) | FMT_TWCC);
        buf.put_u8(PT_RTPFB);
        buf.put_u16(0); // length 占位

        buf.put_u32(self.sender_ssrc);
        buf.put_u32(self.media_ssrc);

        buf.put_u16(self.base_seq);
        buf.put_u16(self.packet_count);
        // reference_time: 24bit
        buf.put_u8((self.reference_time >> 16) as u8 & 0xFF);
        buf.put_u8((self.reference_time >> 8) as u8 & 0xFF);
        buf.put_u8(self.reference_time as u8 & 0xFF);
        buf.put_u8(self.fb_count);

        // status chunks
        buf.put_slice(&self.status_chunks);

        // deltas (16bit each)
        for d in &self.deltas {
            buf.put_i16(*d);
        }

        // padding 到 4 字节对齐
        while buf.len() % 4 != 0 {
            buf.put_u8(0);
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
// 带宽估计器 (基于 TWCC 反馈)
// ============================================================================

/// 带宽估计器 (基于 TWCC 反馈)
pub struct BandwidthEstimator {
    last_bitrate_kbps: u64,
    min_bitrate_kbps: u64,
    max_bitrate_kbps: u64,
}

impl BandwidthEstimator {
    pub fn new(initial_kbps: u64) -> Self {
        Self {
            last_bitrate_kbps: initial_kbps,
            min_bitrate_kbps: 30,
            max_bitrate_kbps: 5_000,
        }
    }

    /// 根据 TWCC 反馈更新带宽估计
    pub fn update(&mut self, feedback: &TwccFeedback) -> u64 {
        // 计算接收率: packet_count 个包, 每个包假设 ~1200 字节 (典型 MTU)
        // 实际应根据 delta 计算到达时间窗口
        let received = feedback.packet_count as u64;

        // 计算反馈覆盖的时间窗口 (从 deltas)
        // delta 单位是 250us
        let total_delta_us: i64 = feedback
            .deltas
            .iter()
            .map(|&d| d as i64 * DELTA_TICK_US)
            .sum();
        let time_window_ms = (total_delta_us / 1000).max(1) as u64;

        // 估算接收比特率
        let bytes_received = received * 1200;
        let bitrate_kbps = if time_window_ms > 0 {
            (bytes_received * 8) / time_window_ms
        } else {
            self.last_bitrate_kbps
        };

        // 简单 AIMD 策略
        // 如果接收率接近当前估计, 增加一点; 否则降低
        if bitrate_kbps >= self.last_bitrate_kbps * 9 / 10 {
            // 增长: additive
            self.last_bitrate_kbps = self.last_bitrate_kbps * 110 / 100;
        } else {
            // 降低: multiplicative
            self.last_bitrate_kbps = self.last_bitrate_kbps * 8 / 10;
        }

        // 限制范围
        if self.last_bitrate_kbps < self.min_bitrate_kbps {
            self.last_bitrate_kbps = self.min_bitrate_kbps;
        }
        if self.last_bitrate_kbps > self.max_bitrate_kbps {
            self.last_bitrate_kbps = self.max_bitrate_kbps;
        }

        self.last_bitrate_kbps
    }

    pub fn current_bitrate_kbps(&self) -> u64 {
        self.last_bitrate_kbps
    }
}

// ============================================================================
// 测试
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_twcc_seq_allocator() {
        let mut alloc = TwccSeqAllocator::new();
        assert_eq!(alloc.next(), 0);
        assert_eq!(alloc.next(), 1);
        assert_eq!(alloc.next(), 2);
        assert_eq!(alloc.next(), 3);
    }

    #[test]
    fn test_twcc_receiver_record() {
        let mut rx = TwccReceiver::new();
        rx.record_packet(100, 1_000_000);
        rx.record_packet(101, 1_020_000);
        rx.record_packet(102, 1_040_000);

        let feedback_bytes = rx.build_feedback(1_000_000);
        // 应该能解析回来
        let parsed = TwccFeedback::parse(&feedback_bytes).unwrap();
        assert_eq!(parsed.base_seq, 100);
        assert_eq!(parsed.packet_count, 3);
    }

    #[test]
    fn test_twcc_feedback_encode_decode() {
        let feedback = TwccFeedback {
            sender_ssrc: 0x12345678,
            media_ssrc: 0xABCDEF01,
            base_seq: 100,
            packet_count: 5,
            reference_time: 0x123456,
            fb_count: 1,
            status_chunks: vec![0x20, 0x05], // run-length: symbol=01, run=5
            deltas: vec![100, 200, 300, 400, 500],
        };

        let encoded = feedback.encode();
        let parsed = TwccFeedback::parse(&encoded).unwrap();

        assert_eq!(parsed.sender_ssrc, feedback.sender_ssrc);
        assert_eq!(parsed.media_ssrc, feedback.media_ssrc);
        assert_eq!(parsed.base_seq, feedback.base_seq);
        assert_eq!(parsed.packet_count, feedback.packet_count);
        assert_eq!(parsed.reference_time, feedback.reference_time);
        assert_eq!(parsed.fb_count, feedback.fb_count);
    }

    #[test]
    fn test_twcc_feedback_with_losses() {
        let mut rx = TwccReceiver::new();
        // 包 100, 102 收到, 101 丢失
        rx.record_packet(100, 1_000_000);
        rx.record_packet(102, 1_040_000);
        rx.record_packet(103, 1_060_000);

        let feedback_bytes = rx.build_feedback(1_000_000);
        let parsed = TwccFeedback::parse(&feedback_bytes).unwrap();

        assert_eq!(parsed.base_seq, 100);
        assert_eq!(parsed.packet_count, 3); // 3 个包收到
    }

    #[test]
    fn test_bandwidth_estimator_initial() {
        let est = BandwidthEstimator::new(1000);
        assert_eq!(est.current_bitrate_kbps(), 1000);
    }

    #[test]
    fn test_bandwidth_estimator_increase() {
        let mut est = BandwidthEstimator::new(1000);

        // 构造一个高接收率的反馈
        let feedback = TwccFeedback {
            sender_ssrc: 0,
            media_ssrc: 0,
            base_seq: 0,
            packet_count: 100,
            reference_time: 0,
            fb_count: 0,
            status_chunks: vec![],
            deltas: vec![40; 100], // 每个 40 * 250us = 10ms, 总 1000ms
        };

        let new_bitrate = est.update(&feedback);
        // 接收率 = 100 * 1200 * 8 / 1000 = 960 kbps
        // 960 >= 1000 * 9/10 = 900, 所以增加
        assert!(new_bitrate > 1000);
    }
}
