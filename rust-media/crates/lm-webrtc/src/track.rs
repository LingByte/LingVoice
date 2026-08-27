//! Track 模块 — RTP 轨道收发
//!
//! 提供 [`RtpTrack`] 抽象，基于 tokio mpsc 通道在 PeerConnection 与上层之间传递 RTP 数据。

use lm_core::{CodecType, TrackKind};
use lm_transport::RtpPacket;
use tokio::sync::mpsc;

use crate::WebrtcError;

/// RTP 轨道
pub struct RtpTrack {
    pub id: String,
    pub kind: TrackKind,
    pub codec: CodecType,
    pub ssrc: u32,
    send_tx: mpsc::Sender<Vec<u8>>,
    /// 发送通道接收端（由驱动层消费写入 webrtc 轨道，保留以避免通道关闭）
    send_rx: mpsc::Receiver<Vec<u8>>,
    recv_rx: mpsc::Receiver<Vec<u8>>,
}

impl RtpTrack {
    /// 创建 RTP 轨道
    pub fn new(id: &str, kind: TrackKind, codec: CodecType, ssrc: u32) -> Self {
        let (send_tx, send_rx) = mpsc::channel::<Vec<u8>>(256);
        let (_recv_tx, recv_rx) = mpsc::channel::<Vec<u8>>(256);
        Self {
            id: id.to_string(),
            kind,
            codec,
            ssrc,
            send_tx,
            send_rx,
            recv_rx,
        }
    }

    /// 发送 RTP 包（打包为字节）
    pub async fn send_rtp(&self, packet: &RtpPacket) -> Result<(), WebrtcError> {
        let bytes = packet.to_bytes();
        self.send_tx
            .send(bytes.to_vec())
            .await
            .map_err(|e| WebrtcError::PeerConnection(format!("发送 RTP 失败: {e}")))
    }

    /// 接收 RTP 包（返回原始字节）
    pub async fn recv_rtp(&mut self) -> Option<Vec<u8>> {
        self.recv_rx.recv().await
    }

    /// 获取发送通道接收端的可变引用（供驱动层消费并写入 webrtc 轨道）
    pub fn send_rx_mut(&mut self) -> &mut mpsc::Receiver<Vec<u8>> {
        &mut self.send_rx
    }

    /// 轨道 ID
    pub fn id(&self) -> &str {
        &self.id
    }

    /// 轨道类型
    pub fn kind(&self) -> TrackKind {
        self.kind
    }

    /// 编解码类型
    pub fn codec(&self) -> CodecType {
        self.codec
    }

    /// SSRC
    pub fn ssrc(&self) -> u32 {
        self.ssrc
    }
}

impl std::fmt::Debug for RtpTrack {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("RtpTrack")
            .field("id", &self.id)
            .field("kind", &self.kind)
            .field("codec", &self.codec)
            .field("ssrc", &self.ssrc)
            .finish()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use bytes::Bytes;

    #[test]
    fn test_rtp_track_creation() {
        let track = RtpTrack::new("audio-1", TrackKind::Audio, CodecType::Opus, 12345);
        assert_eq!(track.id(), "audio-1");
        assert_eq!(track.kind(), TrackKind::Audio);
        assert_eq!(track.codec(), CodecType::Opus);
        assert_eq!(track.ssrc(), 12345);
    }

    #[tokio::test]
    async fn test_rtp_track_send() {
        let track = RtpTrack::new("video-1", TrackKind::Video, CodecType::Vp8, 999);
        let pkt = RtpPacket {
            ssrc: 999,
            payload_type: 96,
            sequence_number: 1,
            timestamp: 90000,
            marker: true,
            payload: Bytes::from_static(b"vp8frame"),
            rid: String::new(),
        };
        // 发送应成功（即使没有接收端消费，channel 有缓冲）
        track.send_rtp(&pkt).await.expect("发送 RTP");
    }
}
