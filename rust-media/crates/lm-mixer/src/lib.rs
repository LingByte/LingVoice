//! lm-mixer — N-1 MCU 混音
//!
//! 参考 RustPBX conference_mixer.rs：
//! - ConferenceMixer 维护房间内所有参与者的音频通道
//! - 每个 tick（20ms）从所有参与者拉取 PCM 帧
//! - 为每个参与者生成"除自己外所有人"的混音（N-1）
//! - 支持 per-route 增益（监听模式）
//! - 使用 try_send 避免单个慢消费者阻塞整个房间

use dashmap::DashMap;
use lm_core::AudioFrame;
use parking_lot::Mutex;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::Arc;
use tokio::sync::mpsc;
use tokio::task::JoinHandle;
use tracing::{debug, info};

// ============================================================================
// 基础混音器（纯函数，无状态）
// ============================================================================

/// 音频混音器（静态工具，参考 RustPBX mixer.rs）
pub struct AudioMixer;

impl AudioMixer {
    /// 将多路 PCM 帧按增益混合
    ///
    /// - `frames`: 各路 PCM 样本（需相同长度）
    /// - `gains`: 各路增益（0.0=静音, 1.0=原音量）
    ///
    /// 返回混合后的单路 PCM 样本（i16），带饱和保护。
    pub fn mix(frames: Vec<Vec<i16>>, gains: &[f32]) -> Vec<i16> {
        if frames.is_empty() || gains.len() != frames.len() {
            return Vec::new();
        }

        let frame_len = frames[0].len();
        let mut output = vec![0i16; frame_len];

        for (frame, &gain) in frames.iter().zip(gains) {
            if frame.len() != frame_len {
                continue;
            }
            for (i, sample) in frame.iter().enumerate() {
                let mixed = (output[i] as f32 + (*sample as f32) * gain) as i16;
                output[i] = mixed.clamp(i16::MIN, i16::MAX);
            }
        }

        output
    }

    /// 将多路 AudioFrame 按增益混合（便捷方法）
    pub fn mix_frames(frames: &[AudioFrame], gains: &[f32]) -> Vec<i16> {
        let samples: Vec<Vec<i16>> = frames.iter().map(|f| f.samples.clone()).collect();
        Self::mix(samples, gains)
    }
}

// ============================================================================
// 会议参与者
// ============================================================================

/// 参与者 ID（用 TrackId 表示）
pub type ParticipantId = String;

/// 会议参与者音频接口（参考 RustPBX ConferenceParticipantAudio）
struct ParticipantAudio {
    /// 输入通道（从参与者接收解码后的 PCM）
    input_rx: mpsc::Receiver<AudioFrame>,
    /// 输出通道（向参与者发送混音后的 PCM）
    output_tx: mpsc::Sender<AudioFrame>,
    /// 是否静音
    muted: AtomicBool,
}

// ============================================================================
// 会议混音器（N-1 MCU）
// ============================================================================

/// 实时会议混音器（参考 RustPBX ConferenceAudioMixer）
///
/// MCU 架构：每个参与者收到所有其他参与者的混音（N-1）。
/// 使用 20ms tick 的 mixing loop，try_send 避免慢消费者阻塞。
pub struct ConferenceMixer {
    /// 会议 ID
    mix_id: String,
    /// 参与者音频通道
    participants: Arc<DashMap<ParticipantId, ParticipantAudio>>,
    /// 参与者数量（原子，用于快速查询）
    participant_count: Arc<AtomicUsize>,
    /// 采样率
    sample_rate: u32,
    /// 每帧样本数（如 960 = 20ms@48k, 160 = 20ms@8k）
    frame_size: usize,
    /// 停止标志
    stopped: Arc<AtomicBool>,
    /// Mixing task handle
    mixing_task: Arc<Mutex<Option<JoinHandle<()>>>>,
    /// Per-(src, dst) 增益覆盖（监听模式）
    /// Key: (src_id, dst_id), Value: gain (0.0=静音, 1.0=正常)
    route_gains: Arc<DashMap<(ParticipantId, ParticipantId), f32>>,
}

impl std::fmt::Debug for ConferenceMixer {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("ConferenceMixer")
            .field("mix_id", &self.mix_id)
            .field("sample_rate", &self.sample_rate)
            .field("frame_size", &self.frame_size)
            .field("participants", &self.participant_count.load(Ordering::Relaxed))
            .finish_non_exhaustive()
    }
}

impl Drop for ConferenceMixer {
    fn drop(&mut self) {
        self.stop();
    }
}

impl ConferenceMixer {
    /// 创建新混音器
    ///
    /// sample_rate 通常为 48000（Opus）或 8000（G.711）
    /// frame_size = sample_rate * 20 / 1000（20ms 帧）
    pub fn new(mix_id: impl Into<String>, sample_rate: u32) -> Self {
        let frame_size = (sample_rate as usize * 20) / 1000; // 20ms frames
        Self {
            mix_id: mix_id.into(),
            participants: Arc::new(DashMap::new()),
            participant_count: Arc::new(AtomicUsize::new(0)),
            sample_rate,
            frame_size,
            stopped: Arc::new(AtomicBool::new(false)),
            mixing_task: Arc::new(Mutex::new(None)),
            route_gains: Arc::new(DashMap::new()),
        }
    }

    /// 添加参与者
    ///
    /// 返回 (input_tx, output_rx)：
    /// - input_tx: 向混音器发送参与者的 PCM 帧
    /// - output_rx: 从混音器接收混音后的 PCM 帧
    pub async fn add_participant(
        &self,
        participant_id: impl Into<ParticipantId>,
    ) -> anyhow::Result<(mpsc::Sender<AudioFrame>, mpsc::Receiver<AudioFrame>)> {
        let pid = participant_id.into();
        if self.participants.contains_key(&pid) {
            return Err(anyhow::anyhow!("participant {} already exists", pid));
        }

        let (input_tx, input_rx) = mpsc::channel::<AudioFrame>(100);
        let (output_tx, output_rx) = mpsc::channel::<AudioFrame>(100);

        self.participants.insert(
            pid.clone(),
            ParticipantAudio {
                input_rx,
                output_tx,
                muted: AtomicBool::new(false),
            },
        );

        self.participant_count.fetch_add(1, Ordering::Relaxed);

        info!(mix_id = %self.mix_id, participant = %pid, "participant added");
        Ok((input_tx, output_rx))
    }

    /// 移除参与者
    pub async fn remove_participant(&self, participant_id: &str) -> anyhow::Result<()> {
        let was_present = self.participants.remove(participant_id).is_some();
        if was_present {
            self.participant_count.fetch_sub(1, Ordering::Relaxed);

            // 清理相关的 route gains
            let before = self.route_gains.len();
            self.route_gains
                .retain(|(src, dst), _| src != participant_id && dst != participant_id);
            let pruned = before - self.route_gains.len();
            if pruned > 0 {
                debug!(mix_id = %self.mix_id, participant = participant_id, pruned, "pruned route gains");
            }
        }

        info!(mix_id = %self.mix_id, participant = participant_id, "participant removed");
        Ok(())
    }

    /// 静音/取消静音
    pub fn set_muted(&self, participant_id: &str, muted: bool) -> anyhow::Result<()> {
        if let Some(p) = self.participants.get(participant_id) {
            p.muted.store(muted, Ordering::Relaxed);
            info!(mix_id = %self.mix_id, participant = participant_id, muted, "mute state changed");
        }
        Ok(())
    }

    /// 设置 per-route 增益（监听模式）
    ///
    /// gain = 0.0: src 对 dst 静音
    /// gain = 1.0: 正常（默认，会移除覆盖）
    pub fn set_route_gain(&self, src: &str, dst: &str, gain: f32) {
        if (gain - 1.0).abs() < f32::EPSILON {
            self.route_gains.remove(&(src.into(), dst.into()));
        } else {
            self.route_gains.insert((src.into(), dst.into()), gain);
        }
        info!(mix_id = %self.mix_id, src, dst, gain, "route gain set");
    }

    /// 启动混音 loop
    pub fn start(self: &Arc<Self>) {
        let ctx = MixingLoopContext {
            mix_id: self.mix_id.clone(),
            participants: self.participants.clone(),
            stopped: self.stopped.clone(),
            frame_size: self.frame_size,
            sample_rate: self.sample_rate,
            route_gains: self.route_gains.clone(),
        };

        let task = tokio::spawn(async move {
            Self::mixing_loop(ctx).await;
        });

        *self.mixing_task.lock() = Some(task);
        info!(mix_id = %self.mix_id, "conference mixer started");
    }

    /// 停止混音
    pub fn stop(&self) {
        self.stopped.store(true, Ordering::Relaxed);
        self.route_gains.clear();
        self.participants.clear();

        if let Some(t) = self.mixing_task.lock().take() {
            t.abort();
        }
        info!(mix_id = %self.mix_id, "conference mixer stopped");
    }

    /// 参与者数量
    pub fn participant_count(&self) -> usize {
        self.participant_count.load(Ordering::Relaxed)
    }

    /// 采样率
    pub fn sample_rate(&self) -> u32 {
        self.sample_rate
    }

    /// 每帧样本数
    pub fn frame_size(&self) -> usize {
        self.frame_size
    }

    /// 混音主循环（参考 RustPBX mixing_loop）
    ///
    /// 每 20ms tick：
    /// 1. 从所有参与者拉取 PCM 帧（try_recv，非阻塞）
    /// 2. 为每个参与者生成 N-1 混音（排除自己 + 静音的）
    /// 3. try_send 到每个参与者的 output channel
    async fn mixing_loop(ctx: MixingLoopContext) {
        let interval_ms = (ctx.frame_size as f64 / ctx.sample_rate as f64 * 1000.0) as u64;
        let interval = tokio::time::Duration::from_millis(interval_ms.max(1));

        info!(
            mix_id = %ctx.mix_id,
            frame_size = ctx.frame_size,
            sample_rate = ctx.sample_rate,
            interval_ms,
            "mixing loop started"
        );

        loop {
            if ctx.stopped.load(Ordering::Relaxed) {
                info!(mix_id = %ctx.mix_id, "mixing loop stopped");
                break;
            }

            tokio::time::sleep(interval).await;

            // 1. 收集所有参与者的音频帧
            let mut participant_audio: std::collections::HashMap<ParticipantId, AudioFrame> =
                std::collections::HashMap::new();

            for mut entry in ctx.participants.iter_mut() {
                // try_recv 所有可用帧，取最后一帧
                let mut last_frame = None;
                loop {
                    match entry.input_rx.try_recv() {
                        Ok(frame) => last_frame = Some(frame),
                        Err(mpsc::error::TryRecvError::Empty) => break,
                        Err(mpsc::error::TryRecvError::Disconnected) => break,
                    }
                }
                if let Some(frame) = last_frame {
                    if !entry.muted.load(Ordering::Relaxed) {
                        participant_audio.insert(entry.key().clone(), frame);
                    }
                }
            }

            if participant_audio.is_empty() {
                continue;
            }

            // 2. 为每个参与者生成 N-1 混音
            let participant_ids: Vec<ParticipantId> =
                ctx.participants.iter().map(|e| e.key().clone()).collect();

            for output_pid in &participant_ids {
                let mut input_frames = Vec::new();
                let mut gains = Vec::new();

                for (input_pid, frame) in &participant_audio {
                    if input_pid == output_pid {
                        continue; // N-1: 排除自己
                    }

                    let gain = ctx
                        .route_gains
                        .get(&(input_pid.clone(), output_pid.clone()))
                        .map(|r| *r)
                        .unwrap_or(1.0);

                    if gain > 0.0 {
                        input_frames.push(frame.samples.clone());
                        gains.push(gain);
                    }
                }

                if input_frames.is_empty() {
                    continue;
                }

                // 归一化帧长度
                let mut normalized = Vec::with_capacity(input_frames.len());
                for mut f in input_frames {
                    if f.len() < ctx.frame_size {
                        f.resize(ctx.frame_size, 0);
                    } else if f.len() > ctx.frame_size {
                        f.truncate(ctx.frame_size);
                    }
                    normalized.push(f);
                }

                let mixed_samples = AudioMixer::mix(normalized, &gains);
                let output_frame = AudioFrame {
                    samples: mixed_samples,
                    sample_rate: ctx.sample_rate,
                    timestamp: 0, // 由 egress pacer 填充
                };

                // try_send: 慢消费者丢帧，不阻塞整个房间
                if let Some(p) = ctx.participants.get(output_pid) {
                    let _ = p.output_tx.try_send(output_frame);
                }
            }
        }
    }
}

/// 混音循环上下文
struct MixingLoopContext {
    mix_id: String,
    participants: Arc<DashMap<ParticipantId, ParticipantAudio>>,
    stopped: Arc<AtomicBool>,
    frame_size: usize,
    sample_rate: u32,
    route_gains: Arc<DashMap<(ParticipantId, ParticipantId), f32>>,
}

// ============================================================================
// 测试
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_mixer_basic() {
        let frame1 = vec![1000i16; 160];
        let frame2 = vec![500i16; 160];
        let gains = vec![1.0, 1.0];

        let result = AudioMixer::mix(vec![frame1, frame2], &gains);
        assert_eq!(result.len(), 160);
        assert!(result.iter().all(|&s| s > 1000));
    }

    #[test]
    fn test_mixer_saturation() {
        let frame1 = vec![30000i16; 160];
        let frame2 = vec![30000i16; 160];
        let gains = vec![1.0, 1.0];

        let result = AudioMixer::mix(vec![frame1, frame2], &gains);
        assert_eq!(result.len(), 160);
        assert!(result.iter().all(|&s| s == i16::MAX));
    }

    #[test]
    fn test_mixer_zero_gain() {
        let frame1 = vec![1000i16; 160];
        let frame2 = vec![1000i16; 160];
        let gains = vec![1.0, 0.0];

        let result = AudioMixer::mix(vec![frame1, frame2], &gains);
        assert_eq!(result.len(), 160);
        assert!(result.iter().all(|&s| (900..=1100).contains(&s)));
    }

    #[test]
    fn test_mixer_empty() {
        let result = AudioMixer::mix(vec![], &[]);
        assert!(result.is_empty());
    }

    #[tokio::test]
    async fn test_conference_mixer_create() {
        let mixer = ConferenceMixer::new("test", 8000);
        assert_eq!(mixer.participant_count(), 0);
    }

    #[tokio::test]
    async fn test_add_remove_participant() {
        let mixer = ConferenceMixer::new("test", 8000);
        let (_tx, _rx) = mixer.add_participant("p1").await.unwrap();
        assert_eq!(mixer.participant_count(), 1);
        mixer.remove_participant("p1").await.unwrap();
        assert_eq!(mixer.participant_count(), 0);
    }

    #[tokio::test]
    async fn test_n1_mixing() {
        let mixer = Arc::new(ConferenceMixer::new("test", 8000));
        mixer.start();

        // 添加两个参与者
        let (tx1, mut rx1) = mixer.add_participant("p1").await.unwrap();
        let (tx2, _rx2) = mixer.add_participant("p2").await.unwrap();

        // p2 发送音频，p1 应该收到（N-1 混音 = p2 的声音）
        let frame = AudioFrame {
            samples: vec![1000i16; 160],
            sample_rate: 8000,
            timestamp: 0,
        };
        tx2.send(frame).await.unwrap();

        // 等待混音 loop 处理（20ms tick）
        tokio::time::sleep(tokio::time::Duration::from_millis(50)).await;

        // p1 应该收到混音
        let received = rx1.try_recv();
        assert!(received.is_ok(), "p1 should receive mixed audio");
        let mixed = received.unwrap();
        assert!(!mixed.samples.is_empty());

        mixer.stop();
    }
}
