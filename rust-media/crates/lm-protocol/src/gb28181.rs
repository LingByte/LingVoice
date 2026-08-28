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

            let start_code = u32::from_be_bytes([
                data[offset],
                data[offset + 1],
                data[offset + 2],
                data[offset + 3],
            ]);

            if start_code == 0x000001BA {
                // PS pack header (MPEG-PS)
                // 结构：
                //   4 bytes: start code (0x000001BA)
                //   6 bytes: SCR + marker bits
                //   3 bytes: mux_rate + marker bits
                //   1 byte:  stuffing_length (低 3 位) + reserved (高 5 位 = 0xFF)
                //   N bytes: stuffing bytes (0xFF * stuffing_length)
                // 总长度 = 14 + stuffing_length
                if offset + 14 > data.len() {
                    break;
                }
                let stuffing_length = (data[offset + 13] & 0x07) as usize;
                if offset + 14 + stuffing_length > data.len() {
                    break;
                }
                offset += 14 + stuffing_length;
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
                let pes_payload_end = std::cmp::min(
                    pes_payload_start + pes_length.saturating_sub(3 + pes_header_length),
                    data.len(),
                );

                if pes_payload_start < pes_payload_end {
                    // PES payload 是 H264 NALU 数据
                    let pes_data = &data[pes_payload_start..pes_payload_end];

                    // 简化：直接作为一帧
                    let keyframe = pes_data
                        .windows(5)
                        .any(|w| w[0..4] == [0, 0, 0, 1] && (w[4] & 0x1f) == 5);
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

// ============================================================================
// SIP 消息编解码 (GB28181 信令层)
// ============================================================================

/// SIP 请求方法
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SipMethod {
    Register,
    Invite,
    Ack,
    Bye,
    Cancel,
    Options,
    Message,
    Notify,
    Subscribe,
}

impl SipMethod {
    fn as_str(&self) -> &'static str {
        match self {
            SipMethod::Register => "REGISTER",
            SipMethod::Invite => "INVITE",
            SipMethod::Ack => "ACK",
            SipMethod::Bye => "BYE",
            SipMethod::Cancel => "CANCEL",
            SipMethod::Options => "OPTIONS",
            SipMethod::Message => "MESSAGE",
            SipMethod::Notify => "NOTIFY",
            SipMethod::Subscribe => "SUBSCRIBE",
        }
    }

    fn from_str(s: &str) -> Option<Self> {
        match s {
            "REGISTER" => Some(SipMethod::Register),
            "INVITE" => Some(SipMethod::Invite),
            "ACK" => Some(SipMethod::Ack),
            "BYE" => Some(SipMethod::Bye),
            "CANCEL" => Some(SipMethod::Cancel),
            "OPTIONS" => Some(SipMethod::Options),
            "MESSAGE" => Some(SipMethod::Message),
            "NOTIFY" => Some(SipMethod::Notify),
            "SUBSCRIBE" => Some(SipMethod::Subscribe),
            _ => None,
        }
    }
}

/// SIP 消息类型
#[derive(Debug, Clone)]
pub enum SipMessage {
    Request {
        method: SipMethod,
        uri: String,
        headers: Vec<(String, String)>,
        body: String,
    },
    Response {
        status_code: u16,
        status_text: String,
        headers: Vec<(String, String)>,
        body: String,
    },
}

impl SipMessage {
    /// 构建 SIP REGISTER 请求
    pub fn register(
        from: &str,
        to: &str,
        contact: &str,
        call_id: &str,
        cseq: u32,
        expires: u32,
    ) -> Self {
        let headers = vec![
            ("From".to_string(), format!("<sip:{}>;tag=reg", from)),
            ("To".to_string(), format!("<sip:{}>", to)),
            ("Contact".to_string(), format!("<sip:{}>", contact)),
            ("Call-ID".to_string(), call_id.to_string()),
            ("CSeq".to_string(), format!("{} REGISTER", cseq)),
            ("Expires".to_string(), expires.to_string()),
            ("Max-Forwards".to_string(), "70".to_string()),
            ("User-Agent".to_string(), "LingVoice/1.0".to_string()),
        ];
        SipMessage::Request {
            method: SipMethod::Register,
            uri: format!("sip:{}", to),
            headers,
            body: String::new(),
        }
    }

    /// 构建 SIP INVITE 请求 (GB28181 媒体邀请)
    pub fn invite(
        from: &str,
        to: &str,
        contact: &str,
        call_id: &str,
        cseq: u32,
        sdp: &str,
    ) -> Self {
        let headers = vec![
            ("From".to_string(), format!("<sip:{}>;tag=inv", from)),
            ("To".to_string(), format!("<sip:{}>", to)),
            ("Contact".to_string(), format!("<sip:{}>", contact)),
            ("Call-ID".to_string(), call_id.to_string()),
            ("CSeq".to_string(), format!("{} INVITE", cseq)),
            ("Content-Type".to_string(), "application/sdp".to_string()),
            ("Max-Forwards".to_string(), "70".to_string()),
            ("User-Agent".to_string(), "LingVoice/1.0".to_string()),
        ];
        SipMessage::Request {
            method: SipMethod::Invite,
            uri: format!("sip:{}", to),
            headers,
            body: sdp.to_string(),
        }
    }

    /// 构建 SIP BYE 请求
    pub fn bye(from: &str, to: &str, call_id: &str, cseq: u32) -> Self {
        let headers = vec![
            ("From".to_string(), format!("<sip:{}>;tag=bye", from)),
            ("To".to_string(), format!("<sip:{}>", to)),
            ("Call-ID".to_string(), call_id.to_string()),
            ("CSeq".to_string(), format!("{} BYE", cseq)),
            ("Max-Forwards".to_string(), "70".to_string()),
        ];
        SipMessage::Request {
            method: SipMethod::Bye,
            uri: format!("sip:{}", to),
            headers,
            body: String::new(),
        }
    }

    /// 构建 SIP 200 OK 响应
    pub fn ok(request: &SipMessage, contact: &str) -> Self {
        let headers = match request {
            SipMessage::Request {
                headers: req_headers,
                ..
            } => {
                let mut h: Vec<(String, String)> = vec![
                    (
                        "Via".to_string(),
                        get_header(req_headers, "Via").unwrap_or_default(),
                    ),
                    (
                        "From".to_string(),
                        get_header(req_headers, "From").unwrap_or_default(),
                    ),
                    (
                        "To".to_string(),
                        get_header(req_headers, "To").unwrap_or_default(),
                    ),
                    (
                        "Call-ID".to_string(),
                        get_header(req_headers, "Call-ID").unwrap_or_default(),
                    ),
                    (
                        "CSeq".to_string(),
                        get_header(req_headers, "CSeq").unwrap_or_default(),
                    ),
                    ("Contact".to_string(), format!("<sip:{}>", contact)),
                    ("User-Agent".to_string(), "LingVoice/1.0".to_string()),
                ];
                // 如果请求有 Content-Type，响应也加上
                if let Some(ct) = get_header(req_headers, "Content-Type") {
                    h.push(("Content-Type".to_string(), ct));
                }
                h
            }
            _ => Vec::new(),
        };
        SipMessage::Response {
            status_code: 200,
            status_text: "OK".to_string(),
            headers,
            body: String::new(),
        }
    }

    /// 序列化为 SIP 文本格式
    pub fn to_string(&self) -> String {
        let mut output = String::new();
        match self {
            SipMessage::Request {
                method,
                uri,
                headers,
                body,
            } => {
                output.push_str(&format!("{} sip:{} SIP/2.0\r\n", method.as_str(), uri));
                for (key, value) in headers {
                    output.push_str(&format!("{}: {}\r\n", key, value));
                }
                if !body.is_empty() {
                    output.push_str(&format!("Content-Length: {}\r\n", body.len()));
                } else {
                    output.push_str("Content-Length: 0\r\n");
                }
                output.push_str("\r\n");
                if !body.is_empty() {
                    output.push_str(body);
                }
            }
            SipMessage::Response {
                status_code,
                status_text,
                headers,
                body,
            } => {
                output.push_str(&format!("SIP/2.0 {} {}\r\n", status_code, status_text));
                for (key, value) in headers {
                    output.push_str(&format!("{}: {}\r\n", key, value));
                }
                if !body.is_empty() {
                    output.push_str(&format!("Content-Length: {}\r\n", body.len()));
                } else {
                    output.push_str("Content-Length: 0\r\n");
                }
                output.push_str("\r\n");
                if !body.is_empty() {
                    output.push_str(body);
                }
            }
        }
        output
    }

    /// 从 SIP 文本解析
    pub fn parse(data: &str) -> Option<Self> {
        let lines: Vec<&str> = data.split("\r\n").collect();
        if lines.is_empty() {
            return None;
        }

        let first_line = lines[0];

        if first_line.starts_with("SIP/2.0 ") {
            // Response
            let parts: Vec<&str> = first_line.splitn(3, ' ').collect();
            if parts.len() < 3 {
                return None;
            }
            let status_code: u16 = parts[1].parse().ok()?;
            let status_text = parts[2].to_string();

            let (headers, body) = parse_headers_and_body(&lines[1..]);
            Some(SipMessage::Response {
                status_code,
                status_text,
                headers,
                body,
            })
        } else {
            // Request
            let parts: Vec<&str> = first_line.splitn(3, ' ').collect();
            if parts.len() < 3 {
                return None;
            }
            let method = SipMethod::from_str(parts[0])?;
            let uri = parts[1]
                .strip_prefix("sip:")
                .unwrap_or(parts[1])
                .to_string();

            let (headers, body) = parse_headers_and_body(&lines[1..]);
            Some(SipMessage::Request {
                method,
                uri,
                headers,
                body,
            })
        }
    }
}

/// 从 header lines 解析 headers 和 body
fn parse_headers_and_body(lines: &[&str]) -> (Vec<(String, String)>, String) {
    let mut headers = Vec::new();
    let mut body_start = lines.len();
    for (i, line) in lines.iter().enumerate() {
        if line.is_empty() {
            body_start = i + 1;
            break;
        }
        if let Some(pos) = line.find(':') {
            let key = line[..pos].trim().to_string();
            let value = line[pos + 1..].trim().to_string();
            headers.push((key, value));
        }
    }
    let body = if body_start < lines.len() {
        lines[body_start..].join("\r\n")
    } else {
        String::new()
    };
    (headers, body)
}

/// 获取 header 值
fn get_header(headers: &[(String, String)], name: &str) -> Option<String> {
    headers
        .iter()
        .find(|(k, _)| k.eq_ignore_ascii_case(name))
        .map(|(_, v)| v.clone())
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
        assert_eq!(session.media_port, 9000);
        assert_eq!(session.ssrc, 0x12345678);
    }

    #[test]
    fn test_gb28181_session_clone() {
        let session = Gb28181Session {
            device_id: "34020000001320000001".into(),
            sip_server: "127.0.0.1:5060".into(),
            media_port: 9000,
            ssrc: 0x12345678,
        };
        let cloned = session.clone();
        assert_eq!(session.device_id, cloned.device_id);
        assert_eq!(session.ssrc, cloned.ssrc);
    }

    #[test]
    fn test_gb28181_demuxer_new() {
        let demuxer = Gb28181Demuxer::new();
        assert_eq!(demuxer.protocol(), Protocol::Gb28181);
        assert_eq!(demuxer.ssrc, 0);
    }

    #[test]
    fn test_gb28181_demuxer_set_ssrc() {
        let mut demuxer = Gb28181Demuxer::new();
        demuxer.set_ssrc(0xAABBCCDD);
        assert_eq!(demuxer.ssrc, 0xAABBCCDD);
    }

    #[test]
    fn test_gb28181_demuxer_short_data() {
        let mut demuxer = Gb28181Demuxer::new();
        // Too short for RTP header (< 12 bytes)
        let frames = demuxer.push_data(&[0, 1, 2, 3]);
        assert!(frames.is_empty());
    }

    #[test]
    fn test_gb28181_demuxer_no_marker() {
        let mut demuxer = Gb28181Demuxer::new();
        // RTP header with marker=0 → should buffer, not produce frames
        let mut rtp = vec![0x80, 0x60]; // V=2, M=0, PT=96
        rtp.extend_from_slice(&0u16.to_be_bytes()); // seq
        rtp.extend_from_slice(&9000u32.to_be_bytes()); // timestamp
        rtp.extend_from_slice(&0x12345678u32.to_be_bytes()); // ssrc
        rtp.extend_from_slice(&[0xAB, 0xCD]); // payload
        let frames = demuxer.push_data(&rtp);
        assert!(frames.is_empty()); // no marker → no frames yet
    }

    #[test]
    fn test_gb28181_demuxer_with_marker_empty_ps() {
        let mut demuxer = Gb28181Demuxer::new();
        // RTP with marker=1 but empty PS payload
        let mut rtp = vec![0x80, 0xE0]; // V=2, M=1, PT=96
        rtp.extend_from_slice(&0u16.to_be_bytes());
        rtp.extend_from_slice(&9000u32.to_be_bytes());
        rtp.extend_from_slice(&0u32.to_be_bytes());
        // no payload
        let frames = demuxer.push_data(&rtp);
        // Empty PS → no frames
        assert!(frames.is_empty());
    }

    #[test]
    fn test_gb28181_demuxer_reset() {
        let mut demuxer = Gb28181Demuxer::new();
        demuxer.set_ssrc(0x12345678);

        // Push some data to fill buffer
        let mut rtp = vec![0x80, 0x60]; // M=0
        rtp.extend_from_slice(&0u16.to_be_bytes());
        rtp.extend_from_slice(&0u32.to_be_bytes());
        rtp.extend_from_slice(&0u32.to_be_bytes());
        rtp.extend_from_slice(&[0xFF; 10]);
        demuxer.push_data(&rtp);

        demuxer.reset();
        assert_eq!(demuxer.ssrc, 0);
        assert!(demuxer.ps_buffer.is_empty());
    }

    #[test]
    fn test_gb28181_demuxer_ssrc_learning() {
        let mut demuxer = Gb28181Demuxer::new();
        assert_eq!(demuxer.ssrc, 0);

        // Push RTP packet with ssrc=12345, marker=0
        let mut rtp = vec![0x80, 0x60];
        rtp.extend_from_slice(&0u16.to_be_bytes());
        rtp.extend_from_slice(&0u32.to_be_bytes());
        rtp.extend_from_slice(&12345u32.to_be_bytes());
        rtp.extend_from_slice(&[0xFF]);
        demuxer.push_data(&rtp);

        // SSRC should be learned
        assert_eq!(demuxer.ssrc, 12345);
    }

    #[test]
    fn test_gb28181_protocol() {
        let demuxer = Gb28181Demuxer::new();
        assert_eq!(demuxer.protocol(), Protocol::Gb28181);
    }

    #[test]
    fn test_gb28181_demuxer_default() {
        let demuxer = Gb28181Demuxer::default();
        assert_eq!(demuxer.protocol(), Protocol::Gb28181);
    }

    /// 构造一个最小的 PS pack header（14 + stuffing_length bytes）
    /// - 4 bytes: start code 0x000001BA
    /// - 6 bytes: SCR + marker bits（填 0）
    /// - 3 bytes: mux_rate + marker bits（填 0）
    /// - 1 byte:  0xF8 | stuffing_length（高 5 位 reserved=0xFF，低 3 位 stuffing_length）
    /// - N bytes: stuffing bytes（0xFF * stuffing_length）
    fn make_pack_header(stuffing_length: u8) -> Vec<u8> {
        let mut hdr = vec![0x00, 0x00, 0x01, 0xBA]; // start code
        hdr.extend_from_slice(&[0x00; 6]); // SCR + marker bits
        hdr.extend_from_slice(&[0x00; 3]); // mux_rate + marker bits
        hdr.push(0xF8 | (stuffing_length & 0x07)); // stuffing_length field
        hdr.extend(std::iter::repeat(0xFF).take(stuffing_length as usize)); // stuffing bytes
        hdr
    }

    #[test]
    fn test_ps_pack_header_stuffing_length_zero() {
        // stuffing_length = 0 → pack header 恰好 14 bytes
        let mut demuxer = Gb28181Demuxer::new();
        let mut ps = make_pack_header(0);
        // 紧跟一个 system header (0x000001BB) 验证 offset 正确
        ps.extend_from_slice(&[0x00, 0x00, 0x01, 0xBB, 0x00, 0x00]); // length=0
        let rtp = wrap_ps_as_rtp(&ps, true);
        let frames = demuxer.push_data(&rtp);
        // 没有 PES → 无帧，但不应 panic / 越界
        assert!(frames.is_empty());
    }

    #[test]
    fn test_ps_pack_header_stuffing_length_nonzero() {
        // stuffing_length = 5 → pack header 14 + 5 = 19 bytes
        let mut demuxer = Gb28181Demuxer::new();
        let mut ps = make_pack_header(5);
        // 紧跟一个 system header 验证 offset 正确跳过 stuffing bytes
        ps.extend_from_slice(&[0x00, 0x00, 0x01, 0xBB, 0x00, 0x00]); // length=0
        let rtp = wrap_ps_as_rtp(&ps, true);
        let frames = demuxer.push_data(&rtp);
        assert!(frames.is_empty());
    }

    #[test]
    fn test_ps_pack_header_stuffing_length_max() {
        // stuffing_length = 7（低 3 位最大值）→ pack header 14 + 7 = 21 bytes
        let mut demuxer = Gb28181Demuxer::new();
        let mut ps = make_pack_header(7);
        ps.extend_from_slice(&[0x00, 0x00, 0x01, 0xBB, 0x00, 0x00]); // length=0
        let rtp = wrap_ps_as_rtp(&ps, true);
        let frames = demuxer.push_data(&rtp);
        assert!(frames.is_empty());
    }

    #[test]
    fn test_ps_pack_header_stuffing_length_with_pes() {
        // stuffing_length = 3，后面跟一个含 payload 的 PES 包
        // 验证 stuffing 被正确跳过，能解析到 PES payload
        let mut demuxer = Gb28181Demuxer::new();
        let mut ps = make_pack_header(3);

        // PES packet: start code 0x000001E0
        // payload: [0,0,0,1, 0x65] = H264 IDR slice (NAL type 5 → keyframe)
        let pes_payload = [0x00, 0x00, 0x00, 0x01, 0x65];
        // PES header: 2 bytes flags + 1 byte PES_header_data_length=0
        let pes_total_len = 3 + 0 + pes_payload.len(); // PES_packet_length 字段值
        ps.extend_from_slice(&[0x00, 0x00, 0x01, 0xE0]); // start code
        ps.extend_from_slice(&(pes_total_len as u16).to_be_bytes()); // PES_packet_length
        ps.push(0x80); // flags byte 1
        ps.push(0x00); // flags byte 2
        ps.push(0x00); // PES_header_data_length = 0
        ps.extend_from_slice(&pes_payload);

        let rtp = wrap_ps_as_rtp(&ps, true);
        let frames = demuxer.push_data(&rtp);
        assert_eq!(frames.len(), 1);
        assert!(frames[0].keyframe);
    }

    /// 将 PS 数据包装成 RTP 包（marker 可控）
    fn wrap_ps_as_rtp(ps: &[u8], marker: bool) -> Vec<u8> {
        let mut rtp = vec![0x80, if marker { 0xE0 } else { 0x60 }]; // V=2, PT=96
        rtp.extend_from_slice(&0u16.to_be_bytes()); // seq
        rtp.extend_from_slice(&9000u32.to_be_bytes()); // timestamp
        rtp.extend_from_slice(&0x12345678u32.to_be_bytes()); // ssrc
        rtp.extend_from_slice(ps);
        rtp
    }
}
