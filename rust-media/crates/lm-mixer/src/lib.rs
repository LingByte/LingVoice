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
use std::collections::{HashMap, HashSet};
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
    /// Top-K 最大发言者数（0 = 混所有人，无 Top-K 限制）
    max_speakers: usize,
}

impl std::fmt::Debug for ConferenceMixer {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("ConferenceMixer")
            .field("mix_id", &self.mix_id)
            .field("sample_rate", &self.sample_rate)
            .field("frame_size", &self.frame_size)
            .field(
                "participants",
                &self.participant_count.load(Ordering::Relaxed),
            )
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
    /// max_speakers: Top-K 最大发言者数（0 = 混所有人，N-1 模式）
    pub fn new(mix_id: impl Into<String>, sample_rate: u32) -> Self {
        Self::with_max_speakers(mix_id, sample_rate, 0)
    }

    /// 创建带 Top-K 发言者限制的混音器
    ///
    /// max_speakers > 0 时，每 tick 只混能量最高的 K 路音频，
    /// CPU 从 O(N) 降到 O(K) 恒定。带 300ms hold hysteresis 避免频繁切换。
    pub fn with_max_speakers(
        mix_id: impl Into<String>,
        sample_rate: u32,
        max_speakers: usize,
    ) -> Self {
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
            max_speakers,
        }
    }

    /// 设置 Top-K 最大发言者数（0 = 混所有人）
    pub fn set_max_speakers(&mut self, k: usize) {
        self.max_speakers = k;
    }

    /// 当前 Top-K 配置
    pub fn max_speakers(&self) -> usize {
        self.max_speakers
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
            max_speakers: self.max_speakers,
        };

        let task = tokio::spawn(async move {
            Self::mixing_loop(ctx).await;
        });

        *self.mixing_task.lock() = Some(task);
        info!(
            mix_id = %self.mix_id,
            max_speakers = self.max_speakers,
            "conference mixer started"
        );
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

    /// 混音主循环（参考 RustPBX mixing_loop + atm0s Top-K 选择）
    ///
    /// 每 20ms tick：
    /// 1. 从所有参与者拉取 PCM 帧（try_recv，非阻塞）
    /// 2. 计算每路 RMS 能量
    /// 3. 如果 max_speakers > 0，选 Top-K 能量最高的路（带 hysteresis hold）
    /// 4. 为每个参与者生成 N-1 混音（排除自己 + 只混 selected speakers）
    /// 5. try_send 到每个参与者的 output channel
    async fn mixing_loop(ctx: MixingLoopContext) {
        let interval_ms = (ctx.frame_size as f64 / ctx.sample_rate as f64 * 1000.0) as u64;
        let interval = tokio::time::Duration::from_millis(interval_ms.max(1));

        // Top-K hysteresis 状态
        // current_speakers: 当前选中的发言者集合（带 hold time）
        // speaker_hold: 每个发言者剩余的 hold 帧数
        // hold_frames = 15 (300ms @ 20ms tick)，避免说话间隙频繁切换
        let hold_frames: u32 = 15;
        let mut current_speakers: HashSet<ParticipantId> = HashSet::new();
        let mut speaker_hold: HashMap<ParticipantId, u32> = HashMap::new();

        info!(
            mix_id = %ctx.mix_id,
            frame_size = ctx.frame_size,
            sample_rate = ctx.sample_rate,
            interval_ms,
            max_speakers = ctx.max_speakers,
            "mixing loop started"
        );

        loop {
            if ctx.stopped.load(Ordering::Relaxed) {
                info!(mix_id = %ctx.mix_id, "mixing loop stopped");
                break;
            }

            tokio::time::sleep(interval).await;

            // 1. 收集所有参与者的音频帧 + 计算 RMS 能量
            let mut participant_audio: HashMap<ParticipantId, AudioFrame> = HashMap::new();
            let mut energies: Vec<(ParticipantId, f32)> = Vec::new();

            for mut entry in ctx.participants.iter_mut() {
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
                        let energy = rms_energy(&frame.samples);
                        energies.push((entry.key().clone(), energy));
                        participant_audio.insert(entry.key().clone(), frame);
                    }
                }
            }

            if participant_audio.is_empty() {
                continue;
            }

            // 2. Top-K 发言者选择（如果启用）
            let active_speakers: HashSet<ParticipantId> = if ctx.max_speakers > 0 {
                // 按 energy 降序排序
                energies.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));

                // 取 Top-K 新候选
                let new_candidates: HashSet<ParticipantId> = energies
                    .iter()
                    .take(ctx.max_speakers)
                    .map(|(pid, _)| pid.clone())
                    .collect();

                // Hysteresis: 当前发言者保留 hold_frames 帧，即使能量下降
                // 新候选直接加入
                for pid in &new_candidates {
                    speaker_hold.insert(pid.clone(), hold_frames);
                    current_speakers.insert(pid.clone());
                }

                // 衰减非新候选的 hold 计数，移除 hold 归零的
                let to_decay: Vec<ParticipantId> = speaker_hold
                    .keys()
                    .filter(|pid| !new_candidates.contains(*pid))
                    .cloned()
                    .collect();
                for pid in &to_decay {
                    let h = speaker_hold
                        .get(pid)
                        .copied()
                        .unwrap_or(0)
                        .saturating_sub(1);
                    if h == 0 {
                        speaker_hold.remove(pid);
                        current_speakers.remove(pid);
                    } else {
                        speaker_hold.insert(pid.clone(), h);
                    }
                }

                // 限制不超过 max_speakers（hysteresis 可能临时超过）
                // 如果超过，优先保留能量最高的
                if current_speakers.len() > ctx.max_speakers {
                    let mut kept: Vec<(ParticipantId, f32)> = current_speakers
                        .iter()
                        .map(|pid| {
                            let e = energies
                                .iter()
                                .find(|(p, _)| p == pid)
                                .map(|(_, e)| *e)
                                .unwrap_or(0.0);
                            (pid.clone(), e)
                        })
                        .collect();
                    kept.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
                    current_speakers = kept
                        .iter()
                        .take(ctx.max_speakers)
                        .map(|(pid, _)| pid.clone())
                        .collect();
                    // 清理被移除的 hold
                    speaker_hold.retain(|pid, _| current_speakers.contains(pid));
                }

                current_speakers.clone()
            } else {
                // 无 Top-K：混所有人
                participant_audio.keys().cloned().collect()
            };

            // 3. 为每个参与者生成 N-1 混音（只混 active_speakers）
            // 优化：直接用引用混音，避免 clone 每路 PCM
            let participant_ids: Vec<ParticipantId> =
                ctx.participants.iter().map(|e| e.key().clone()).collect();

            // 预分配混音输出 buffer（复用，减少分配）
            let mut mix_buffer: Vec<i16> = vec![0i16; ctx.frame_size];

            for output_pid in &participant_ids {
                // 收集 (input_ref, gain) 对，不 clone
                let mut inputs: Vec<(&[i16], f32)> = Vec::new();

                for (input_pid, frame) in &participant_audio {
                    if input_pid == output_pid {
                        continue; // N-1: 排除自己
                    }

                    // Top-K: 只混被选中的发言者
                    if ctx.max_speakers > 0 && !active_speakers.contains(input_pid) {
                        continue;
                    }

                    let gain = ctx
                        .route_gains
                        .get(&(input_pid.clone(), output_pid.clone()))
                        .map(|r| *r)
                        .unwrap_or(1.0);

                    if gain > 0.0 {
                        inputs.push((&frame.samples, gain));
                    }
                }

                if inputs.is_empty() {
                    continue;
                }

                // 直接混音到预分配 buffer（零 clone）
                mix_buffer.iter_mut().for_each(|s| *s = 0);
                for (samples, gain) in &inputs {
                    let len = samples.len().min(ctx.frame_size);
                    for i in 0..len {
                        let mixed = (mix_buffer[i] as f32 + (*samples)[i] as f32 * gain) as i16;
                        mix_buffer[i] = mixed.clamp(i16::MIN, i16::MAX);
                    }
                }

                let output_frame = AudioFrame {
                    samples: mix_buffer.clone(),
                    sample_rate: ctx.sample_rate,
                    timestamp: 0,
                };

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
    /// Top-K 最大发言者数（0 = 混所有人）
    max_speakers: usize,
}

// ============================================================================
// 辅助函数
// ============================================================================

/// 计算 RMS 能量（参考 lm-dsp rms_energy）
fn rms_energy(samples: &[i16]) -> f32 {
    if samples.is_empty() {
        return 0.0;
    }
    let sum: f64 = samples.iter().map(|s| (*s as f64).powi(2)).sum();
    (sum / samples.len() as f64).sqrt() as f32
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

    #[tokio::test]
    async fn test_topk_mixing() {
        // Top-K=1: 3 个参与者，只有能量最高的被混入
        let mixer = Arc::new(ConferenceMixer::with_max_speakers("test-topk", 8000, 1));
        mixer.start();

        let (tx1, mut rx1) = mixer.add_participant("p1").await.unwrap();
        let (tx2, _rx2) = mixer.add_participant("p2").await.unwrap();
        let (tx3, _rx3) = mixer.add_participant("p3").await.unwrap();

        // p2 发大声（高能量），p3 发小声（低能量）
        tx2.send(AudioFrame {
            samples: vec![30000i16; 160],
            sample_rate: 8000,
            timestamp: 0,
        })
        .await
        .unwrap();
        tx3.send(AudioFrame {
            samples: vec![100i16; 160],
            sample_rate: 8000,
            timestamp: 0,
        })
        .await
        .unwrap();

        // 等待几个 tick 让 Top-K 选择生效（需要超过 hold_frames 才会切换）
        tokio::time::sleep(tokio::time::Duration::from_millis(100)).await;

        // p1 应该收到混音（只有 p2，因为 p2 能量最高）
        let received = rx1.try_recv();
        assert!(received.is_ok(), "p1 should receive mixed audio");
        let mixed = received.unwrap();
        assert!(!mixed.samples.is_empty());
        // 混音应该接近 p2 的值（30000），因为 p3 没被选中
        assert!(
            mixed.samples.iter().all(|&s| s > 20000),
            "mixed should be dominated by p2 (high energy)"
        );

        mixer.stop();
    }

    #[test]
    fn test_rms_energy() {
        assert_eq!(rms_energy(&[0i16; 160]), 0.0);
        assert!(rms_energy(&[1000i16; 160]) > 0.0);
        assert!(rms_energy(&[30000i16; 160]) > rms_energy(&[1000i16; 160]));
    }
}
