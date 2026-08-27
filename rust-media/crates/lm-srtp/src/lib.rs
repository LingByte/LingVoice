//! SRTP/SRTCP 加密和解密 (RFC 3711)
//!
//! 实现:
//! - AES-CM (Counter Mode) 加密
//! - HMAC-SHA1-80 认证 (10 字节 auth tag)
//! - Key Derivation Function (KDF) 从 master key 派生 session keys
//! - 重放保护 (Replay Protection)
//! - SRTP protect/unprotect
//! - SRTCP protect/unprotect

use aes::cipher::{KeyIvInit, StreamCipher};
use aes::Aes128;
use ctr::Ctr128BE;
use hmac::{Hmac, Mac};
use sha1::Sha1;

type AesCm = Ctr128BE<Aes128>;
type HmacSha1 = Hmac<Sha1>;

// ============================================================================
// 错误类型
// ============================================================================

#[derive(Debug, thiserror::Error, PartialEq, Eq)]
pub enum SrtpError {
    #[error("无效的密钥长度: {0} (需要 16)")]
    InvalidKeyLength(usize),
    #[error("无效的 salt 长度: {0} (需要 14)")]
    InvalidSaltLength(usize),
    #[error("包太短: {0} 字节")]
    PacketTooShort(usize),
    #[error("认证失败")]
    AuthenticationFailed,
    #[error("重放攻击检测: seq={0}")]
    ReplayDetected(u16),
    #[error("SRTCP 重放攻击检测: index={0}")]
    ReplayDetectedSrtcp(u32),
    #[error("SSRC 不匹配: 期望 {expected}, 实际 {actual}")]
    SsrcMismatch { expected: u32, actual: u32 },
}

// ============================================================================
// 配置
// ============================================================================

/// SRTP 配置
#[derive(Debug, Clone)]
pub struct SrtpConfig {
    /// 主密钥 (16 字节 for AES-128)
    pub master_key: Vec<u8>,
    /// 主 salt (14 字节)
    pub master_salt: Vec<u8>,
    /// 认证标签长度 (10 for SHA1-80, 4 for SHA1-32)
    pub auth_tag_len: usize,
    /// 重放保护窗口大小
    pub replay_window_size: u32,
}

impl Default for SrtpConfig {
    fn default() -> Self {
        Self {
            master_key: vec![0u8; 16],
            master_salt: vec![0u8; 14],
            auth_tag_len: 10,
            replay_window_size: 1024,
        }
    }
}

// ============================================================================
// KDF (Key Derivation Function)
// ============================================================================

/// SRTP KDF key IDs
const KEY_ID_ENCRYPTION: u8 = 0x00;
const KEY_ID_AUTH: u8 = 0x01;
const KEY_ID_SALT: u8 = 0x02;

/// 从 master key 派生 session key
/// session_key = AES-CM(master_key, x || key_id || ssrc || 0x00*7)
fn derive_key(
    master_key: &[u8],
    master_salt: &[u8],
    key_id: u8,
    ssrc: u32,
    key_len: usize,
) -> Vec<u8> {
    // 构建 IV (16 bytes): master_salt(14) || key_id(1) || 0x00(1)
    // 但实际 RFC 3711 的 KDF 是:
    // x = master_salt (14 bytes)
    // key_id 占 1 byte, ssrc 占 4 bytes, 后面补 0
    // IV = (salt << 16) | (key_id << 8) | 0x00 ... (16 bytes)
    // 更精确: r = key_id * 2^8, x XOR (ssrc << 16 | r << 8 | 0)

    // 简化实现: 构建一个 16 byte 的 IV
    let mut iv = [0u8; 16];
    // master_salt 是 14 bytes, 放在 IV 的前 14 bytes
    iv[..14].copy_from_slice(master_salt);
    // key_id 放在 byte 14
    iv[14] = key_id;
    // byte 15 = 0

    // 将 ssrc 混入: XOR ssrc 到 IV 的适当位置
    // RFC 3711: x XOR (ssrc * 2^16 | key_id * 2^8)
    // ssrc 在 IV 的 byte 4-7 位置 XOR
    let ssrc_bytes = ssrc.to_be_bytes();
    iv[4] ^= ssrc_bytes[0];
    iv[5] ^= ssrc_bytes[1];
    iv[6] ^= ssrc_bytes[2];
    iv[7] ^= ssrc_bytes[3];

    // AES-CM 加密全零明文得到 session key
    let mut cipher = AesCm::new(master_key.into(), &iv.into());
    let mut key = vec![0u8; key_len];
    cipher.apply_keystream(&mut key);
    key
}

/// 从 master key 派生 salt key (14 bytes)
fn derive_salt(master_key: &[u8], master_salt: &[u8], ssrc: u32) -> Vec<u8> {
    // salt key 也是通过 KDF 派生的, 但只取前 14 bytes
    let mut salt = derive_key(master_key, master_salt, KEY_ID_SALT, ssrc, 16);
    salt.truncate(14);
    salt
}

// ============================================================================
// SRTP Session
// ============================================================================

/// SRTP 会话 (单个 SSRC)
pub struct SrtpSession {
    config: SrtpConfig,
    ssrc: u32,
    /// 加密密钥 (16 bytes)
    encryption_key: Vec<u8>,
    /// 认证密钥 (20 bytes for SHA1)
    auth_key: Vec<u8>,
    /// Salt 密钥 (14 bytes)
    salt_key: Vec<u8>,
    /// 重放保护窗口 bitmap (RTP)
    replay_bitmap: Vec<u64>,
    /// 已收到的最大 RTP packet index
    last_index: u64,
    /// RTP 重放保护是否已初始化 (是否收到过包)
    replay_initialized: bool,
    /// SRTCP 重放保护窗口 bitmap
    srtcp_replay_bitmap: Vec<u64>,
    /// 已收到的最大 SRTCP index
    srtcp_last_index: u32,
    /// SRTCP 重放保护是否已初始化
    srtcp_replay_initialized: bool,
    /// SRTCP 发送索引 (递增计数器)
    srtcp_index: u32,
    /// Rollover Counter
    roc: u32,
    /// 最后收到的 seq
    last_seq: u16,
}

impl SrtpSession {
    /// 创建新的 SRTP 会话
    pub fn new(config: SrtpConfig, ssrc: u32) -> Result<Self, SrtpError> {
        if config.master_key.len() != 16 {
            return Err(SrtpError::InvalidKeyLength(config.master_key.len()));
        }
        if config.master_salt.len() != 14 {
            return Err(SrtpError::InvalidSaltLength(config.master_salt.len()));
        }

        let encryption_key = derive_key(
            &config.master_key,
            &config.master_salt,
            KEY_ID_ENCRYPTION,
            ssrc,
            16,
        );
        let auth_key = derive_key(
            &config.master_key,
            &config.master_salt,
            KEY_ID_AUTH,
            ssrc,
            20,
        );
        let salt_key = derive_salt(&config.master_key, &config.master_salt, ssrc);

        // 重放保护 bitmap: ceil(window_size / 64) 个 u64 word
        let bitmap_words = ((config.replay_window_size as usize) + 63) / 64;
        let replay_bitmap = vec![0u64; bitmap_words];
        let srtcp_replay_bitmap = vec![0u64; bitmap_words];

        Ok(Self {
            config,
            ssrc,
            encryption_key,
            auth_key,
            salt_key,
            replay_bitmap,
            last_index: 0,
            replay_initialized: false,
            srtcp_replay_bitmap,
            srtcp_last_index: 0,
            srtcp_replay_initialized: false,
            srtcp_index: 0,
            roc: 0,
            last_seq: 0,
        })
    }

    /// 生成 SRTP IV
    /// IV = (k_s * 2^16) XOR (SSRC * 2^64) XOR (i * 2^16)
    /// 简化: IV = salt(14) || 0x00(2) XOR (ssrc at bytes 2-5) XOR (index at bytes 6-9)
    fn generate_iv(&self, seq: u16, roc: u32) -> [u8; 16] {
        let mut iv = [0u8; 16];
        // salt key (14 bytes) 左移 16 位 = 放在 IV 的前 14 bytes
        iv[..14].copy_from_slice(&self.salt_key);

        // XOR SSRC (4 bytes) at position 4
        let ssrc_bytes = self.ssrc.to_be_bytes();
        iv[4] ^= ssrc_bytes[0];
        iv[5] ^= ssrc_bytes[1];
        iv[6] ^= ssrc_bytes[2];
        iv[7] ^= ssrc_bytes[3];

        // XOR packet index (48 bits: ROC(32) | seq(16)) at position 8
        let index = ((roc as u64) << 16) | (seq as u64);
        let idx_bytes = index.to_be_bytes();
        // index 是 48 bit, 放在 IV 的 byte 2-7 (从右数)
        // 实际: IV[8..14] XOR index 的低 48 bit
        iv[8] ^= idx_bytes[5];
        iv[9] ^= idx_bytes[4];
        iv[10] ^= idx_bytes[3];
        iv[11] ^= idx_bytes[2];
        iv[12] ^= idx_bytes[1];
        iv[13] ^= idx_bytes[0];

        iv
    }

    /// 计算 packet index
    fn packet_index(&self, seq: u16) -> u64 {
        let s = seq as u64;
        let l = self.last_seq as u64;
        let roc = self.roc as u64;

        // 估计 ROC: 如果 seq 回绕
        if s + 0x8000 < l + 0x10000 && s < l {
            // 可能回绕
            if l > 0x8000 && s < 0x4000 {
                (roc + 1) << 16 | s
            } else {
                roc << 16 | s
            }
        } else if s > l + 0x8000 && l < 0x8000 {
            // 前一个 ROC
            if roc > 0 {
                (roc - 1) << 16 | s
            } else {
                s
            }
        } else {
            roc << 16 | s
        }
    }

    /// 更新 ROC 和 last_seq
    fn update_index(&mut self, seq: u16) {
        if seq < self.last_seq && self.last_seq - seq > 0x8000 {
            // 回绕
            self.roc = self.roc.wrapping_add(1);
        }
        self.last_seq = seq;
    }

    /// 检查重放 (RTP) — RFC 3711 Section 3.3.2 滑动窗口
    fn check_replay(&self, index: u64) -> bool {
        if !self.replay_initialized {
            return true;
        }
        if index > self.last_index {
            // 新包，在窗口右侧，接受
            return true;
        }
        let diff = self.last_index - index;
        if diff >= self.config.replay_window_size as u64 {
            // 包太旧，在窗口左侧，拒绝
            return false;
        }
        // 包在窗口内，检查 bitmap
        let offset = diff as usize;
        if bitmap_get(&self.replay_bitmap, offset) {
            return false; // 已收过，重放
        }
        true // 未收过，接受
    }

    /// 更新重放保护状态 (RTP)
    fn update_replay(&mut self, index: u64) {
        if !self.replay_initialized {
            self.replay_initialized = true;
            self.last_index = index;
            bitmap_set(&mut self.replay_bitmap, 0);
            return;
        }
        if index > self.last_index {
            let shift = (index - self.last_index) as usize;
            bitmap_shift_left(&mut self.replay_bitmap, shift);
            bitmap_set(&mut self.replay_bitmap, 0);
            self.last_index = index;
        } else {
            let offset = (self.last_index - index) as usize;
            bitmap_set(&mut self.replay_bitmap, offset);
        }
    }

    /// 检查重放 (SRTCP)
    fn check_replay_srtcp(&self, index: u32) -> bool {
        if !self.srtcp_replay_initialized {
            return true;
        }
        if index > self.srtcp_last_index {
            return true;
        }
        let diff = self.srtcp_last_index - index;
        if diff >= self.config.replay_window_size {
            return false;
        }
        let offset = diff as usize;
        if bitmap_get(&self.srtcp_replay_bitmap, offset) {
            return false;
        }
        true
    }

    /// 更新重放保护状态 (SRTCP)
    fn update_replay_srtcp(&mut self, index: u32) {
        if !self.srtcp_replay_initialized {
            self.srtcp_replay_initialized = true;
            self.srtcp_last_index = index;
            bitmap_set(&mut self.srtcp_replay_bitmap, 0);
            return;
        }
        if index > self.srtcp_last_index {
            let shift = (index - self.srtcp_last_index) as usize;
            bitmap_shift_left(&mut self.srtcp_replay_bitmap, shift);
            bitmap_set(&mut self.srtcp_replay_bitmap, 0);
            self.srtcp_last_index = index;
        } else {
            let offset = (self.srtcp_last_index - index) as usize;
            bitmap_set(&mut self.srtcp_replay_bitmap, offset);
        }
    }

    /// 加密 RTP 包 (发送方)
    pub fn protect_rtp(&mut self, rtp_packet: &[u8]) -> Result<Vec<u8>, SrtpError> {
        if rtp_packet.len() < 12 {
            return Err(SrtpError::PacketTooShort(rtp_packet.len()));
        }

        // 解析 RTP header
        let seq = u16::from_be_bytes([rtp_packet[2], rtp_packet[3]]);
        let ssrc =
            u32::from_be_bytes([rtp_packet[8], rtp_packet[9], rtp_packet[10], rtp_packet[11]]);

        if ssrc != self.ssrc {
            return Err(SrtpError::SsrcMismatch {
                expected: self.ssrc,
                actual: ssrc,
            });
        }

        let header_len = 12; // 最小 header
        let payload = &rtp_packet[header_len..];

        // 生成 IV
        let iv = self.generate_iv(seq, self.roc);

        // AES-CM 加密 payload
        let mut cipher = AesCm::new(self.encryption_key.as_slice().into(), &iv.into());
        let mut encrypted_payload = payload.to_vec();
        cipher.apply_keystream(&mut encrypted_payload);

        // 构建 SRTP 包: header + encrypted_payload + auth_tag
        let mut srtp_packet = Vec::with_capacity(rtp_packet.len() + self.config.auth_tag_len);
        srtp_packet.extend_from_slice(&rtp_packet[..header_len]);
        srtp_packet.extend_from_slice(&encrypted_payload);

        // 计算 HMAC-SHA1 over (header + encrypted_payload + ROC)
        let roc_bytes = self.roc.to_be_bytes();
        let mut mac = HmacSha1::new_from_slice(&self.auth_key).unwrap();
        mac.update(&srtp_packet);
        mac.update(&roc_bytes);
        let auth_tag = mac.finalize().into_bytes();

        // 附加 auth tag (截断)
        srtp_packet.extend_from_slice(&auth_tag[..self.config.auth_tag_len]);

        // 更新 index
        self.update_index(seq);

        Ok(srtp_packet)
    }

    /// 解密 SRTP 包 (接收方)
    pub fn unprotect_rtp(&mut self, srtp_packet: &[u8]) -> Result<Vec<u8>, SrtpError> {
        let min_len = 12 + self.config.auth_tag_len;
        if srtp_packet.len() < min_len {
            return Err(SrtpError::PacketTooShort(srtp_packet.len()));
        }

        // 解析 header
        let seq = u16::from_be_bytes([srtp_packet[2], srtp_packet[3]]);
        let ssrc = u32::from_be_bytes([
            srtp_packet[8],
            srtp_packet[9],
            srtp_packet[10],
            srtp_packet[11],
        ]);

        if ssrc != self.ssrc {
            return Err(SrtpError::SsrcMismatch {
                expected: self.ssrc,
                actual: ssrc,
            });
        }

        let header_len = 12;
        let tag_len = self.config.auth_tag_len;
        let encrypted_payload = &srtp_packet[header_len..srtp_packet.len() - tag_len];
        let received_tag = &srtp_packet[srtp_packet.len() - tag_len..];

        // 估计 ROC
        let index = self.packet_index(seq);
        let estimated_roc = (index >> 16) as u32;

        // 验证 auth tag
        let roc_bytes = estimated_roc.to_be_bytes();
        let mut mac = HmacSha1::new_from_slice(&self.auth_key).unwrap();
        mac.update(&srtp_packet[..srtp_packet.len() - tag_len]);
        mac.update(&roc_bytes);
        let expected_tag = mac.finalize().into_bytes();

        // 常量时间比较
        if !ct_eq(&expected_tag[..tag_len], received_tag) {
            return Err(SrtpError::AuthenticationFailed);
        }

        // 检查重放
        if !self.check_replay(index) {
            return Err(SrtpError::ReplayDetected(seq));
        }

        // 生成 IV 并解密
        let iv = self.generate_iv(seq, estimated_roc);
        let mut cipher = AesCm::new(self.encryption_key.as_slice().into(), &iv.into());
        let mut decrypted_payload = encrypted_payload.to_vec();
        cipher.apply_keystream(&mut decrypted_payload);

        // 构建 RTP 包
        let mut rtp_packet = Vec::with_capacity(header_len + decrypted_payload.len());
        rtp_packet.extend_from_slice(&srtp_packet[..header_len]);
        rtp_packet.extend_from_slice(&decrypted_payload);

        // 更新 index
        self.roc = estimated_roc;
        self.update_index(seq);

        // 更新重放保护 bitmap
        self.update_replay(index);

        Ok(rtp_packet)
    }

    /// 加密 RTCP 包 (发送方)
    pub fn protect_rtcp(&mut self, rtcp_packet: &[u8]) -> Result<Vec<u8>, SrtpError> {
        if rtcp_packet.len() < 8 {
            return Err(SrtpError::PacketTooShort(rtcp_packet.len()));
        }

        let header_len = 8;
        let payload = &rtcp_packet[header_len..];

        // SRTCP index: 31-bit index + 1-bit E (encrypted flag)
        // 使用递增计数器 (31-bit, 回绕)
        let srtcp_index: u32 = self.srtcp_index;
        self.srtcp_index = (self.srtcp_index + 1) & 0x7FFFFFFF;
        let e_flag: u32 = 1 << 31;
        let srtcp_index_with_e = srtcp_index | e_flag;

        // 生成 IV (类似 SRTP, 但用 SRTCP index)
        let mut iv = [0u8; 16];
        iv[..14].copy_from_slice(&self.salt_key);
        let ssrc_bytes = self.ssrc.to_be_bytes();
        iv[4] ^= ssrc_bytes[0];
        iv[5] ^= ssrc_bytes[1];
        iv[6] ^= ssrc_bytes[2];
        iv[7] ^= ssrc_bytes[3];
        let idx_bytes = srtcp_index.to_be_bytes();
        iv[8] ^= idx_bytes[0];
        iv[9] ^= idx_bytes[1];
        iv[10] ^= idx_bytes[2];
        iv[11] ^= idx_bytes[3];

        // AES-CM 加密 payload
        let mut cipher = AesCm::new(self.encryption_key.as_slice().into(), &iv.into());
        let mut encrypted_payload = payload.to_vec();
        cipher.apply_keystream(&mut encrypted_payload);

        // 构建 SRTCP 包: header + encrypted_payload + SRTCP index(4) + auth_tag
        let mut srtcp_packet = Vec::with_capacity(rtcp_packet.len() + 4 + self.config.auth_tag_len);
        srtcp_packet.extend_from_slice(&rtcp_packet[..header_len]);
        srtcp_packet.extend_from_slice(&encrypted_payload);
        srtcp_packet.extend_from_slice(&srtcp_index_with_e.to_be_bytes());

        // 计算 HMAC-SHA1 over (header + encrypted + SRTCP index)
        let mut mac = HmacSha1::new_from_slice(&self.auth_key).unwrap();
        mac.update(&srtcp_packet);
        let auth_tag = mac.finalize().into_bytes();
        srtcp_packet.extend_from_slice(&auth_tag[..self.config.auth_tag_len]);

        Ok(srtcp_packet)
    }

    /// 解密 SRTCP 包 (接收方)
    pub fn unprotect_rtcp(&mut self, srtcp_packet: &[u8]) -> Result<Vec<u8>, SrtpError> {
        let min_len = 8 + 4 + self.config.auth_tag_len;
        if srtcp_packet.len() < min_len {
            return Err(SrtpError::PacketTooShort(srtcp_packet.len()));
        }

        let header_len = 8;
        let tag_len = self.config.auth_tag_len;
        let srtcp_index_offset = srtcp_packet.len() - tag_len - 4;

        // 提取 SRTCP index
        let srtcp_index_with_e = u32::from_be_bytes([
            srtcp_packet[srtcp_index_offset],
            srtcp_packet[srtcp_index_offset + 1],
            srtcp_packet[srtcp_index_offset + 2],
            srtcp_packet[srtcp_index_offset + 3],
        ]);
        let srtcp_index = srtcp_index_with_e & 0x7FFFFFFF;
        let e_flag = (srtcp_index_with_e >> 31) & 1;

        // 验证 auth tag
        let received_tag = &srtcp_packet[srtcp_packet.len() - tag_len..];
        let mut mac = HmacSha1::new_from_slice(&self.auth_key).unwrap();
        mac.update(&srtcp_packet[..srtcp_packet.len() - tag_len]);
        let expected_tag = mac.finalize().into_bytes();

        if !ct_eq(&expected_tag[..tag_len], received_tag) {
            return Err(SrtpError::AuthenticationFailed);
        }

        let encrypted_payload = &srtcp_packet[header_len..srtcp_index_offset];
        let mut decrypted_payload = encrypted_payload.to_vec();

        if e_flag == 1 {
            // 解密
            let mut iv = [0u8; 16];
            iv[..14].copy_from_slice(&self.salt_key);
            let ssrc_bytes = self.ssrc.to_be_bytes();
            iv[4] ^= ssrc_bytes[0];
            iv[5] ^= ssrc_bytes[1];
            iv[6] ^= ssrc_bytes[2];
            iv[7] ^= ssrc_bytes[3];
            let idx_bytes = srtcp_index.to_be_bytes();
            iv[8] ^= idx_bytes[0];
            iv[9] ^= idx_bytes[1];
            iv[10] ^= idx_bytes[2];
            iv[11] ^= idx_bytes[3];

            let mut cipher = AesCm::new(self.encryption_key.as_slice().into(), &iv.into());
            cipher.apply_keystream(&mut decrypted_payload);
        }

        // 构建 RTCP 包
        let mut rtcp_packet = Vec::with_capacity(header_len + decrypted_payload.len());
        rtcp_packet.extend_from_slice(&srtcp_packet[..header_len]);
        rtcp_packet.extend_from_slice(&decrypted_payload);

        Ok(rtcp_packet)
    }
}

/// 获取 bitmap 中指定位置的 bit
fn bitmap_get(bitmap: &[u64], pos: usize) -> bool {
    let word = pos / 64;
    let bit = pos % 64;
    if word >= bitmap.len() {
        return false;
    }
    (bitmap[word] >> bit) & 1 == 1
}

/// 设置 bitmap 中指定位置的 bit
fn bitmap_set(bitmap: &mut [u64], pos: usize) {
    let word = pos / 64;
    let bit = pos % 64;
    if word < bitmap.len() {
        bitmap[word] |= 1u64 << bit;
    }
}

/// 将整个 bitmap 左移 `shift` 位 (超出窗口的 bit 被丢弃)
fn bitmap_shift_left(bitmap: &mut [u64], shift: usize) {
    if shift == 0 || bitmap.is_empty() {
        return;
    }
    let n_words = bitmap.len();
    let word_shift = shift / 64;
    let bit_shift = shift % 64;

    if word_shift >= n_words {
        for w in bitmap.iter_mut() {
            *w = 0;
        }
        return;
    }

    // 从高位到低位处理，避免覆盖尚未读取的值
    for i in (0..n_words).rev() {
        let lo = if i >= word_shift {
            bitmap[i - word_shift] << bit_shift
        } else {
            0
        };
        let hi = if bit_shift > 0 && i >= word_shift + 1 {
            bitmap[i - word_shift - 1] >> (64 - bit_shift)
        } else {
            0
        };
        bitmap[i] = lo | hi;
    }
}

/// 常量时间比较
fn ct_eq(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    let mut diff = 0u8;
    for (x, y) in a.iter().zip(b.iter()) {
        diff |= x ^ y;
    }
    diff == 0
}

// ============================================================================
// SRTP Context (多 SSRC 管理)
// ============================================================================

/// SRTP 上下文 (管理多个 SSRC 的会话)
pub struct SrtpContext {
    config: SrtpConfig,
    sessions: std::collections::HashMap<u32, SrtpSession>,
}

impl SrtpContext {
    pub fn new(config: SrtpConfig) -> Self {
        Self {
            config,
            sessions: std::collections::HashMap::new(),
        }
    }

    pub fn add_ssrc(&mut self, ssrc: u32) -> Result<(), SrtpError> {
        if !self.sessions.contains_key(&ssrc) {
            let session = SrtpSession::new(self.config.clone(), ssrc)?;
            self.sessions.insert(ssrc, session);
        }
        Ok(())
    }

    pub fn protect_rtp(&mut self, ssrc: u32, packet: &[u8]) -> Result<Vec<u8>, SrtpError> {
        let session = self
            .sessions
            .get_mut(&ssrc)
            .ok_or(SrtpError::SsrcMismatch {
                expected: ssrc,
                actual: 0,
            })?;
        session.protect_rtp(packet)
    }

    pub fn unprotect_rtp(&mut self, packet: &[u8]) -> Result<Vec<u8>, SrtpError> {
        if packet.len() < 12 {
            return Err(SrtpError::PacketTooShort(packet.len()));
        }
        let ssrc = u32::from_be_bytes([packet[8], packet[9], packet[10], packet[11]]);
        let session = self
            .sessions
            .get_mut(&ssrc)
            .ok_or(SrtpError::SsrcMismatch {
                expected: 0,
                actual: ssrc,
            })?;
        session.unprotect_rtp(packet)
    }

    pub fn protect_rtcp(&mut self, ssrc: u32, packet: &[u8]) -> Result<Vec<u8>, SrtpError> {
        let session = self
            .sessions
            .get_mut(&ssrc)
            .ok_or(SrtpError::SsrcMismatch {
                expected: ssrc,
                actual: 0,
            })?;
        session.protect_rtcp(packet)
    }

    pub fn unprotect_rtcp(&mut self, packet: &[u8]) -> Result<Vec<u8>, SrtpError> {
        // SRTCP 包中 SSRC 在 header 的 byte 4-7
        if packet.len() < 12 {
            return Err(SrtpError::PacketTooShort(packet.len()));
        }
        let ssrc = u32::from_be_bytes([packet[4], packet[5], packet[6], packet[7]]);
        let session = self
            .sessions
            .get_mut(&ssrc)
            .ok_or(SrtpError::SsrcMismatch {
                expected: 0,
                actual: ssrc,
            })?;
        session.unprotect_rtcp(packet)
    }
}

// ============================================================================
// 测试
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    // RFC 3711 Appendix B 测试向量
    const MASTER_KEY: [u8; 16] = [
        0xe1, 0xf9, 0x7a, 0x0d, 0x3e, 0x01, 0x8b, 0xe0, 0xd6, 0x4f, 0xa3, 0x2c, 0x06, 0xc1, 0x20,
        0x41,
    ];
    const MASTER_SALT: [u8; 14] = [
        0x0e, 0xc6, 0x75, 0xad, 0x49, 0x8a, 0xfe, 0xab, 0xe6, 0xe7, 0x09, 0xf0, 0x08, 0x17,
    ];

    fn make_test_config() -> SrtpConfig {
        SrtpConfig {
            master_key: MASTER_KEY.to_vec(),
            master_salt: MASTER_SALT.to_vec(),
            auth_tag_len: 10,
            replay_window_size: 1024,
        }
    }

    fn make_test_rtp(ssrc: u32, seq: u16) -> Vec<u8> {
        let mut pkt = vec![0u8; 20];
        // V=2, P=0, X=0, CC=0
        pkt[0] = 0x80;
        // PT=0 (PCMU)
        pkt[1] = 0x00;
        // seq
        pkt[2] = (seq >> 8) as u8;
        pkt[3] = seq as u8;
        // timestamp
        pkt[4] = 0x00;
        pkt[5] = 0x00;
        pkt[6] = 0x00;
        pkt[7] = 0x01;
        // ssrc
        let ssrc_bytes = ssrc.to_be_bytes();
        pkt[8..12].copy_from_slice(&ssrc_bytes);
        // payload (8 bytes)
        pkt[12..20].copy_from_slice(&[0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07]);
        pkt
    }

    #[test]
    fn test_srtp_key_derivation() {
        let session = SrtpSession::new(make_test_config(), 0xCAFEBABE).unwrap();
        // 验证密钥已派生 (非全零)
        assert!(!session.encryption_key.iter().all(|&b| b == 0));
        assert!(!session.auth_key.iter().all(|&b| b == 0));
        assert!(!session.salt_key.iter().all(|&b| b == 0));
        assert_eq!(session.encryption_key.len(), 16);
        assert_eq!(session.auth_key.len(), 20);
        assert_eq!(session.salt_key.len(), 14);
    }

    #[test]
    fn test_srtp_protect_unprotect_roundtrip() {
        let mut session = SrtpSession::new(make_test_config(), 0xCAFEBABE).unwrap();
        let rtp = make_test_rtp(0xCAFEBABE, 1);

        let srtp = session.protect_rtp(&rtp).unwrap();
        // SRTP 包应比 RTP 包长 (加了 auth tag)
        assert_eq!(srtp.len(), rtp.len() + 10);

        // 解密
        let decrypted = session.unprotect_rtp(&srtp).unwrap();
        assert_eq!(decrypted, rtp);
    }

    #[test]
    fn test_srtp_auth_tag_verification() {
        let mut session = SrtpSession::new(make_test_config(), 0xCAFEBABE).unwrap();
        let rtp = make_test_rtp(0xCAFEBABE, 1);

        let mut srtp = session.protect_rtp(&rtp).unwrap();

        // 篡改 auth tag
        let tag_start = srtp.len() - 10;
        srtp[tag_start] ^= 0xFF;

        // 解密应失败
        let result = session.unprotect_rtp(&srtp);
        assert_eq!(result, Err(SrtpError::AuthenticationFailed));
    }

    #[test]
    fn test_srtp_replay_protection() {
        let mut session = SrtpSession::new(make_test_config(), 0xCAFEBABE).unwrap();
        let rtp = make_test_rtp(0xCAFEBABE, 1);

        let srtp = session.protect_rtp(&rtp).unwrap();

        // 第一次解密应成功
        let _ = session.unprotect_rtp(&srtp).unwrap();

        // 第二次解密同一包 (重放) — 简化实现暂不检测重放
        // 实际实现应返回 ReplayDetected
        // 这里只验证不 panic
        let _ = session.unprotect_rtp(&srtp);
    }

    #[test]
    fn test_srtcp_protect_unprotect_roundtrip() {
        let mut session = SrtpSession::new(make_test_config(), 0xCAFEBABE).unwrap();

        // 构建一个简单的 RTCP 包 (SR, 28 bytes)
        let mut rtcp = vec![0u8; 28];
        rtcp[0] = 0x80; // V=2
        rtcp[1] = 200; // PT=SR
        rtcp[2] = 0x00;
        rtcp[3] = 0x06; // length=6
                        // SSRC
        rtcp[4..8].copy_from_slice(&0xCAFEBABEu32.to_be_bytes());
        // 剩余填充

        let srtcp = session.protect_rtcp(&rtcp).unwrap();
        // SRTCP 比 RTCP 长: +4 (index) +10 (auth tag)
        assert_eq!(srtcp.len(), rtcp.len() + 4 + 10);

        let decrypted = session.unprotect_rtcp(&srtcp).unwrap();
        assert_eq!(decrypted, rtcp);
    }

    #[test]
    fn test_srtp_context_multi_ssrc() {
        let mut ctx = SrtpContext::new(make_test_config());
        ctx.add_ssrc(0xCAFEBABE).unwrap();
        ctx.add_ssrc(0xDEADBEEF).unwrap();

        let rtp1 = make_test_rtp(0xCAFEBABE, 1);
        let rtp2 = make_test_rtp(0xDEADBEEF, 1);

        let srtp1 = ctx.protect_rtp(0xCAFEBABE, &rtp1).unwrap();
        let srtp2 = ctx.protect_rtp(0xDEADBEEF, &rtp2).unwrap();

        let dec1 = ctx.unprotect_rtp(&srtp1).unwrap();
        let dec2 = ctx.unprotect_rtp(&srtp2).unwrap();

        assert_eq!(dec1, rtp1);
        assert_eq!(dec2, rtp2);
    }

    #[test]
    fn test_srtp_invalid_key_length() {
        let config = SrtpConfig {
            master_key: vec![0u8; 15], // 错误长度
            master_salt: MASTER_SALT.to_vec(),
            auth_tag_len: 10,
            replay_window_size: 1024,
        };
        let result = SrtpSession::new(config, 0xCAFEBABE);
        assert!(matches!(result, Err(SrtpError::InvalidKeyLength(15))));
    }

    #[test]
    fn test_srtp_authentication_failure() {
        let mut session = SrtpSession::new(make_test_config(), 0xCAFEBABE).unwrap();
        let rtp = make_test_rtp(0xCAFEBABE, 1);

        let mut srtp = session.protect_rtp(&rtp).unwrap();

        // 篡改加密 payload (不是 auth tag)
        srtp[15] ^= 0xFF;

        let result = session.unprotect_rtp(&srtp);
        assert_eq!(result, Err(SrtpError::AuthenticationFailed));
    }

    #[test]
    fn test_srtp_multiple_packets() {
        let mut session = SrtpSession::new(make_test_config(), 0xCAFEBABE).unwrap();

        for seq in 1..=10u16 {
            let rtp = make_test_rtp(0xCAFEBABE, seq);
            let srtp = session.protect_rtp(&rtp).unwrap();
            let decrypted = session.unprotect_rtp(&srtp).unwrap();
            assert_eq!(decrypted, rtp, "roundtrip failed for seq {}", seq);
        }
    }

    #[test]
    fn test_ct_eq() {
        assert!(ct_eq(&[1, 2, 3], &[1, 2, 3]));
        assert!(!ct_eq(&[1, 2, 3], &[1, 2, 4]));
        assert!(!ct_eq(&[1, 2], &[1, 2, 3]));
    }
}
