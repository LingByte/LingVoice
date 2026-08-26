//! GB28181 Demuxer — GB28181 摄像头协议
//!
//! GB28181 是中国安防监控标准，基于 SIP 信令 + RTP 传输。
//!
//! 协议结构：
//! - SIP 信令：REGISTER, INVITE, ACK, BYE
//! - 媒体：RTP over UDP，PS 流封装（MPEG-PS）
//!
//! 简化实现：SIP 信令由外部处理，demuxer 解析 PS 流中的 RTP。

use crate::{Demuxer, Protocol};
use lm_core::{CodecType, MediaFrame};
use lm_depacketizer::create_depacketizer;

/// GB28181 会话
#[derive(Debug, Clone)]
pub struct Gb28181Session {
    /// 设备 ID（20 位国标编码）
    pub device_id: String,
    /// SIP 服务器地址
    pub sip_server: String,
    /// 媒体接收端口
    pub media_port: u16,
    /// SSRC（国标 SSRC 格式）
    pub ssrc: u32,
}

/// GB28181 Demuxer
///
/// 解析 GB28181 PS 流（MPEG-PS over RTP）。
pub struct Gb28181Demuxer {
    /// 视频解包器（H264）
    video_depacketizer: Box<dyn lm_core::Depacketizer>,
    /// PS 流缓冲
    ps_buffer: Vec<u8>,
    /// 当前 SSRC
    ssrc: u32,
}

impl Gb28181Demuxer {
    pub fn new() -> Self {
        Self {
            video_depacketizer: create_depacketizer(CodecType::H264),
            ps_buffer: Vec::new(),
            ssrc: 0,
        }
    }

    /// 设置 SSRC
    pub fn set_ssrc(&mut self, ssrc: u32) {
        self.ssrc = ssrc;
    }

    /// 解析 MPEG-PS 包
    ///
    /// PS 流结构：
    /// - PS Header (pack_start_code 0x000001BA)
    /// - System Header (0x000001BB)
    /// - Program Stream Map (0x000001BC)
    /// - PES packets (0x000001E0-0x000001EF for video)
    fn parse_ps(&mut self, data: &[u8]) -> Vec<MediaFrame> {
        let mut frames = Vec::new();
        let mut offset = 0;

        while offset < data.len() {
            // 查找 pack start code
            if offset + 4 > data.len() {
                break;
            }

            let start_code = u32::from_be_bytes([data[offset], data[offset + 1], data[offset + 2], data[offset + 3]]);

            if start_code == 0x000001BA {
                // PS pack header
                // 简化：跳过 pack header（14 bytes minimum）
                if offset + 14 > data.len() {
                    break;
                }
                offset += 14; // 简化：固定 14 bytes
            } else if start_code == 0x000001BB {
                // System header
                if offset + 6 > data.len() {
                    break;
                }
                let length = u16::from_be_bytes([data[offset + 4], data[offset + 5]]) as usize;
                offset += 6 + length;
            } else if start_code == 0x000001BC {
                // Program Stream Map
                if offset + 6 > data.len() {
                    break;
                }
                let length = u16::from_be_bytes([data[offset + 4], data[offset + 5]]) as usize;
                offset += 6 + length;
            } else if (start_code & 0xFFFFFF_F0) == 0x000001E0 {
                // PES packet (video stream)
                if offset + 9 > data.len() {
                    break;
                }
                let pes_length = u16::from_be_bytes([data[offset + 4], data[offset + 5]]) as usize;
                let pes_header_length = data[offset + 8] as usize;

                if offset + 9 + pes_header_length > data.len() {
                    break;
                }

                let pes_payload_start = offset + 9 + pes_header_length;
                let pes_payload_end = std::cmp::min(pes_payload_start + pes_length.saturating_sub(3 + pes_header_length), data.len());

                if pes_payload_start < pes_payload_end {
                    // PES payload 是 H264 NALU 数据
                    let pes_data = &data[pes_payload_start..pes_payload_end];

                    // 简化：直接作为一帧
                    let keyframe = pes_data.windows(5).any(|w| w[0..4] == [0, 0, 0, 1] && (w[4] & 0x1f) == 5);
                    let frame = MediaFrame::video(
                        CodecType::H264,
                        0, // 时间戳从 PES 提取（简化）
                        bytes::Bytes::from(pes_data.to_vec()),
                        self.ssrc,
                        keyframe,
                    );
                    frames.push(frame);
                }

                offset = pes_payload_end;
            } else {
                offset += 1;
            }
        }

        frames
    }
}

impl Default for Gb28181Demuxer {
    fn default() -> Self {
        Self::new()
    }
}

impl Demuxer for Gb28181Demuxer {
    fn protocol(&self) -> Protocol {
        Protocol::Gb28181
    }

    fn push_data(&mut self, data: &[u8]) -> Vec<MediaFrame> {
        // GB28181 数据是 RTP 包，payload 是 PS 流
        if data.len() < 12 {
            return Vec::new();
        }

        // 解析 RTP header
        let timestamp = u32::from_be_bytes([data[4], data[5], data[6], data[7]]);
        let ssrc = u32::from_be_bytes([data[8], data[9], data[10], data[11]]);
        let marker = data[1] & 0x80 != 0;

        if self.ssrc == 0 {
            self.ssrc = ssrc;
        }

        let rtp_payload = &data[12..];

        // 累积 PS 流数据
        self.ps_buffer.extend_from_slice(rtp_payload);

        // marker=true 表示一帧 PS 数据完整
        if marker {
            let ps_data = std::mem::take(&mut self.ps_buffer);
            return self.parse_ps(&ps_data);
        }

        Vec::new()
    }

    fn reset(&mut self) {
        self.ps_buffer.clear();
        self.ssrc = 0;
        self.video_depacketizer.reset();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_gb28181_session() {
        let session = Gb28181Session {
            device_id: "34020000001320000001".into(),
            sip_server: "127.0.0.1:5060".into(),
            media_port: 9000,
            ssrc: 0x12345678,
        };
        assert_eq!(session.device_id.len(), 20);
    }
}
