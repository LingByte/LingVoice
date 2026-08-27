//! RTSP 推流客户端 — 完整的推流输出实现
//!
//! 实现 RTSP 客户端协议栈：
//! 1. ANNOUNCE (发送 SDP 描述)
//! 2. SETUP (建立 interleaved 通道)
//! 3. RECORD (开始推流)
//! 4. interleaved RTP 数据推送
//! 5. TEARDOWN (结束)
//!
//! 用法：
//! ```ignore
//! let mut client = RtspClient::new("rtsp://server:554/live/stream");
//! client.connect_and_announce(&sdp).await?;
//! client.setup_and_record().await?;
//! client.send_rtp(&rtp_data, channel).await?;
//! client.teardown().await?;
//! ```

use anyhow::{anyhow, Result};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::TcpStream;

/// RTSP 推流客户端
pub struct RtspClient {
    url: String,
    stream: Option<TcpStream>,
    /// CSeq 序列号
    cseq: u32,
    /// Session ID (from SETUP response)
    session_id: Option<String>,
    /// interleaved channel 映射: track → channel
    channel_map: Vec<u8>,
}

impl RtspClient {
    pub fn new(url: &str) -> Self {
        Self {
            url: url.to_string(),
            stream: None,
            cseq: 1,
            session_id: None,
            channel_map: Vec::new(),
        }
    }

    /// 连接并发送 ANNOUNCE
    pub async fn connect_and_announce(&mut self, sdp: &str) -> Result<()> {
        let (host, port, _path) = parse_rtsp_url(&self.url)?;

        let addr = format!("{host}:{port}");
        let stream = TcpStream::connect(&addr)
            .await
            .map_err(|e| anyhow!("connect {addr}: {e}"))?;
        self.stream = Some(stream);

        let url = self.url.clone();

        // 发送 OPTIONS
        let resp = self.send_request("OPTIONS", &url, &[], None).await?;
        if !resp.status.starts_with("200") {
            return Err(anyhow!("OPTIONS failed: {}", resp.status));
        }

        // 发送 ANNOUNCE
        let resp = self
            .send_request(
                "ANNOUNCE",
                &url,
                &[("Content-Type", "application/sdp")],
                Some(sdp.as_bytes()),
            )
            .await?;
        if !resp.status.starts_with("200") {
            return Err(anyhow!("ANNOUNCE failed: {}", resp.status));
        }

        Ok(())
    }

    /// SETUP + RECORD
    /// track_count: SDP 中的 track 数量
    pub async fn setup_and_record(&mut self, track_count: usize) -> Result<()> {
        let url = self.url.clone();
        // 为每个 track 发送 SETUP
        for i in 0..track_count {
            let track_url = format!("{url}/trackID={i}");
            let interleaved = format!("interleaved={}-{}", i * 2, i * 2 + 1);
            let transport = format!("RTP/AVP/TCP;unicast;{interleaved}");
            let resp = self
                .send_request("SETUP", &track_url, &[("Transport", &transport)], None)
                .await?;
            if !resp.status.starts_with("200") {
                return Err(anyhow!("SETUP track {i} failed: {}", resp.status));
            }
            // 提取 Session ID
            if let Some(sid) = resp.headers.iter().find_map(|(k, v)| {
                if k.eq_ignore_ascii_case("Session") {
                    Some(v.split(';').next().unwrap_or("").trim().to_string())
                } else {
                    None
                }
            }) {
                self.session_id = Some(sid);
            }
            self.channel_map.push((i * 2) as u8);
        }

        // 发送 RECORD
        let mut headers: Vec<(&str, &str)> = Vec::new();
        let sid_holder;
        if let Some(ref sid) = self.session_id {
            sid_holder = sid.clone();
            headers.push(("Session", &sid_holder));
        }
        let resp = self.send_request("RECORD", &url, &headers, None).await?;
        if !resp.status.starts_with("200") {
            return Err(anyhow!("RECORD failed: {}", resp.status));
        }

        Ok(())
    }

    /// 发送 interleaved RTP 数据
    /// channel: interleaved channel (0, 2, 4, ...)
    pub async fn send_rtp(&mut self, data: &[u8], channel: u8) -> Result<()> {
        if let Some(stream) = self.stream.as_mut() {
            // interleaved frame: '$' + channel + length(2 bytes BE) + data
            let mut frame = Vec::with_capacity(4 + data.len());
            frame.push(b'$');
            frame.push(channel);
            frame.extend_from_slice(&(data.len() as u16).to_be_bytes());
            frame.extend_from_slice(data);
            stream.write_all(&frame).await?;
            Ok(())
        } else {
            Err(anyhow!("not connected"))
        }
    }

    /// TEARDOWN
    pub async fn teardown(&mut self) -> Result<()> {
        if self.stream.is_some() {
            let url = self.url.clone();
            let mut headers: Vec<(&str, &str)> = Vec::new();
            let sid_holder;
            if let Some(ref sid) = self.session_id {
                sid_holder = sid.clone();
                headers.push(("Session", &sid_holder));
            }
            let _ = self.send_request("TEARDOWN", &url, &headers, None).await;
        }
        if let Some(mut stream) = self.stream.take() {
            let _ = stream.shutdown().await;
        }
        Ok(())
    }

    // ─── 内部 ───────────────────────────────────────────────────────

    async fn send_request(
        &mut self,
        method: &str,
        uri: &str,
        headers: &[(&str, &str)],
        body: Option<&[u8]>,
    ) -> Result<RtspResponse> {
        let stream = self
            .stream
            .as_mut()
            .ok_or_else(|| anyhow!("not connected"))?;
        let cseq = self.cseq;
        self.cseq += 1;

        let mut req = format!("{method} {uri} RTSP/1.0\r\nCSeq: {cseq}\r\n");
        for (k, v) in headers {
            req.push_str(&format!("{k}: {v}\r\n"));
        }
        if let Some(b) = body {
            req.push_str(&format!("Content-Length: {}\r\n", b.len()));
        }
        req.push_str("\r\n");

        stream.write_all(req.as_bytes()).await?;
        if let Some(b) = body {
            stream.write_all(b).await?;
        }
        stream.flush().await?;

        // 读取响应
        let mut reader = BufReader::new(stream);
        read_rtsp_response(&mut reader).await
    }
}

/// RTSP 响应
struct RtspResponse {
    status: String,
    headers: Vec<(String, String)>,
}

/// 读取 RTSP 响应
async fn read_rtsp_response<R: AsyncBufReadExt + Unpin>(reader: &mut R) -> Result<RtspResponse> {
    let mut status_line = String::new();
    reader.read_line(&mut status_line).await?;
    let status = status_line.trim().to_string();

    let mut headers = Vec::new();
    loop {
        let mut line = String::new();
        let n = reader.read_line(&mut line).await?;
        if n == 0 || line.trim().is_empty() {
            break;
        }
        if let Some((k, v)) = line.split_once(':') {
            headers.push((k.trim().to_string(), v.trim().to_string()));
        }
    }

    Ok(RtspResponse { status, headers })
}

/// 解析 RTSP URL: rtsp://host:port/path
fn parse_rtsp_url(url: &str) -> Result<(String, u16, String)> {
    let url = url
        .strip_prefix("rtsp://")
        .ok_or_else(|| anyhow!("invalid RTSP URL"))?;
    let (host_port, path) = url.split_once('/').unwrap_or((url, ""));
    let (host, port) = if let Some((h, p)) = host_port.split_once(':') {
        (h.to_string(), p.parse::<u16>().unwrap_or(554))
    } else {
        (host_port.to_string(), 554u16)
    };
    Ok((host, port, path.to_string()))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_rtsp_url() {
        let (host, port, path) = parse_rtsp_url("rtsp://localhost:554/live/stream").unwrap();
        assert_eq!(host, "localhost");
        assert_eq!(port, 554);
        assert_eq!(path, "live/stream");
    }

    #[test]
    fn test_parse_rtsp_url_no_port() {
        let (host, port, _path) = parse_rtsp_url("rtsp://example.com/path").unwrap();
        assert_eq!(host, "example.com");
        assert_eq!(port, 554);
    }

    #[test]
    fn test_rtsp_client_new() {
        let client = RtspClient::new("rtsp://localhost:554/test");
        assert_eq!(client.cseq, 1);
        assert!(client.session_id.is_none());
    }

    #[test]
    fn test_interleaved_frame_format() {
        // 验证 interleaved frame 格式: '$' + channel + length + data
        let data = vec![0x80, 0x60, 0x00, 0x01];
        let channel: u8 = 0;
        let mut frame = Vec::with_capacity(4 + data.len());
        frame.push(b'$');
        frame.push(channel);
        frame.extend_from_slice(&(data.len() as u16).to_be_bytes());
        frame.extend_from_slice(&data);

        assert_eq!(frame[0], b'$');
        assert_eq!(frame[1], 0);
        assert_eq!(u16::from_be_bytes([frame[2], frame[3]]), 4);
        assert_eq!(frame.len(), 8);
    }
}
