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
        let packet =
            RtpPacket::from_bytes(data).ok_or_else(|| anyhow::anyhow!("invalid rtp packet"))?;
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
            let frame = match self.input_rx.try_recv() {
                Ok(f) => Some(f),
                Err(mpsc::error::TryRecvError::Empty) => None,
                Err(mpsc::error::TryRecvError::Disconnected) => {
                    debug!("egress input channel closed, stopping");
                    return;
                }
            };

            use std::sync::atomic::Ordering;
            if frame.is_some() {
                self.stats.frames_received.fetch_add(1, Ordering::Relaxed);
            }

            // 使用帧或静音（保持流连续性）
            let samples = frame
                .map(|f| f.samples)
                .unwrap_or_else(|| silence_buf.clone());

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
// JitterBuffer — RTP 包重排序/去抖动（Sans-IO）
// ============================================================================

/// JitterBuffer — RTP 包重排序与去抖动
///
/// Sans-IO 版本：由外部驱动调用 `push` 和 `pop`。
///
/// 功能：
/// - 按 sequence number 重排序乱序包
/// - 去重复包
/// - 缓冲 `capacity` 个包后开始输出
/// - 检测丢包（seq gap），跳过缺失包避免长时间阻塞
/// - 处理 seq 回绕（u16 溢出）
///
/// 典型用法：
/// ```ignore
/// let mut jb = JitterBuffer::new(10, 5); // capacity=10, threshold=5
/// jb.push(rtp_packet);
/// while let Some(pkt) = jb.pop() {
///     // 处理有序包
/// }
/// ```
pub struct JitterBuffer {
    /// 缓冲区容量（包数）
    capacity: usize,
    /// 开始输出前需要的最小包数
    threshold: usize,
    /// 缓冲包：按 sequence number 索引
    buffer: std::collections::HashMap<u16, RtpPacket>,
    /// 期望的下一个 sequence number
    next_seq: Option<u16>,
    /// 第一个到达的包的 seq（用于初始化 next_seq）
    first_seq: Option<u16>,
    /// 统计
    stats: JitterStats,
}

/// JitterBuffer 统计
#[derive(Debug, Default, Clone)]
pub struct JitterStats {
    pub packets_pushed: u64,
    pub packets_popped: u64,
    pub packets_dropped_duplicate: u64,
    pub packets_dropped_overflow: u64,
    pub packets_lost: u64,
    pub seq_wraps: u64,
}

impl JitterBuffer {
    /// 创建 jitter buffer
    ///
    /// capacity: 缓冲区最大包数
    /// threshold: 开始输出前需要的最小包数（通常 capacity/2）
    pub fn new(capacity: usize, threshold: usize) -> Self {
        Self {
            capacity,
            threshold: threshold.min(capacity),
            buffer: std::collections::HashMap::with_capacity(capacity),
            next_seq: None,
            first_seq: None,
            stats: JitterStats::default(),
        }
    }

    /// 投递一个 RTP 包
    ///
    /// 返回 true 表示接受，false 表示丢弃（重复或溢出）
    pub fn push(&mut self, packet: RtpPacket) -> bool {
        self.stats.packets_pushed += 1;

        // 检查重复
        if self.buffer.contains_key(&packet.sequence_number) {
            self.stats.packets_dropped_duplicate += 1;
            return false;
        }

        // 检查是否已经输出过这个 seq（迟到的包）
        if let Some(next) = self.next_seq {
            // 计算 packet seq 相对于 next_seq 的"距离"（处理回绕）
            let diff = packet.sequence_number.wrapping_sub(next);
            // 如果 diff 很大（> 32768），说明是过去的包
            if diff > 32768 {
                self.stats.packets_dropped_duplicate += 1;
                return false;
            }
        }

        // 记录第一个到达的包
        if self.first_seq.is_none() {
            self.first_seq = Some(packet.sequence_number);
        }

        // 检查缓冲区溢出
        if self.buffer.len() >= self.capacity {
            // 丢掉离 next_seq 最远的包（最旧的）
            if let Some(next) = self.next_seq.or(self.first_seq) {
                let mut farthest_seq = None;
                let mut farthest_dist: u16 = 0;
                for &seq in self.buffer.keys() {
                    let dist = seq.wrapping_sub(next);
                    if dist > farthest_dist {
                        farthest_dist = dist;
                        farthest_seq = Some(seq);
                    }
                }
                if let Some(seq) = farthest_seq {
                    self.buffer.remove(&seq);
                    self.stats.packets_dropped_overflow += 1;
                }
            }
        }

        self.buffer.insert(packet.sequence_number, packet);
        true
    }

    /// 弹出下一个有序包
    ///
    /// 当缓冲区达到 threshold 后开始输出。
    /// 如果检测到 seq gap（丢包），跳过缺失的 seq。
    pub fn pop(&mut self) -> Option<RtpPacket> {
        // 还没开始输出
        if self.next_seq.is_none() {
            if self.buffer.len() < self.threshold {
                return None;
            }
            // 开始：确定起点 seq
            // 策略：如果缓冲区中所有 seq 的跨度 < 32768，选数值最小的
            // 否则用 first_seq（回绕场景）
            let seqs: Vec<u16> = self.buffer.keys().copied().collect();
            let max_seq = *seqs.iter().max().unwrap();
            let min_seq = *seqs.iter().min().unwrap();
            let span = max_seq.wrapping_sub(min_seq);

            let start_seq = if span < 32768 {
                // 无回绕：选最小的 seq
                min_seq
            } else {
                // 有回绕：用 first_seq
                self.first_seq.unwrap_or(min_seq)
            };
            self.next_seq = Some(start_seq);
        }

        let next = self.next_seq?;

        // 检查 next 是否在缓冲区
        if let Some(pkt) = self.buffer.remove(&next) {
            self.stats.packets_popped += 1;
            // 推进 next_seq（处理回绕）
            let new_seq = next.wrapping_add(1);
            if new_seq == 0 {
                self.stats.seq_wraps += 1;
            }
            self.next_seq = Some(new_seq);
            return Some(pkt);
        }

        // next 不在缓冲区 — 可能是丢包或还没到
        // 找缓冲区中 wrapping 距离 next 最近的包
        let mut nearest_seq: Option<u16> = None;
        let mut nearest_dist: u16 = u16::MAX;
        for &seq in self.buffer.keys() {
            let dist = seq.wrapping_sub(next);
            if dist < nearest_dist {
                nearest_dist = dist;
                nearest_seq = Some(seq);
            }
        }

        if let Some(earliest) = nearest_seq {
            let gap = earliest.wrapping_sub(next);
            if gap > 0 && gap < 32768 {
                // 有比 next 更大的包，说明 next 丢了
                // 跳过丢失的包
                self.stats.packets_lost += gap as u64;
                self.next_seq = Some(earliest);
                // 递归 pop（现在 earliest 应该在缓冲区）
                return self.pop();
            }
        }

        // 缓冲区为空或所有包都在 next 之前（不该发生）
        None
    }

    /// 当前缓冲区大小
    pub fn len(&self) -> usize {
        self.buffer.len()
    }

    pub fn is_empty(&self) -> bool {
        self.buffer.is_empty()
    }

    /// 获取统计
    pub fn stats(&self) -> &JitterStats {
        &self.stats
    }

    /// 重置（清空缓冲区，重置 next_seq）
    pub fn reset(&mut self) {
        self.buffer.clear();
        self.next_seq = None;
        self.first_seq = None;
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

    // ─── JitterBuffer 测试 ───────────────────────────────────────────────

    fn make_rtp(seq: u16, ts: u32) -> RtpPacket {
        RtpPacket {
            ssrc: 12345,
            payload_type: 0,
            sequence_number: seq,
            timestamp: ts,
            marker: false,
            payload: bytes::Bytes::from_static(&[0xff; 160]),
            rid: String::new(),
        }
    }

    #[test]
    fn test_jitter_buffer_ordered() {
        // 顺序到达的包应该直接输出
        let mut jb = JitterBuffer::new(10, 3);
        for i in 0..5u16 {
            jb.push(make_rtp(i, i as u32 * 160));
        }
        // 达到 threshold 后开始输出
        let mut output = Vec::new();
        while let Some(pkt) = jb.pop() {
            output.push(pkt.sequence_number);
        }
        assert_eq!(output, vec![0, 1, 2, 3, 4]);
    }

    #[test]
    fn test_jitter_buffer_reorder() {
        // 乱序到达的包应该被重排序
        let mut jb = JitterBuffer::new(10, 3);
        // 推入顺序: 2, 0, 1, 4, 3
        jb.push(make_rtp(2, 320));
        jb.push(make_rtp(0, 0));
        jb.push(make_rtp(1, 160));
        jb.push(make_rtp(4, 640));
        jb.push(make_rtp(3, 480));

        let mut output = Vec::new();
        while let Some(pkt) = jb.pop() {
            output.push(pkt.sequence_number);
        }
        assert_eq!(output, vec![0, 1, 2, 3, 4]);
    }

    #[test]
    fn test_jitter_buffer_duplicate() {
        let mut jb = JitterBuffer::new(10, 2);
        assert!(jb.push(make_rtp(0, 0)));
        assert!(!jb.push(make_rtp(0, 0))); // 重复包被拒绝
        assert_eq!(jb.stats().packets_dropped_duplicate, 1);
    }

    #[test]
    fn test_jitter_buffer_loss() {
        // 丢包场景：0, 1, 3, 4（2 丢了）
        let mut jb = JitterBuffer::new(10, 2);
        jb.push(make_rtp(0, 0));
        jb.push(make_rtp(1, 160));
        jb.push(make_rtp(3, 480));
        jb.push(make_rtp(4, 640));

        let mut output = Vec::new();
        while let Some(pkt) = jb.pop() {
            output.push(pkt.sequence_number);
        }
        // 应该输出 0, 1, 3, 4（跳过 2）
        assert_eq!(output, vec![0, 1, 3, 4]);
        assert!(jb.stats().packets_lost > 0);
    }

    #[test]
    fn test_jitter_buffer_wrap() {
        // seq 回绕：65534, 65535, 0, 1
        let mut jb = JitterBuffer::new(10, 2);
        jb.push(make_rtp(65534, 0));
        jb.push(make_rtp(65535, 160));
        jb.push(make_rtp(0, 320));
        jb.push(make_rtp(1, 480));

        let mut output = Vec::new();
        while let Some(pkt) = jb.pop() {
            output.push(pkt.sequence_number);
        }
        assert_eq!(output, vec![65534, 65535, 0, 1]);
        assert!(jb.stats().seq_wraps >= 1);
    }

    #[test]
    fn test_jitter_buffer_late_packet() {
        // 迟到的包应该被丢弃
        let mut jb = JitterBuffer::new(10, 2);
        jb.push(make_rtp(0, 0));
        jb.push(make_rtp(1, 160));
        // 先 pop 0 和 1
        jb.pop();
        jb.pop();
        // 迟到的 0 应该被拒绝
        assert!(!jb.push(make_rtp(0, 0)));
    }

    #[test]
    fn test_jitter_buffer_threshold() {
        // 不到 threshold 不输出
        let mut jb = JitterBuffer::new(10, 5);
        jb.push(make_rtp(0, 0));
        jb.push(make_rtp(1, 160));
        assert!(jb.pop().is_none()); // 只有 2 个包，不到 5
    }
}
