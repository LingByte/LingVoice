//! RTMP 推流客户端 — 完整的推流输出实现
//!
//! 实现 RTMP 客户端协议栈：
//! 1. Handshake (C0/C1 → S0/S1/S2 → C2)
//! 2. Chunk stream 协商 (SetChunkSize, WindowAckSize, SetPeerBandwidth)
//! 3. AMF0 命令: connect → createStream → publish
//! 4. Media message 推流 (audio/video)
//!
//! 用法：
//! ```ignore
//! let mut client = RtmpClient::new("rtmp://server:1935/live/stream1");
//! client.connect().await?;
//! client.publish().await?;
//! client.send_frame(&frame).await?;
//! client.close().await?;
//! ```

use anyhow::{anyhow, Result};
use lm_core::{CodecType, MediaFrame, TrackKind};
use std::time::{SystemTime, UNIX_EPOCH};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;

/// RTMP 客户端
pub struct RtmpClient {
    /// 目标 URL: rtmp://host:port/app/stream
    url: String,
    /// TCP 连接
    stream: Option<TcpStream>,
    /// chunk size
    chunk_size: u32,
    /// 当前时间戳基准
    first_timestamp: Option<u32>,
    /// 是否已发送 metadata
    metadata_sent: bool,
    /// 是否已 publish
    publishing: bool,
    /// stream id (from createStream response)
    stream_id: f64,
    /// transaction id
    transaction_id: f64,
}

impl RtmpClient {
    /// 创建 RTMP 客户端
    /// url: rtmp://host:port/app/stream
    pub fn new(url: &str) -> Self {
        Self {
            url: url.to_string(),
            stream: None,
            chunk_size: 4096,
            first_timestamp: None,
            metadata_sent: false,
            publishing: false,
            stream_id: 1.0,
            transaction_id: 1.0,
        }
    }

    /// 连接到 RTMP 服务器并完成握手 + connect
    pub async fn connect(&mut self) -> Result<()> {
        let (host, port, _app, _stream) = parse_rtmp_url(&self.url)?;

        // TCP 连接
        let addr = format!("{host}:{port}");
        let stream = TcpStream::connect(&addr)
            .await
            .map_err(|e| anyhow!("connect {addr}: {e}"))?;
        self.stream = Some(stream);

        // RTMP Handshake
        self.handshake().await?;

        // 协商 chunk size
        self.send_set_chunk_size(4096).await?;
        self.send_window_ack_size(2500000).await?;

        // connect 命令
        self.send_connect().await?;

        Ok(())
    }

    /// 开始推流 (createStream + publish)
    pub async fn publish(&mut self) -> Result<()> {
        if self.stream.is_none() {
            return Err(anyhow!("not connected"));
        }

        // createStream
        self.send_create_stream().await?;

        // publish
        let (_host, _port, _app, stream_key) = parse_rtmp_url(&self.url)?;
        self.send_publish(&stream_key).await?;

        self.publishing = true;
        Ok(())
    }

    /// 发送一帧媒体数据
    pub async fn send_frame(&mut self, frame: &MediaFrame) -> Result<()> {
        if !self.publishing {
            return Err(anyhow!("not publishing"));
        }

        if self.first_timestamp.is_none() {
            self.first_timestamp = Some(frame.timestamp);
        }

        // 发送 metadata（第一帧时）
        if !self.metadata_sent {
            self.send_metadata(frame.codec).await?;
            self.metadata_sent = true;
        }

        // 构造 RTMP payload
        let timestamp_ms = if frame.kind == TrackKind::Video {
            frame.timestamp / 90
        } else {
            frame.timestamp / 48
        };

        let (msg_type, cs_id, payload) = match frame.kind {
            TrackKind::Video => {
                let frame_type = if frame.keyframe { 1u8 } else { 2u8 };
                let codec_id = match frame.codec {
                    CodecType::H264 => 7u8,
                    _ => 7u8,
                };
                let mut p = vec![(frame_type << 4) | codec_id];
                if frame.codec == CodecType::H264 {
                    p.push(0x01); // AVCPacketType = NALU
                    p.extend_from_slice(&[0, 0, 0]); // CompositionTime
                }
                p.extend_from_slice(&frame.data);
                (0x09u8, 6u32, p)
            }
            TrackKind::Audio => {
                let codec_id = match frame.codec {
                    CodecType::Aac => 10u8,
                    CodecType::Opus => 13u8,
                    CodecType::PcmU => 8u8,
                    CodecType::PcmA => 7u8,
                    _ => 13u8,
                };
                let mut p = vec![(codec_id << 4) | 0x0f];
                if frame.codec == CodecType::Aac {
                    p.push(0x01); // AACPacketType = raw
                }
                p.extend_from_slice(&frame.data);
                (0x08u8, 7u32, p)
            }
        };

        // 封装 chunk 并发送
        let chunk = create_chunk_type0(cs_id, timestamp_ms, msg_type, self.stream_id as u32, &payload, self.chunk_size);
        self.write_all(&chunk).await?;

        Ok(())
    }

    /// 关闭连接
    pub async fn close(&mut self) -> Result<()> {
        if let Some(mut stream) = self.stream.take() {
            let _ = stream.shutdown().await;
        }
        self.publishing = false;
        Ok(())
    }

    // ─── Handshake ──────────────────────────────────────────────────

    async fn handshake(&mut self) -> Result<()> {
        let stream = self.stream.as_mut().unwrap();

        // C0 + C1
        let mut c0c1 = vec![0u8; 1 + 1536];
        c0c1[0] = 0x03; // version
        // C1: time (4) + zero (4) + random (1528)
        let now = SystemTime::now().duration_since(UNIX_EPOCH).unwrap_or_default().as_secs() as u32;
        c0c1[1..5].copy_from_slice(&now.to_be_bytes());
        // random bytes
        for i in 5..1537 {
            c0c1[i] = (i % 256) as u8;
        }
        stream.write_all(&c0c1).await?;

        // S0 + S1 + S2
        let mut s0s1s2 = vec![0u8; 1 + 1536 + 1536];
        stream.read_exact(&mut s0s1s2).await?;

        if s0s1s2[0] != 0x03 {
            return Err(anyhow!("invalid RTMP version: {}", s0s1s2[0]));
        }

        // C2 = S1
        let c2 = &s0s1s2[1..1537];
        stream.write_all(c2).await?;

        Ok(())
    }

    // ─── Chunk 协商 ─────────────────────────────────────────────────

    async fn send_set_chunk_size(&mut self, size: u32) -> Result<()> {
        let mut payload = size.to_be_bytes().to_vec();
        let chunk = create_chunk_type0(2, 0, 1, 0, &payload, self.chunk_size);
        self.write_all(&chunk).await?;
        self.chunk_size = size;
        Ok(())
    }

    async fn send_window_ack_size(&mut self, size: u32) -> Result<()> {
        let payload = size.to_be_bytes().to_vec();
        let chunk = create_chunk_type0(2, 0, 5, 0, &payload, self.chunk_size);
        self.write_all(&chunk).await?;
        Ok(())
    }

    // ─── AMF0 命令 ──────────────────────────────────────────────────

    async fn send_connect(&mut self) -> Result<()> {
        let (_host, _port, app, _stream) = parse_rtmp_url(&self.url)?;

        let mut amf = Vec::new();
        // command name
        amf_extend_string(&mut amf, "connect");
        // transaction id = 1
        amf_extend_number(&mut amf, 1.0);
        // command object
        amf_push_object_start(&mut amf);
        amf_push_string_kv(&mut amf, "app", &app);
        amf_push_string_kv(&mut amf, "flashVer", "FMLE/3.0");
        amf_push_string_kv(&mut amf, "tcUrl", &format!("rtmp://{}/{}", _host_port(&self.url), app));
        amf_push_bool_kv(&mut amf, "fpad", false);
        amf_push_number_kv(&mut amf, "capabilities", 15.0);
        amf_push_object_end(&mut amf);

        let chunk = create_chunk_type0(3, 0, 20, 0, &amf, self.chunk_size);
        self.write_all(&chunk).await?;
        Ok(())
    }

    async fn send_create_stream(&mut self) -> Result<()> {
        self.transaction_id += 1.0;
        let tid = self.transaction_id;

        let mut amf = Vec::new();
        amf_extend_string(&mut amf, "createStream");
        amf_extend_number(&mut amf, tid);
        amf_push_null(&mut amf);

        let chunk = create_chunk_type0(3, 0, 20, 0, &amf, self.chunk_size);
        self.write_all(&chunk).await?;

        // 简化：不等待响应，使用默认 stream_id=1
        self.stream_id = 1.0;
        Ok(())
    }

    async fn send_publish(&mut self, stream_key: &str) -> Result<()> {
        let mut amf = Vec::new();
        amf_extend_string(&mut amf, "publish");
        amf_extend_number(&mut amf, 0.0);
        amf_push_null(&mut amf);
        amf_extend_string(&mut amf, stream_key);
        amf_extend_string(&mut amf, "live");

        let chunk = create_chunk_type0(3, 0, 20, 0, &amf, self.chunk_size);
        self.write_all(&chunk).await?;
        Ok(())
    }

    async fn send_metadata(&mut self, _codec: CodecType) -> Result<()> {
        let mut amf = Vec::new();
        amf_extend_string(&mut amf, "@setDataFrame");
        amf_extend_string(&mut amf, "onMetaData");
        amf_push_object_start(&mut amf);
        amf_push_number_kv(&mut amf, "duration", 0.0);
        amf_push_number_kv(&mut amf, "width", 1280.0);
        amf_push_number_kv(&mut amf, "height", 720.0);
        amf_push_number_kv(&mut amf, "framerate", 30.0);
        amf_push_number_kv(&mut amf, "videodatarate", 1500.0);
        amf_push_number_kv(&mut amf, "audiosamplerate", 48000.0);
        amf_push_number_kv(&mut amf, "audiochannels", 1.0);
        amf_push_object_end(&mut amf);

        let chunk = create_chunk_type0(4, 0, 20, 1, &amf, self.chunk_size);
        self.write_all(&chunk).await?;
        Ok(())
    }

    // ─── IO ─────────────────────────────────────────────────────────

    async fn write_all(&mut self, data: &[u8]) -> Result<()> {
        if let Some(stream) = self.stream.as_mut() {
            stream.write_all(data).await?;
            Ok(())
        } else {
            Err(anyhow!("not connected"))
        }
    }
}

// ============================================================================
// 辅助函数
// ============================================================================

/// 解析 RTMP URL: rtmp://host:port/app/stream
fn parse_rtmp_url(url: &str) -> Result<(String, u16, String, String)> {
    let url = url.strip_prefix("rtmp://").ok_or_else(|| anyhow!("invalid RTMP URL"))?;
    let (host_port, path) = url.split_once('/').unwrap_or((url, ""));
    let (host, port) = if let Some((h, p)) = host_port.split_once(':') {
        (h.to_string(), p.parse::<u16>().unwrap_or(1935))
    } else {
        (host_port.to_string(), 1935u16)
    };
    let parts: Vec<&str> = path.splitn(2, '/').collect();
    let app = parts.first().unwrap_or(&"live").to_string();
    let stream = parts.get(1).unwrap_or(&"stream").to_string();
    Ok((host, port, app, stream))
}

fn _host_port(url: &str) -> String {
    url.strip_prefix("rtmp://").unwrap_or(url).split('/').next().unwrap_or("").to_string()
}

/// 创建 Type0 chunk
fn create_chunk_type0(cs_id: u32, timestamp: u32, msg_type: u8, stream_id: u32, payload: &[u8], chunk_size: u32) -> Vec<u8> {
    let mut chunk = Vec::new();

    // Basic header: type0, cs_id
    chunk.push((0u8 << 6) | (cs_id as u8 & 0x3f));

    // Message header (11 bytes)
    chunk.push((timestamp >> 16) as u8);
    chunk.push((timestamp >> 8) as u8);
    chunk.push(timestamp as u8);
    let msg_len = payload.len() as u32;
    chunk.push((msg_len >> 16) as u8);
    chunk.push((msg_len >> 8) as u8);
    chunk.push(msg_len as u8);
    chunk.push(msg_type);
    chunk.extend_from_slice(&stream_id.to_le_bytes());

    // Chunk data
    let cs = chunk_size as usize;
    let first_chunk_size = std::cmp::min(payload.len(), cs);
    chunk.extend_from_slice(&payload[..first_chunk_size]);

    // Type3 continuation chunks
    let mut offset = first_chunk_size;
    while offset < payload.len() {
        chunk.push((3u8 << 6) | (cs_id as u8 & 0x3f));
        let remaining = std::cmp::min(payload.len() - offset, cs);
        chunk.extend_from_slice(&payload[offset..offset + remaining]);
        offset += remaining;
    }

    chunk
}

// ─── AMF0 编码 ──────────────────────────────────────────────────────

fn amf_extend_string(data: &mut Vec<u8>, s: &str) {
    data.push(0x02);
    let len = s.len() as u16;
    data.extend_from_slice(&len.to_be_bytes());
    data.extend_from_slice(s.as_bytes());
}

fn amf_extend_number(data: &mut Vec<u8>, n: f64) {
    data.push(0x00);
    data.extend_from_slice(&n.to_be_bytes());
}

fn amf_push_object_start(data: &mut Vec<u8>) {
    data.push(0x03);
}

fn amf_push_object_end(data: &mut Vec<u8>) {
    data.push(0x00);
    data.push(0x00);
    data.push(0x09);
}

fn amf_push_string_kv(data: &mut Vec<u8>, key: &str, val: &str) {
    let klen = key.len() as u16;
    data.extend_from_slice(&klen.to_be_bytes());
    data.extend_from_slice(key.as_bytes());
    amf_extend_string(data, val);
}

fn amf_push_number_kv(data: &mut Vec<u8>, key: &str, val: f64) {
    let klen = key.len() as u16;
    data.extend_from_slice(&klen.to_be_bytes());
    data.extend_from_slice(key.as_bytes());
    amf_extend_number(data, val);
}

fn amf_push_bool_kv(data: &mut Vec<u8>, key: &str, val: bool) {
    let klen = key.len() as u16;
    data.extend_from_slice(&klen.to_be_bytes());
    data.extend_from_slice(key.as_bytes());
    data.push(0x01);
    data.push(if val { 1 } else { 0 });
}

fn amf_push_null(data: &mut Vec<u8>) {
    data.push(0x05);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_rtmp_url() {
        let (host, port, app, stream) = parse_rtmp_url("rtmp://localhost:1935/live/stream1").unwrap();
        assert_eq!(host, "localhost");
        assert_eq!(port, 1935);
        assert_eq!(app, "live");
        assert_eq!(stream, "stream1");
    }

    #[test]
    fn test_parse_rtmp_url_no_port() {
        let (host, port, app, stream) = parse_rtmp_url("rtmp://example.com/app/key").unwrap();
        assert_eq!(host, "example.com");
        assert_eq!(port, 1935);
        assert_eq!(app, "app");
        assert_eq!(stream, "key");
    }

    #[test]
    fn test_create_chunk_type0() {
        let chunk = create_chunk_type0(3, 1000, 20, 1, &[0x02, 0x00, 0x03], 4096);
        // Basic header: type0, cs_id=3 → 0x03
        assert_eq!(chunk[0], 0x03);
        // timestamp 1000 = 0x003E8
        assert_eq!(chunk[1], 0x00);
        assert_eq!(chunk[2], 0x03);
        assert_eq!(chunk[3], 0xE8);
    }

    #[test]
    fn test_create_chunk_large_payload() {
        let payload = vec![0xAB; 5000];
        let chunk = create_chunk_type0(6, 0, 9, 1, &payload, 4096);
        // 应该有 Type3 continuation
        // 第一个 chunk: 1 (basic) + 11 (header) + 4096 (data) = 4108
        // Type3: 1 (basic) + 904 (data) = 905
        assert!(chunk.len() > 5000);
    }

    #[test]
    fn test_amf0_encode() {
        let mut data = Vec::new();
        amf_extend_string(&mut data, "connect");
        assert_eq!(data[0], 0x02); // string type
        assert_eq!(u16::from_be_bytes([data[1], data[2]]), 7); // length
    }

    #[test]
    fn test_amf0_number() {
        let mut data = Vec::new();
        amf_extend_number(&mut data, 1.0);
        assert_eq!(data[0], 0x00); // number type
        assert_eq!(data[1..9], 1.0f64.to_be_bytes());
    }

    #[test]
    fn test_rtmp_client_new() {
        let client = RtmpClient::new("rtmp://localhost:1935/live/test");
        assert_eq!(client.chunk_size, 4096);
        assert!(!client.publishing);
    }
}
