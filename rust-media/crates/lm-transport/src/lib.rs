//! lm-transport — RTP/WebRTC 传输层
//!
//! Phase 1：RTP 包结构 + 打包/解包。
//! WebRTC/SRTP 由 Go 层处理，Rust 收发明文 RTP。

pub use lm_core::Transport;

use bytes::{Bytes, BytesMut, BufMut};

// ============================================================================
// RTP 包
// ============================================================================

/// RTP 包（明文，Go 层已处理 ICE/DTLS/SRTP 解密）
#[derive(Debug, Clone)]
pub struct RtpPacket {
    /// 同步源标识
    pub ssrc: u32,
    /// 负载类型
    pub payload_type: u8,
    /// 序列号
    pub sequence_number: u16,
    /// RTP 时间戳
    pub timestamp: u32,
    /// 标志位（帧边界）
    pub marker: bool,
    /// 负载
    pub payload: Bytes,
    /// simulcast RID（如 "low"/"mid"/"high"）
    pub rid: String,
}

impl RtpPacket {
    /// 从原始字节解析 RTP 包
    ///
    /// RTP header 格式（RFC 3550）：
    /// ```text
    ///  0                   1                   2                   3
    ///  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
    /// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
    /// |V=2|P|X|  CC   |M|     PT      |       sequence number         |
    /// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
    /// |                           timestamp                           |
    /// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
    /// |           synchronization source (SSRC) identifier            |
    /// +=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+
    /// |            contributing source (CSRC) identifiers             |
    /// |                             ....                              |
    /// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
    /// |                           payload                             |
    /// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
    /// ```
    pub fn from_bytes(data: &[u8]) -> Option<Self> {
        if data.len() < 12 {
            return None;
        }

        let version = (data[0] >> 6) & 0x03;
        if version != 2 {
            return None;
        }

        let has_padding = (data[0] >> 5) & 0x01 == 1;
        let has_extension = (data[0] >> 4) & 0x01 == 1;
        let csrc_count = (data[0] & 0x0F) as usize;
        let marker = (data[1] >> 7) & 0x01 == 1;
        let payload_type = data[1] & 0x7F;
        let sequence_number = u16::from_be_bytes([data[2], data[3]]);
        let timestamp = u32::from_be_bytes([data[4], data[5], data[6], data[7]]);
        let ssrc = u32::from_be_bytes([data[8], data[9], data[10], data[11]]);

        let mut offset = 12 + csrc_count * 4;

        // 跳过 CSRC
        if offset > data.len() {
            return None;
        }

        // 跳过 header extension
        if has_extension && offset + 4 <= data.len() {
            let ext_len = u16::from_be_bytes([data[offset + 2], data[offset + 3]]) as usize;
            offset += 4 + ext_len * 4;
        }

        if offset > data.len() {
            return None;
        }

        // 处理 padding
        let payload_end = if has_padding && data.len() > 0 {
            let pad_len = data[data.len() - 1] as usize;
            if pad_len <= data.len() - offset {
                data.len() - pad_len
            } else {
                data.len()
            }
        } else {
            data.len()
        };

        let payload = Bytes::copy_from_slice(&data[offset..payload_end]);

        Some(Self {
            ssrc,
            payload_type,
            sequence_number,
            timestamp,
            marker,
            payload,
            rid: String::new(),
        })
    }

    /// 打包为 RTP 字节流
    pub fn to_bytes(&self) -> Bytes {
        let mut buf = BytesMut::with_capacity(12 + self.payload.len());

        // V=2, P=0, X=0, CC=0
        buf.put_u8(0x80);
        // M + PT
        buf.put_u8(((self.marker as u8) << 7) | (self.payload_type & 0x7F));
        // Sequence number
        buf.put_u16(self.sequence_number);
        // Timestamp
        buf.put_u32(self.timestamp);
        // SSRC
        buf.put_u32(self.ssrc);
        // Payload
        buf.put_slice(&self.payload);

        buf.freeze()
    }
}

// ============================================================================
// RTP 包构建器（参考 RustPBX rtp_track_builder）
// ============================================================================

/// RTP 包构建器（维护序列号和时间戳递增）
pub struct RtpPacketBuilder {
    ssrc: u32,
    payload_type: u8,
    sequence: u16,
    timestamp: u32,
    clock_rate: u32,
    frame_samples: u32,
}

impl RtpPacketBuilder {
    pub fn new(ssrc: u32, payload_type: u8, clock_rate: u32, frame_samples: u32) -> Self {
        Self {
            ssrc,
            payload_type,
            sequence: 0,
            timestamp: 0,
            clock_rate,
            frame_samples,
        }
    }

    /// 构建下一个 RTP 包
    pub fn build(&mut self, payload: Bytes, marker: bool) -> RtpPacket {
        let pkt = RtpPacket {
            ssrc: self.ssrc,
            payload_type: self.payload_type,
            sequence_number: self.sequence,
            timestamp: self.timestamp,
            marker,
            payload,
            rid: String::new(),
        };
        self.sequence = self.sequence.wrapping_add(1);
        self.timestamp = self.timestamp.wrapping_add(self.frame_samples);
        pkt
    }

    pub fn ssrc(&self) -> u32 {
        self.ssrc
    }

    pub fn payload_type(&self) -> u8 {
        self.payload_type
    }

    pub fn clock_rate(&self) -> u32 {
        self.clock_rate
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_rtp_roundtrip() {
        let payload = Bytes::from_static(b"hello world");
        let pkt = RtpPacket {
            ssrc: 12345,
            payload_type: 0,
            sequence_number: 42,
            timestamp: 960,
            marker: true,
            payload: payload.clone(),
            rid: String::new(),
        };

        let bytes = pkt.to_bytes();
        let parsed = RtpPacket::from_bytes(&bytes).unwrap();

        assert_eq!(parsed.ssrc, 12345);
        assert_eq!(parsed.payload_type, 0);
        assert_eq!(parsed.sequence_number, 42);
        assert_eq!(parsed.timestamp, 960);
        assert!(parsed.marker);
        assert_eq!(parsed.payload, payload);
    }

    #[test]
    fn test_rtp_no_marker() {
        let pkt = RtpPacket {
            ssrc: 1,
            payload_type: 8,
            sequence_number: 100,
            timestamp: 160,
            marker: false,
            payload: Bytes::from_static(b"audio"),
            rid: String::new(),
        };
        let bytes = pkt.to_bytes();
        let parsed = RtpPacket::from_bytes(&bytes).unwrap();
        assert!(!parsed.marker);
        assert_eq!(parsed.payload_type, 8);
    }

    #[test]
    fn test_rtp_invalid_version() {
        let data = [0x40, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]; // V=1
        assert!(RtpPacket::from_bytes(&data).is_none());
    }

    #[test]
    fn test_rtp_too_short() {
        let data = [0x80, 0x00];
        assert!(RtpPacket::from_bytes(&data).is_none());
    }

    #[test]
    fn test_packet_builder() {
        let mut builder = RtpPacketBuilder::new(12345, 0, 8000, 160);
        let p1 = builder.build(Bytes::from_static(b"frame1"), false);
        assert_eq!(p1.sequence_number, 0);
        assert_eq!(p1.timestamp, 0);

        let p2 = builder.build(Bytes::from_static(b"frame2"), true);
        assert_eq!(p2.sequence_number, 1);
        assert_eq!(p2.timestamp, 160);
        assert!(p2.marker);

        let p3 = builder.build(Bytes::from_static(b"frame3"), false);
        assert_eq!(p3.sequence_number, 2);
        assert_eq!(p3.timestamp, 320);
    }
}
