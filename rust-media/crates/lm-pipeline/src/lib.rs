//! lm-pipeline — Ingress/Egress 媒体管线与 Pacer
//!
//! 参考 RustPBX egress.rs / app_ingress.rs：
//! - Ingress: 接收 RTP → 解码 → 路由分发
//! - Egress:  接收混音/转发帧 → Pacer(20ms) → 编码 → 输出
//! - Pacer: 单一 pacing task 拥有活动源的可变状态（无锁热路径）

use std::time::Duration;

use lm_core::{AudioFrame, CodecType};
use lm_transport::RtpPacket;
use tokio::sync::mpsc;
use tracing::{debug, trace, warn};

// ============================================================================
// Ingress 管线
// ============================================================================

/// Ingress 管线：RTP → 解码 → 路由
///
/// 参考 RustPBX app_ingress.rs LegPcmStream：
/// - 接收 RTP 包
/// - 解码为 PCM
/// - 通过 channel 分发给下游（路由表/混音器/录制）
pub struct IngressPipeline {
    /// 编解码类型
    codec: CodecType,
    /// 解码器
    decoder: Box<dyn audio_codec::Decoder>,
    /// 输出 channel（解码后的 PCM 帧）
    output_tx: mpsc::Sender<AudioFrame>,
    /// 统计
    stats: IngressStats,
}

/// Ingress 统计
#[derive(Debug, Default)]
pub struct IngressStats {
    pub packets_received: std::sync::atomic::AtomicU64,
    pub packets_decoded: std::sync::atomic::AtomicU64,
    pub bytes_received: std::sync::atomic::AtomicU64,
}

impl IngressPipeline {
    /// 创建 ingress 管线
    ///
    /// output_tx: 解码后的 PCM 帧发送到此 channel
    pub fn new(codec: CodecType, output_tx: mpsc::Sender<AudioFrame>) -> Self {
        let ac_codec = match codec {
            CodecType::Opus => audio_codec::CodecType::Opus,
            CodecType::PcmU => audio_codec::CodecType::PCMU,
            CodecType::PcmA => audio_codec::CodecType::PCMA,
            CodecType::G722 => audio_codec::CodecType::G722,
            _ => audio_codec::CodecType::PCMU,
        };
        Self {
            codec,
            decoder: audio_codec::create_decoder(ac_codec),
            output_tx,
            stats: IngressStats::default(),
        }
    }

    /// 处理一个入站 RTP 包
    ///
    /// 解码 RTP payload 为 PCM，发送到 output channel。
    pub fn handle_rtp(&mut self, packet: &RtpPacket) -> anyhow::Result<()> {
        use std::sync::atomic::Ordering;
        self.stats.packets_received.fetch_add(1, Ordering::Relaxed);
        self.stats
            .bytes_received
            .fetch_add(packet.payload.len() as u64, Ordering::Relaxed);

        // 解码
        let samples = self.decoder.decode(&packet.payload);
        if samples.is_empty() {
            trace!("decoder returned empty samples");
            return Ok(());
        }

        let frame = AudioFrame {
            samples,
            sample_rate: self.decoder.sample_rate(),
            timestamp: packet.timestamp as u64,
        };

        // 发送到下游（try_send 避免阻塞 ingress）
        if let Err(e) = self.output_tx.try_send(frame) {
            match e {
                mpsc::error::TrySendError::Full(_) => {
                    warn!("ingress output channel full, dropping frame");
                }
                mpsc::error::TrySendError::Closed(_) => {
                    return Err(anyhow::anyhow!("ingress output channel closed"));
                }
            }
        }

        self.stats.packets_decoded.fetch_add(1, Ordering::Relaxed);
        Ok(())
    }

    /// 处理原始 RTP 字节
    pub fn handle_rtp_bytes(&mut self, data: &[u8]) -> anyhow::Result<()> {
        let packet = RtpPacket::from_bytes(data)
            .ok_or_else(|| anyhow::anyhow!("invalid rtp packet"))?;
        self.handle_rtp(&packet)
    }

    pub fn codec(&self) -> CodecType {
        self.codec
    }

    pub fn stats(&self) -> (u64, u64, u64) {
        use std::sync::atomic::Ordering;
        (
            self.stats.packets_received.load(Ordering::Relaxed),
            self.stats.packets_decoded.load(Ordering::Relaxed),
            self.stats.bytes_received.load(Ordering::Relaxed),
        )
    }
}

// ============================================================================
// Egress 管线
// ============================================================================

/// Egress 管线：混音/转发帧 → Pacer → 编码 → 输出
///
/// 参考 RustPBX egress.rs EgressPipeline：
/// - 单一 pacing task 拥有活动源的可变状态（无锁热路径）
/// - 始终发射：每个 tick 都发射（源回退到静音），保持流连续性
pub struct EgressPipeline {
    /// 输入 channel（待发送的 PCM 帧）
    input_rx: mpsc::Receiver<AudioFrame>,
    /// 编解码类型
    codec: CodecType,
    /// 编码器
    encoder: Box<dyn audio_codec::Encoder>,
    /// 输出 channel（编码后的 RTP payload）
    output_tx: mpsc::Sender<Vec<u8>>,
    /// 采样率
    sample_rate: u32,
    /// 帧大小（样本数）
    frame_size: usize,
    /// 统计
    stats: EgressStats,
}

/// Egress 统计
#[derive(Debug, Default)]
pub struct EgressStats {
    pub frames_received: std::sync::atomic::AtomicU64,
    pub frames_encoded: std::sync::atomic::AtomicU64,
    pub bytes_sent: std::sync::atomic::AtomicU64,
}

impl EgressPipeline {
    /// 创建 egress 管线
    ///
    /// input_rx: 从此 channel 接收待发送的 PCM 帧
    /// output_tx: 编码后的 RTP payload 发送到此 channel
    pub fn new(
        codec: CodecType,
        sample_rate: u32,
        input_rx: mpsc::Receiver<AudioFrame>,
        output_tx: mpsc::Sender<Vec<u8>>,
    ) -> Self {
        let ac_codec = match codec {
            CodecType::Opus => audio_codec::CodecType::Opus,
            CodecType::PcmU => audio_codec::CodecType::PCMU,
            CodecType::PcmA => audio_codec::CodecType::PCMA,
            CodecType::G722 => audio_codec::CodecType::G722,
            _ => audio_codec::CodecType::PCMU,
        };
        let frame_size = (sample_rate as usize * 20) / 1000; // 20ms
        Self {
            input_rx,
            codec,
            encoder: audio_codec::create_encoder(ac_codec),
            output_tx,
            sample_rate,
            frame_size,
            stats: EgressStats::default(),
        }
    }

    /// 启动 egress pacing loop（参考 RustPBX pacing task）
    ///
    /// 每 20ms tick：
    /// 1. 从 input channel 拉取 PCM 帧（try_recv，非阻塞）
    /// 2. 如果没有帧，生成静音帧（保持流连续性）
    /// 3. 编码为 RTP payload
    /// 4. 发送到 output channel
    pub async fn run(mut self) {
        let interval = Duration::from_millis(20);
        let silence_buf = vec![0i16; self.frame_size];

        debug!(
            codec = ?self.codec,
            sample_rate = self.sample_rate,
            frame_size = self.frame_size,
            "egress pipeline started"
        );

        loop {
            tokio::time::sleep(interval).await;

            // 拉取所有可用帧，取最后一帧
            let frame = loop {
                match self.input_rx.try_recv() {
                    Ok(f) => break Some(f),
                    Err(mpsc::error::TryRecvError::Empty) => break None,
                    Err(mpsc::error::TryRecvError::Disconnected) => {
                        debug!("egress input channel closed, stopping");
                        return;
                    }
                }
            };

            use std::sync::atomic::Ordering;
            if frame.is_some() {
                self.stats.frames_received.fetch_add(1, Ordering::Relaxed);
            }

            // 使用帧或静音（保持流连续性）
            let samples = frame.map(|f| f.samples).unwrap_or_else(|| silence_buf.clone());

            // 编码
            let encoded = self.encoder.encode(&samples);
            if encoded.is_empty() {
                trace!("encoder returned empty bytes");
                continue;
            }

            self.stats.frames_encoded.fetch_add(1, Ordering::Relaxed);
            self.stats
                .bytes_sent
                .fetch_add(encoded.len() as u64, Ordering::Relaxed);

            // 发送到 output
            if let Err(e) = self.output_tx.send(encoded).await {
                // SendError 只在 channel 关闭时发生
                debug!("egress output channel closed: {}, stopping", e);
                return;
            }
        }
    }

    pub fn codec(&self) -> CodecType {
        self.codec
    }

    pub fn sample_rate(&self) -> u32 {
        self.sample_rate
    }
}

// ============================================================================
// Pacer — 20ms tick 节拍器（Sans-IO 版本）
// ============================================================================

/// Pacer — 20ms tick 节拍器
///
/// Sans-IO 版本：由外部驱动调用 `poll`。
/// 将突发到达的帧按 20ms 间隔平滑输出，避免网络拥塞。
pub struct Pacer {
    /// 节拍间隔（默认 20ms）
    pub tick_interval: Duration,
    /// 待发送队列
    queue: std::collections::VecDeque<AudioFrame>,
    /// 下一帧应该输出的时间（ms）
    next_tick_ms: u64,
    /// 帧大小（样本数，用于生成静音帧）
    frame_size: usize,
    /// 采样率
    sample_rate: u32,
}

impl Pacer {
    pub fn new(sample_rate: u32) -> Self {
        let frame_size = (sample_rate as usize * 20) / 1000;
        Self {
            tick_interval: Duration::from_millis(20),
            queue: std::collections::VecDeque::new(),
            next_tick_ms: 0,
            frame_size,
            sample_rate,
        }
    }

    /// 投递一帧待发送的音频
    pub fn push_frame(&mut self, frame: AudioFrame) {
        self.queue.push_back(frame);
    }

    /// 以 20ms 节拍轮询输出
    ///
    /// 当 now_ms >= next_tick_ms 时输出一帧：
    /// - 如果队列有帧，输出队首帧
    /// - 如果队列为空，输出静音帧（保持流连续性）
    pub fn poll(&mut self, now_ms: u64) -> Option<AudioFrame> {
        if now_ms < self.next_tick_ms {
            return None;
        }

        self.next_tick_ms = now_ms + self.tick_interval.as_millis() as u64;

        let frame = self.queue.pop_front().unwrap_or_else(|| AudioFrame {
            samples: vec![0i16; self.frame_size],
            sample_rate: self.sample_rate,
            timestamp: now_ms,
        });

        Some(frame)
    }

    /// 队列长度
    pub fn queue_len(&self) -> usize {
        self.queue.len()
    }
}

impl Default for Pacer {
    fn default() -> Self {
        Self::new(48000)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_pacer_outputs_frame() {
        let mut pacer = Pacer::new(8000);
        pacer.push_frame(AudioFrame {
            samples: vec![100i16; 160],
            sample_rate: 8000,
            timestamp: 0,
        });

        let frame = pacer.poll(0);
        assert!(frame.is_some());
        assert_eq!(frame.unwrap().samples, vec![100i16; 160]);
    }

    #[test]
    fn test_pacer_silence_when_empty() {
        let mut pacer = Pacer::new(8000);

        // 第一次 poll：队列为空，输出静音
        let frame = pacer.poll(0).unwrap();
        assert_eq!(frame.samples, vec![0i16; 160]);

        // 下一个 tick 之前不输出
        assert!(pacer.poll(5).is_none());
    }

    #[test]
    fn test_pacer_timing() {
        let mut pacer = Pacer::new(8000);
        pacer.push_frame(AudioFrame {
            samples: vec![100i16; 160],
            sample_rate: 8000,
            timestamp: 0,
        });

        // t=0: 输出第一帧
        assert!(pacer.poll(0).is_some());

        // t=10: 还没到 20ms，不输出
        assert!(pacer.poll(10).is_none());

        // t=20: 输出静音帧
        assert!(pacer.poll(20).is_some());
    }

    #[tokio::test]
    async fn test_ingress_pipeline() {
        let (tx, mut rx) = mpsc::channel::<AudioFrame>(100);
        let mut ingress = IngressPipeline::new(CodecType::PcmU, tx);

        // 构造一个 PCMU RTP 包
        let packet = RtpPacket {
            ssrc: 12345,
            payload_type: 0,
            sequence_number: 1,
            timestamp: 160,
            marker: false,
            payload: bytes::Bytes::from_static(&[0xff; 160]), // PCMU silence
            rid: String::new(),
        };

        ingress.handle_rtp(&packet).unwrap();

        let frame = rx.recv().await;
        assert!(frame.is_some());
        let f = frame.unwrap();
        assert!(!f.samples.is_empty());
    }

    #[tokio::test]
    async fn test_egress_pipeline() {
        let (input_tx, input_rx) = mpsc::channel::<AudioFrame>(100);
        let (output_tx, mut output_rx) = mpsc::channel::<Vec<u8>>(100);

        let egress = EgressPipeline::new(CodecType::PcmU, 8000, input_rx, output_tx);

        // 发送一帧
        input_tx
            .send(AudioFrame {
                samples: vec![100i16; 160],
                sample_rate: 8000,
                timestamp: 0,
            })
            .await
            .unwrap();

        // 启动 egress
        let handle = tokio::spawn(egress.run());

        // 等待输出
        let encoded = tokio::time::timeout(Duration::from_millis(100), output_rx.recv())
            .await
            .expect("timeout")
            .expect("no output");

        assert!(!encoded.is_empty());

        handle.abort();
    }
}
