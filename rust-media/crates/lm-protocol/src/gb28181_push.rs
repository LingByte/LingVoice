//! GB28181 推流输出 — 向国标平台推送 PS 流
//!
//! 实现 GB28181 推流流程：
//! 1. SIP REGISTER 注册到国标平台
//! 2. 等待平台 INVITE（或主动发起）
//! 3. SDP 协商
//! 4. RTP/AVP 推送 PS 封装的媒体流
//!
//! 简化实现：提供 PS 封装 + RTP 打包 + UDP 推送能力。
//! SIP 信令由 Go 协议层处理，这里只负责媒体推送。

use anyhow::{anyhow, Result};
use tokio::net::UdpSocket;
use lm_core::{MediaFrame, TrackKind, CodecType};

/// GB28181 推流客户端
pub struct Gb28181Pusher {
    /// 目标 IP:port（从 SDP 协商获得）
    remote_addr: String,
    /// UDP socket
    socket: Option<UdpSocket>,
    /// PS 封装器
    ps_muxer: PsMuxer,
    /// RTP 序列号
    rtp_seq: u16,
    /// RTP SSRC
    ssrc: u32,
    /// RTP 时间戳
    rtp_timestamp: u32,
}

impl Gb28181Pusher {
    pub fn new(remote_addr: &str, ssrc: u32) -> Self {
        Self {
            remote_addr: remote_addr.to_string(),
            socket: None,
            ps_muxer: PsMuxer::new(),
            rtp_seq: 0,
            ssrc,
            rtp_timestamp: 0,
        }
    }

    /// 连接（绑定 UDP socket）
    pub async fn connect(&mut self) -> Result<()> {
        let socket = UdpSocket::bind("0.0.0.0:0")
            .await
            .map_err(|e| anyhow!("bind UDP: {e}"))?;
        socket.connect(&self.remote_addr)
            .await
            .map_err(|e| anyhow!("connect {addr}: {e}", addr = self.remote_addr))?;
        self.socket = Some(socket);
        Ok(())
    }

    /// 发送一帧媒体（封装为 PS + RTP）
    pub async fn send_frame(&mut self, frame: &MediaFrame) -> Result<()> {
        if self.socket.is_none() {
            return Err(anyhow!("not connected"));
        }

        // 封装为 PS 流
        let ps_data = self.ps_muxer.mux_frame(frame);

        // 分割为 RTP 包（MTU 1400，留余量给 RTP header + PS header）
        let mtu = 1400;
        let rtp_payload_type: u8 = 96; // 动态 PT for PS

        for chunk in ps_data.chunks(mtu) {
            let rtp_packet = build_rtp_packet(
                self.rtp_seq,
                self.rtp_timestamp,
                self.ssrc,
                rtp_payload_type,
                chunk,
                false, // marker bit 在最后一包设为 true
            );
            self.rtp_seq = self.rtp_seq.wrapping_add(1);

            if let Some(socket) = &self.socket {
                socket.send(&rtp_packet).await?;
            }
        }

        // 更新时间戳
        self.rtp_timestamp = self.rtp_timestamp.wrapping_add(9000); // 假设 30fps

        Ok(())
    }

    /// 关闭
    pub async fn close(&mut self) -> Result<()> {
        self.socket = None;
        Ok(())
    }
}

/// PS 封装器（简化版）
///
/// GB28181 PS 流结构：
/// - PS Header (pack_start_code + system_header + program_map)
/// - PES Packet (video/audio)
struct PsMuxer {
    /// 是否已发送 PS header
    header_sent: bool,
}

impl PsMuxer {
    fn new() -> Self {
        Self { header_sent: false }
    }

    /// 封装一帧为 PS 流
    fn mux_frame(&mut self, frame: &MediaFrame) -> Vec<u8> {
        let mut output = Vec::new();

        // PS Pack Header (每帧都发)
        output.extend_from_slice(&PS_PACK_START_CODE);
        output.extend_from_slice(&[0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]); // SCR + mux_rate

        // System Header (仅在第一帧)
        if !self.header_sent {
            output.extend_from_slice(&SYSTEM_HEADER_START_CODE);
            output.extend_from_slice(&[0x00, 0x0C]); // header length = 12
            output.extend_from_slice(&[0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]);
            self.header_sent = true;
        }

        // PES Packet
        let stream_id = match frame.kind {
            TrackKind::Video => 0xE0, // video stream
            TrackKind::Audio => 0xC0, // audio stream
        };

        let pes_payload_len = frame.data.len();
        let pes_packet_len = (pes_payload_len + 3) as u16; // PES header (3 bytes) + payload

        // PES start code
        output.extend_from_slice(&[0x00, 0x00, 0x01]);
        output.push(stream_id);
        output.extend_from_slice(&pes_packet_len.to_be_bytes());

        // PES header flags
        output.push(0x80); // no optional fields
        output.push(0x00); // no optional fields
        output.push(0x00); // no optional fields

        // PES payload
        output.extend_from_slice(&frame.data);

        output
    }
}

/// PS Pack Start Code
const PS_PACK_START_CODE: [u8; 4] = [0x00, 0x00, 0x01, 0xBA];
/// System Header Start Code
const SYSTEM_HEADER_START_CODE: [u8; 4] = [0x00, 0x00, 0x01, 0xBB];

/// 构造 RTP 包
fn build_rtp_packet(seq: u16, timestamp: u32, ssrc: u32, pt: u8, payload: &[u8], marker: bool) -> Vec<u8> {
    let mut packet = Vec::with_capacity(12 + payload.len());

    // RTP header
    let version: u8 = 2;
    let padding: u8 = 0;
    let extension: u8 = 0;
    let csrc_count: u8 = 0;
    let byte0 = (version << 6) | (padding << 5) | (extension << 4) | csrc_count;
    packet.push(byte0);

    let marker_bit: u8 = if marker { 1 } else { 0 };
    packet.push((marker_bit << 7) | pt);

    packet.extend_from_slice(&seq.to_be_bytes());
    packet.extend_from_slice(&timestamp.to_be_bytes());
    packet.extend_from_slice(&ssrc.to_be_bytes());

    // Payload
    packet.extend_from_slice(payload);

    packet
}

#[cfg(test)]
mod tests {
    use super::*;
    use bytes::Bytes;

    #[test]
    fn test_ps_muxer_video() {
        let mut muxer = PsMuxer::new();
        let frame = MediaFrame::video(
            CodecType::H264,
            9000,
            Bytes::from(vec![0, 0, 0, 1, 0x65, 0xAA]),
            1,
            true,
        );
        let ps = muxer.mux_frame(&frame);
        assert!(!ps.is_empty());
        // PS pack start code
        assert_eq!(&ps[0..4], &PS_PACK_START_CODE);
    }

    #[test]
    fn test_ps_muxer_audio() {
        let mut muxer = PsMuxer::new();
        let frame = MediaFrame::audio(
            CodecType::PcmU,
            160,
            Bytes::from(vec![0xFF; 160]),
            1,
        );
        let ps = muxer.mux_frame(&frame);
        assert!(!ps.is_empty());
    }

    #[test]
    fn test_build_rtp_packet() {
        let payload = vec![0x47, 0x40, 0x00, 0x10];
        let packet = build_rtp_packet(1, 9000, 12345, 96, &payload, false);

        // RTP header = 12 bytes
        assert_eq!(packet.len(), 12 + payload.len());
        // Version = 2
        assert_eq!(packet[0] >> 6, 2);
        // PT = 96
        assert_eq!(packet[1] & 0x7F, 96);
        // Seq = 1
        assert_eq!(u16::from_be_bytes([packet[2], packet[3]]), 1);
        // Timestamp = 9000
        assert_eq!(u32::from_be_bytes([packet[4], packet[5], packet[6], packet[7]]), 9000);
        // SSRC = 12345
        assert_eq!(u32::from_be_bytes([packet[8], packet[9], packet[10], packet[11]]), 12345);
    }

    #[test]
    fn test_gb28181_pusher_new() {
        let pusher = Gb28181Pusher::new("192.168.1.100:9000", 12345);
        assert_eq!(pusher.ssrc, 12345);
        assert_eq!(pusher.rtp_seq, 0);
    }

    #[test]
    fn test_ps_muxer_header_sent_once() {
        let mut muxer = PsMuxer::new();
        assert!(!muxer.header_sent);

        let frame = MediaFrame::video(
            CodecType::H264,
            9000,
            Bytes::from(vec![0x01]),
            1,
            true,
        );
        let ps1 = muxer.mux_frame(&frame);
        assert!(muxer.header_sent);

        // 第二帧不应该有 system header
        let ps2 = muxer.mux_frame(&frame);
        // ps1 应该比 ps2 长（包含 system header）
        assert!(ps1.len() > ps2.len());
    }
}
