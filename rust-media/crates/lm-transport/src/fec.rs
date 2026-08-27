//! FEC 前向纠错 (XOR-based, RFC 5109 / ULP FEC)
//!
//! XOR FEC 原理:
//! - 对一组媒体包的 payload 做 XOR，生成 FEC 包
//! - 如果组内丢失 1 个包，可以用 FEC 包 + 其他包 XOR 恢复
//! - FEC 包包含: sn_base (组内第一个包的 seq), mask (标记哪些包参与 XOR), XOR 后的 payload

use std::collections::HashMap;

// ============================================================================
// FEC 配置
// ============================================================================

/// FEC 保护级别
#[derive(Debug, Clone)]
pub struct FecConfig {
    /// 行 FEC: 每 L 个媒体包生成 1 个 FEC 包
    pub l: usize,
    /// 列 FEC: 每 D 个媒体包生成 1 个 FEC 包 (可选)
    pub d: usize,
    /// 1D 行保护
    pub enable_1d: bool,
    /// 2D 行列保护
    pub enable_2d: bool,
}

impl Default for FecConfig {
    fn default() -> Self {
        Self {
            l: 5,
            d: 5,
            enable_1d: true,
            enable_2d: false,
        }
    }
}

// ============================================================================
// FEC 包
// ============================================================================

/// FEC 包
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FecPacket {
    /// 基础序号 (组内第一个包的 seq)
    pub sn_base: u16,
    /// 时间戳恢复值
    pub ts_recovery: u32,
    /// 长度恢复值
    pub length_recovery: u16,
    /// XOR 后的 payload
    pub payload: Vec<u8>,
    /// 保护哪些包的 bitmask (每个 bit 对应 sn_base + offset)
    pub mask: Vec<u8>,
}

// ============================================================================
// FEC 编码器 (发送方)
// ============================================================================

/// FEC 编码器 (发送方)
pub struct FecEncoder {
    config: FecConfig,
    /// 当前组的媒体包: (seq, payload)
    current_group: Vec<(u16, Vec<u8>)>,
}

impl FecEncoder {
    pub fn new(config: FecConfig) -> Self {
        Self {
            current_group: Vec::with_capacity(config.l),
            config,
        }
    }

    /// 添加一个媒体包, 如果组满则生成 FEC 包
    pub fn add_packet(&mut self, seq: u16, payload: &[u8]) -> Option<FecPacket> {
        self.current_group.push((seq, payload.to_vec()));

        if self.config.enable_1d && self.current_group.len() >= self.config.l {
            Some(self.build_fec_packet())
        } else {
            None
        }
    }

    /// 刷新: 即使组未满也生成 FEC 包
    pub fn flush(&mut self) -> Option<FecPacket> {
        if self.current_group.is_empty() {
            return None;
        }
        if self.config.enable_1d {
            Some(self.build_fec_packet())
        } else {
            None
        }
    }

    fn build_fec_packet(&mut self) -> FecPacket {
        let group = std::mem::take(&mut self.current_group);
        let sn_base = group.first().map(|(s, _)| *s).unwrap_or(0);

        // 计算 mask: 每个 bit 对应 sn_base + offset
        let max_offset = group.len();
        let mask_bits = (max_offset + 7) / 8;
        let mut mask = vec![0u8; mask_bits];
        for (i, _) in group.iter().enumerate() {
            mask[i / 8] |= 1 << (i % 8);
        }

        // XOR 所有 payload
        let max_len = group.iter().map(|(_, p)| p.len()).max().unwrap_or(0);
        let mut fec_payload = vec![0u8; max_len];
        let mut length_recovery: u16 = 0;
        let mut ts_recovery: u32 = 0;

        for (_, payload) in &group {
            length_recovery ^= payload.len() as u16;
            // ts_recovery: 这里我们没有 ts, 用 0 占位 (实际应 XOR 各包 timestamp)
            for (i, b) in payload.iter().enumerate() {
                fec_payload[i] ^= b;
            }
        }

        FecPacket {
            sn_base,
            ts_recovery,
            length_recovery,
            payload: fec_payload,
            mask,
        }
    }
}

// ============================================================================
// FEC 解码器 (接收方)
// ============================================================================

struct FecGroup {
    /// media_packets[idx] = Some((seq, payload)) 或 None (丢失)
    media_packets: Vec<Option<(u16, Vec<u8>)>>,
    fec_packets: Vec<FecPacket>,
}

/// FEC 解码器 (接收方)
pub struct FecDecoder {
    /// 按 sn_base 存储组
    groups: HashMap<u16, FecGroup>,
    /// 尚未归组的媒体包 (FEC 包到达前收到的)
    pending_media: Vec<(u16, Vec<u8>)>,
}

impl FecDecoder {
    pub fn new() -> Self {
        Self {
            groups: HashMap::new(),
            pending_media: Vec::new(),
        }
    }

    /// 添加媒体包, 如果能恢复出丢失的包则返回恢复的 payload
    pub fn add_media_packet(&mut self, seq: u16, payload: Vec<u8>) -> Option<Vec<u8>> {
        // 尝试将包放入匹配的组
        let mut recovered = None;
        let mut placed = false;

        for (sn_base, group) in &mut self.groups {
            let offset = seq.wrapping_sub(*sn_base);
            if offset < 256 {
                let idx = offset as usize;
                if idx < group.media_packets.len() {
                    if group.media_packets[idx].is_none() {
                        group.media_packets[idx] = Some((seq, payload.clone()));
                        placed = true;
                        // 尝试恢复
                        if let Some(rec) = try_recover(group) {
                            recovered = Some(rec);
                        }
                    }
                }
            }
        }

        // 如果没有匹配的组, 存入 pending
        if !placed {
            self.pending_media.push((seq, payload));
        }

        recovered
    }

    /// 添加 FEC 包, 如果能恢复出丢失的包则返回 (seq, payload)
    pub fn add_fec_packet(&mut self, fec: FecPacket) -> Option<(u16, Vec<u8>)> {
        let sn_base = fec.sn_base;
        let needed = count_mask_bits(&fec.mask);

        let group = self.groups.entry(sn_base).or_insert_with(|| FecGroup {
            media_packets: vec![None; needed],
            fec_packets: Vec::new(),
        });

        // 确保 media_packets 足够大
        while group.media_packets.len() < needed {
            group.media_packets.push(None);
        }

        // 回填 pending 中匹配的媒体包
        let mut still_pending = Vec::new();
        for (seq, payload) in std::mem::take(&mut self.pending_media) {
            let offset = seq.wrapping_sub(sn_base);
            if offset < 256 {
                let idx = offset as usize;
                if idx < group.media_packets.len() && group.media_packets[idx].is_none() {
                    group.media_packets[idx] = Some((seq, payload));
                } else {
                    still_pending.push((seq, payload));
                }
            } else {
                still_pending.push((seq, payload));
            }
        }
        self.pending_media = still_pending;

        group.fec_packets.push(fec);

        try_recover_with_seq(group)
    }
}

impl Default for FecDecoder {
    fn default() -> Self {
        Self::new()
    }
}

/// 计算 mask 中设置的 bit 数 (即组大小)
fn count_mask_bits(mask: &[u8]) -> usize {
    let mut count = 0;
    for &b in mask {
        count += b.count_ones() as usize;
    }
    count
}

/// 尝试从组中恢复一个丢失的包 (返回 payload)
fn try_recover(group: &FecGroup) -> Option<Vec<u8>> {
    try_recover_internal(group)
}

/// 尝试恢复, 返回 (seq, payload)
fn try_recover_with_seq(group: &FecGroup) -> Option<(u16, Vec<u8>)> {
    let payload = try_recover_internal(group)?;
    // 找到丢失的包的 seq
    let sn_base = group.fec_packets.first().map(|f| f.sn_base).unwrap_or(0);
    for (idx, slot) in group.media_packets.iter().enumerate() {
        if slot.is_none() {
            let seq = sn_base.wrapping_add(idx as u16);
            return Some((seq, payload));
        }
    }
    None
}

/// 内部恢复逻辑: 如果组内恰好丢失 1 个包且有 FEC 包, 则恢复
fn try_recover_internal(group: &FecGroup) -> Option<Vec<u8>> {
    // 统计丢失的包
    let missing_indices: Vec<usize> = group
        .media_packets
        .iter()
        .enumerate()
        .filter(|(_, s)| s.is_none())
        .map(|(i, _)| i)
        .collect();

    if missing_indices.len() != 1 {
        return None;
    }

    let missing_idx = missing_indices[0];
    if group.fec_packets.is_empty() {
        return None;
    }

    let fec = &group.fec_packets[0];

    // XOR 所有已收到的媒体包的 payload + FEC payload = 丢失包的 payload
    let max_len = fec.payload.len();
    let mut recovered = fec.payload.clone();

    for slot in &group.media_packets {
        if let Some((_, payload)) = slot {
            for (i, b) in payload.iter().enumerate() {
                if i < recovered.len() {
                    recovered[i] ^= b;
                }
            }
            // 如果 payload 比 recovered 短, 需要处理长度
            // 如果 payload 比 recovered 长, 也需要处理
        }
    }

    // 修剪到正确长度
    // length_recovery XOR 所有已收到包的长度 = 丢失包的长度
    let mut expected_len = fec.length_recovery;
    for slot in &group.media_packets {
        if let Some((_, payload)) = slot {
            expected_len ^= payload.len() as u16;
        }
    }

    if recovered.len() >= expected_len as usize {
        recovered.truncate(expected_len as usize);
    } else {
        recovered.resize(expected_len as usize, 0);
    }

    // 标记已恢复
    let sn_base = fec.sn_base;
    let seq = sn_base.wrapping_add(missing_idx as u16);
    // 注意: 这里不能修改 group (借用), 调用方处理

    // 实际上我们需要返回 payload, 调用方会处理
    // 但这里我们简化: 直接返回 recovered payload
    // 为了让恢复的包能被记录, 我们在调用方处理
    let _ = seq;
    Some(recovered)
}

// ============================================================================
// XOR 辅助函数
// ============================================================================

/// 对两个 byte slice 做 XOR
pub fn xor_bytes(a: &[u8], b: &[u8]) -> Vec<u8> {
    let max_len = a.len().max(b.len());
    let mut result = vec![0u8; max_len];
    for i in 0..max_len {
        let av = if i < a.len() { a[i] } else { 0 };
        let bv = if i < b.len() { b[i] } else { 0 };
        result[i] = av ^ bv;
    }
    result
}

// ============================================================================
// 测试
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_fec_xor_basic() {
        let a = [0x01, 0x02, 0x03, 0xFF];
        let b = [0x10, 0x20, 0x30, 0x0F];
        let result = xor_bytes(&a, &b);
        assert_eq!(result, vec![0x11, 0x22, 0x33, 0xF0]);

        // XOR 回来应该得到原值
        let recovered = xor_bytes(&result, &b);
        assert_eq!(recovered, a.to_vec());
    }

    #[test]
    fn test_fec_encoder_1d() {
        let config = FecConfig {
            l: 3,
            d: 0,
            enable_1d: true,
            enable_2d: false,
        };
        let mut encoder = FecEncoder::new(config);

        // 添加 3 个包应触发 FEC 生成
        assert!(encoder.add_packet(100, b"hello").is_none());
        assert!(encoder.add_packet(101, b"world").is_none());
        let fec = encoder.add_packet(102, b"test!");
        assert!(fec.is_some());
        let fec = fec.unwrap();
        assert_eq!(fec.sn_base, 100);
        assert_eq!(fec.payload.len(), 5); // 最长 payload 长度
    }

    #[test]
    fn test_fec_decoder_recover_single_loss() {
        let config = FecConfig {
            l: 3,
            d: 0,
            enable_1d: true,
            enable_2d: false,
        };
        let mut encoder = FecEncoder::new(config);
        let mut decoder = FecDecoder::new();

        let p0 = b"hello".to_vec();
        let p1 = b"world".to_vec();
        let p2 = b"test!".to_vec();

        encoder.add_packet(100, &p0);
        encoder.add_packet(101, &p1);
        let fec = encoder.add_packet(102, &p2).unwrap();

        // 接收方: 收到 100, 102, FEC, 丢失 101
        decoder.add_media_packet(100, p0.clone());
        decoder.add_media_packet(102, p2.clone());
        let recovered = decoder.add_fec_packet(fec);
        assert!(recovered.is_some());
        let (seq, payload) = recovered.unwrap();
        assert_eq!(seq, 101);
        assert_eq!(payload, p1);
    }

    #[test]
    fn test_fec_decoder_no_recovery_when_multiple_loss() {
        let config = FecConfig {
            l: 3,
            d: 0,
            enable_1d: true,
            enable_2d: false,
        };
        let mut encoder = FecEncoder::new(config);
        let mut decoder = FecDecoder::new();

        let p0 = b"hello".to_vec();
        let p1 = b"world".to_vec();
        let p2 = b"test!".to_vec();

        encoder.add_packet(100, &p0);
        encoder.add_packet(101, &p1);
        let fec = encoder.add_packet(102, &p2).unwrap();

        // 接收方: 只收到 100, 丢失 101 和 102, 有 FEC
        decoder.add_media_packet(100, p0.clone());
        let recovered = decoder.add_fec_packet(fec);
        // 丢失 2 个包, 无法恢复
        assert!(recovered.is_none());
    }

    #[test]
    fn test_fec_group_flush() {
        let config = FecConfig {
            l: 5,
            d: 0,
            enable_1d: true,
            enable_2d: false,
        };
        let mut encoder = FecEncoder::new(config);

        // 只添加 2 个包, 未满
        assert!(encoder.add_packet(100, b"a").is_none());
        assert!(encoder.add_packet(101, b"b").is_none());

        // flush 应该生成 FEC
        let fec = encoder.flush();
        assert!(fec.is_some());
        let fec = fec.unwrap();
        assert_eq!(fec.sn_base, 100);

        // 再次 flush 应该返回 None (组已清空)
        assert!(encoder.flush().is_none());
    }
}
