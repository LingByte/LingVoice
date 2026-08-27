//! NACK 重传缓冲
//!
//! - 发送方维护最近发送的 RTP 包，收到 NACK 时重发
//! - 接收方检测丢包后生成 NACK RTCP 包

use bytes::Bytes;
use std::collections::{HashSet, VecDeque};

use crate::rtcp::NackPacket;

// ============================================================================
// 发送方重传缓冲区
// ============================================================================

/// 发送方重传缓冲区
/// 维护最近发送的 RTP 包，收到 NACK 时重发
pub struct RetransmitBuffer {
    /// seq -> (packet_bytes, timestamp)
    buffer: VecDeque<(u16, Bytes, u64)>,
    max_size: usize,
}

impl RetransmitBuffer {
    pub fn new(max_size: usize) -> Self {
        Self {
            buffer: VecDeque::with_capacity(max_size),
            max_size,
        }
    }

    pub fn insert(&mut self, seq: u16, packet: Bytes, timestamp_ms: u64) {
        // 如果超过 max_size, 移除最旧的
        while self.buffer.len() >= self.max_size {
            self.buffer.pop_front();
        }
        self.buffer.push_back((seq, packet, timestamp_ms));
    }

    pub fn get(&self, seq: u16) -> Option<&Bytes> {
        for (s, p, _) in &self.buffer {
            if *s == seq {
                return Some(p);
            }
        }
        None
    }

    /// 处理 NACK, 返回需要重传的包列表
    pub fn handle_nack(&self, nack: &NackPacket) -> Vec<Bytes> {
        let mut result = Vec::new();
        let mut lost_seqs: Vec<u16> = Vec::new();
        for item in &nack.items {
            lost_seqs.extend(item.lost_sequence_numbers());
        }
        for seq in lost_seqs {
            if let Some(pkt) = self.get(seq) {
                result.push(pkt.clone());
            }
        }
        result
    }

    /// 清理过期的包
    pub fn cleanup(&mut self, now_ms: u64, max_age_ms: u64) {
        while let Some(front) = self.buffer.front() {
            if now_ms.saturating_sub(front.2) > max_age_ms {
                self.buffer.pop_front();
            } else {
                break;
            }
        }
    }

    pub fn len(&self) -> usize {
        self.buffer.len()
    }

    pub fn is_empty(&self) -> bool {
        self.buffer.is_empty()
    }
}

// ============================================================================
// 接收方 NACK 生成器
// ============================================================================

/// 接收方 NACK 生成器
/// 检测丢包后生成 NACK RTCP 包
pub struct NackGenerator {
    highest_seq: u16,
    initialized: bool,
    missing_seqs: HashSet<u16>,
}

impl NackGenerator {
    pub fn new() -> Self {
        Self {
            highest_seq: 0,
            initialized: false,
            missing_seqs: HashSet::new(),
        }
    }

    /// 记录收到的包, 如果检测到丢包返回 NACK 包
    pub fn record_packet(&mut self, seq: u16) -> Option<NackPacket> {
        if !self.initialized {
            self.highest_seq = seq;
            self.initialized = true;
            return None;
        }

        // 如果是之前丢失的包现在收到了, 从 missing 中移除
        if self.missing_seqs.remove(&seq) {
            return None;
        }

        // 比较 seq 和 highest_seq (考虑 u16 回绕)
        let diff = seq.wrapping_sub(self.highest_seq);
        if diff == 0 {
            // 重复包
            return None;
        }

        if diff > 0 && diff < 32768 {
            // seq 在 highest_seq 之后, 检查中间是否有丢失
            let mut new_missing: Vec<u16> = Vec::new();
            let mut s = self.highest_seq.wrapping_add(1);
            while s != seq {
                if !self.missing_seqs.contains(&s) {
                    new_missing.push(s);
                }
                s = s.wrapping_add(1);
            }

            for m in &new_missing {
                self.missing_seqs.insert(*m);
            }

            self.highest_seq = seq;

            if new_missing.is_empty() {
                return None;
            }

            // 构建 NACK 包
            return Some(self.build_nack(&new_missing));
        }

        // seq 在 highest_seq 之前 (乱序), 已经处理过 missing remove
        None
    }

    /// 是否有 pending 的 NACK
    pub fn pending_nacks(&self) -> bool {
        !self.missing_seqs.is_empty()
    }

    fn build_nack(&self, missing: &[u16]) -> NackPacket {
        // 将丢失的 seq 列表打包为 NackItem (pid + blp)
        let mut sorted: Vec<u16> = missing.to_vec();
        sorted.sort();

        let mut items = Vec::new();
        let mut i = 0;
        while i < sorted.len() {
            let pid = sorted[i];
            let mut blp: u16 = 0;
            let mut j = i + 1;
            while j < sorted.len() {
                let offset = sorted[j].wrapping_sub(pid);
                if offset == 0 || offset > 16 {
                    break;
                }
                blp |= 1 << (offset - 1);
                j += 1;
            }
            items.push(crate::rtcp::NackItem { pid, blp });
            i = j;
        }

        NackPacket {
            sender_ssrc: 0,
            media_ssrc: 0,
            items,
        }
    }
}

impl Default for NackGenerator {
    fn default() -> Self {
        Self::new()
    }
}

// ============================================================================
// 测试
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;
    use crate::rtcp::NackItem;

    #[test]
    fn test_retransmit_buffer_insert_get() {
        let mut buf = RetransmitBuffer::new(100);
        let pkt = Bytes::from_static(b"rtp_packet_data");
        buf.insert(42, pkt.clone(), 1000);

        assert_eq!(buf.len(), 1);
        assert_eq!(buf.get(42), Some(&pkt));
        assert_eq!(buf.get(43), None);
    }

    #[test]
    fn test_retransmit_buffer_handle_nack() {
        let mut buf = RetransmitBuffer::new(100);
        let pkt1 = Bytes::from_static(b"packet1");
        let pkt2 = Bytes::from_static(b"packet2");
        buf.insert(100, pkt1.clone(), 1000);
        buf.insert(101, pkt2.clone(), 1000);

        let nack = NackPacket {
            sender_ssrc: 0,
            media_ssrc: 0,
            items: vec![NackItem {
                pid: 100,
                blp: 0b10,
            }], // 100 和 102
        };

        let retrans = buf.handle_nack(&nack);
        assert_eq!(retrans.len(), 1); // 只有 100 在缓冲区
        assert_eq!(retrans[0], pkt1);
    }

    #[test]
    fn test_retransmit_buffer_cleanup() {
        let mut buf = RetransmitBuffer::new(100);
        buf.insert(1, Bytes::from_static(b"old"), 1000);
        buf.insert(2, Bytes::from_static(b"new"), 2000);

        // max_age = 500ms, now = 2000 -> old(1000) 过期
        buf.cleanup(2000, 500);
        assert_eq!(buf.len(), 1);
        assert_eq!(buf.get(1), None);
        assert!(buf.get(2).is_some());
    }

    #[test]
    fn test_retransmit_buffer_overflow() {
        let mut buf = RetransmitBuffer::new(3);
        buf.insert(1, Bytes::from_static(b"a"), 0);
        buf.insert(2, Bytes::from_static(b"b"), 0);
        buf.insert(3, Bytes::from_static(b"c"), 0);
        buf.insert(4, Bytes::from_static(b"d"), 0);

        // 应该只有 3 个, 最旧的被移除
        assert_eq!(buf.len(), 3);
        assert_eq!(buf.get(1), None);
        assert!(buf.get(2).is_some());
        assert!(buf.get(3).is_some());
        assert!(buf.get(4).is_some());
    }

    #[test]
    fn test_nack_generator_no_loss() {
        let mut gen = NackGenerator::new();
        assert!(gen.record_packet(100).is_none());
        assert!(gen.record_packet(101).is_none());
        assert!(gen.record_packet(102).is_none());
        assert!(!gen.pending_nacks());
    }

    #[test]
    fn test_nack_generator_single_loss() {
        let mut gen = NackGenerator::new();
        assert!(gen.record_packet(100).is_none());
        // 101 丢失, 直接收到 102
        let nack = gen.record_packet(102);
        assert!(nack.is_some());
        let nack = nack.unwrap();
        assert_eq!(nack.items.len(), 1);
        assert_eq!(nack.items[0].pid, 101);
        assert_eq!(nack.items[0].blp, 0);
    }

    #[test]
    fn test_nack_generator_multiple_loss() {
        let mut gen = NackGenerator::new();
        assert!(gen.record_packet(100).is_none());
        // 101, 102, 103 丢失, 直接收到 104
        let nack = gen.record_packet(104);
        assert!(nack.is_some());
        let nack = nack.unwrap();
        let lost: Vec<u16> = nack
            .items
            .iter()
            .flat_map(|i| i.lost_sequence_numbers())
            .collect();
        assert!(lost.contains(&101));
        assert!(lost.contains(&102));
        assert!(lost.contains(&103));
    }

    #[test]
    fn test_nack_generator_blp_bitmap() {
        let mut gen = NackGenerator::new();
        assert!(gen.record_packet(100).is_none());
        // 101 丢失, 102 收到, 103 丢失, 直接收到 104
        assert!(gen.record_packet(102).is_some()); // 101 丢失
                                                   // 103 丢失, 收到 104
        let nack = gen.record_packet(104);
        assert!(nack.is_some());
        let nack = nack.unwrap();
        let lost: Vec<u16> = nack
            .items
            .iter()
            .flat_map(|i| i.lost_sequence_numbers())
            .collect();
        assert!(lost.contains(&103));
    }
}
