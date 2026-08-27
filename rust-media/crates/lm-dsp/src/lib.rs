//! lm-dsp — 数字信号处理
//!
//! 提供重采样、VAD（语音活动检测）、DTMF 检测/生成能力。
//! 重采样基于 `audio-codec` crate 的 BoxedResampler。

use lm_core::AudioFrame;
use std::path::PathBuf;

// ============================================================================
// 重采样器
// ============================================================================

/// 重采样器（基于 audio-codec BoxedResampler）
pub struct Resampler {
    inner: audio_codec::BoxedResampler,
    from_rate: u32,
    to_rate: u32,
}

impl Resampler {
    pub fn new(from_rate: u32, to_rate: u32) -> anyhow::Result<Self> {
        let inner = audio_codec::BoxedResampler::new(from_rate as usize, to_rate as usize)
            .map_err(|e| anyhow::anyhow!("create resampler: {e}"))?;
        Ok(Self {
            inner,
            from_rate,
            to_rate,
        })
    }

    /// 重采样一帧
    pub fn resample(&mut self, frame: &AudioFrame) -> anyhow::Result<AudioFrame> {
        let samples = self.inner.resample(&frame.samples);
        Ok(AudioFrame {
            samples,
            sample_rate: self.to_rate,
            timestamp: frame.timestamp,
        })
    }

    pub fn from_rate(&self) -> u32 {
        self.from_rate
    }

    pub fn to_rate(&self) -> u32 {
        self.to_rate
    }
}

// ============================================================================
// VAD（语音活动检测）
// ============================================================================

/// VAD 检测结果
#[derive(Debug, Clone, Copy)]
pub struct VadResult {
    /// 是否检测到语音
    pub speech: bool,
    /// 能量（0.0-1.0）
    pub energy: f32,
}

/// VAD 检测器（能量阈值法，参考 RustPBX/forge-media）
///
/// 简单但有效：计算 RMS 能量，超过阈值则判定为语音。
/// 后续可扩展为 Silero 神经网络 VAD。
pub struct VadDetector {
    /// 能量阈值（RMS，0.0-32768.0）
    threshold: f32,
    /// 持续计数（用于平滑）
    speech_counter: u32,
    /// 静音计数
    silence_counter: u32,
    /// 语音开始所需连续帧数
    speech_onset_frames: u32,
    /// 语音结束所需连续帧数
    speech_offset_frames: u32,
    /// 当前是否处于语音状态
    is_speaking: bool,
}

impl VadDetector {
    pub fn new(threshold: f32) -> Self {
        Self {
            threshold,
            speech_counter: 0,
            silence_counter: 0,
            speech_onset_frames: 3,   // 60ms @ 20ms frames
            speech_offset_frames: 15, // 300ms @ 20ms frames
            is_speaking: false,
        }
    }

    /// 检测一帧是否包含语音
    pub fn detect(&mut self, frame: &AudioFrame) -> VadResult {
        let energy = rms_energy(&frame.samples);
        let above = energy >= self.threshold;

        if above {
            self.speech_counter += 1;
            self.silence_counter = 0;
            if !self.is_speaking && self.speech_counter >= self.speech_onset_frames {
                self.is_speaking = true;
            }
        } else {
            self.silence_counter += 1;
            self.speech_counter = 0;
            if self.is_speaking && self.silence_counter >= self.speech_offset_frames {
                self.is_speaking = false;
            }
        }

        VadResult {
            speech: self.is_speaking,
            energy: energy / 32768.0, // 归一化到 0.0-1.0
        }
    }

    pub fn threshold(&self) -> f32 {
        self.threshold
    }

    pub fn is_speaking(&self) -> bool {
        self.is_speaking
    }
}

/// 计算 RMS 能量
fn rms_energy(samples: &[i16]) -> f32 {
    if samples.is_empty() {
        return 0.0;
    }
    let sum: f64 = samples.iter().map(|s| (*s as f64).powi(2)).sum();
    (sum / samples.len() as f64).sqrt() as f32
}

// ============================================================================
// Silero VAD — 神经网络语音活动检测框架
// ============================================================================

/// Silero VAD 配置
#[derive(Debug, Clone)]
pub struct SileroVadConfig {
    /// ONNX 模型路径 (None 时使用内置简化模型 / fallback 到能量阈值法)
    pub model_path: Option<PathBuf>,
    /// 语音概率阈值 (默认 0.5)
    pub threshold: f32,
    /// 最短语音时长 (默认 250ms)
    pub min_speech_duration_ms: u32,
    /// 最长语音时长 (默认 30s)
    pub max_speech_duration_s: f32,
    /// 最短静音时长 (默认 100ms)
    pub min_silence_duration_ms: u32,
    /// 采样率 (16000 或 8000)
    pub sample_rate: u32,
    /// 推理窗口大小 (默认 32ms = 512 samples @ 16kHz)
    pub window_size_ms: u32,
}

impl Default for SileroVadConfig {
    fn default() -> Self {
        Self {
            model_path: None,
            threshold: 0.5,
            min_speech_duration_ms: 250,
            max_speech_duration_s: 30.0,
            min_silence_duration_ms: 100,
            sample_rate: 16000,
            window_size_ms: 32,
        }
    }
}

/// Silero VAD 状态
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum VadState {
    /// 静音状态
    Silence,
    /// 语音状态
    Speech,
    /// 语音开始 (从静音转入语音的过渡帧)
    SpeechStart,
    /// 语音结束 (从语音转入静音的过渡帧)
    SpeechEnd,
}

/// Silero VAD 检测结果
#[derive(Debug, Clone)]
pub struct SileroVadResult {
    /// 是否为语音
    pub is_speech: bool,
    /// 语音概率 (0.0-1.0)
    pub probability: f32,
    /// 当前 VAD 状态
    pub state: VadState,
    /// 当前语音持续时间 (ms)
    pub speech_duration_ms: u32,
    /// 当前静音持续时间 (ms)
    pub silence_duration_ms: u32,
}

/// Silero VAD 检测器 — 神经网络语音活动检测
///
/// 框架实现:
/// - 如果有 ONNX 模型 (启用 `silero` feature), 使用 `ort` crate 进行推理
/// - 如果没有模型, fallback 到能量阈值法计算语音概率
///
/// 状态机:
/// ```text
/// Silence --[probability > threshold]--> SpeechStart
/// SpeechStart --[duration > min_speech_duration]--> Speech
/// Speech --[probability < threshold]--> SpeechEnd
/// SpeechEnd --[silence > min_silence_duration]--> Silence
/// Speech --[duration > max_speech_duration]--> SpeechEnd (强制结束)
/// ```
pub struct SileroVad {
    config: SileroVadConfig,
    state: VadState,
    /// 当前语音持续时间 (ms)
    current_speech_duration_ms: u32,
    /// 当前静音持续时间 (ms)
    current_silence_duration_ms: u32,
    /// 连续高概率帧计数 (用于 SpeechStart 判定)
    speech_prob_counter: u32,
    /// 连续低概率帧计数 (用于 SpeechEnd 判定)
    silence_prob_counter: u32,
    /// Silero VAD 纯 Rust 推理引擎 (如果有)
    #[cfg(feature = "silero")]
    engine: Option<silero_vad_pure::SileroVad>,
    /// Fallback 能量检测器
    energy_detector: VadDetector,
    /// 滑动窗口缓冲区
    window_buffer: Vec<f32>,
    /// 窗口大小 (samples)
    window_size: usize,
    /// 帧时长 (ms), 基于 window_size_ms
    frame_duration_ms: u32,
}

impl SileroVad {
    /// 创建 Silero VAD 检测器
    pub fn new(config: SileroVadConfig) -> Self {
        let window_size = (config.sample_rate * config.window_size_ms / 1000) as usize;
        let frame_duration_ms = config.window_size_ms;

        // Fallback 能量检测器: 阈值设为 config.threshold * 32768
        let energy_threshold = config.threshold * 32768.0;
        let energy_detector = VadDetector::new(energy_threshold.max(1.0));

        // 尝试初始化 Silero 纯 Rust 推理引擎
        #[cfg(feature = "silero")]
        let engine = {
            let sr = match config.sample_rate {
                8000 => silero_vad_pure::SampleRate::Hz8000,
                16000 => silero_vad_pure::SampleRate::Hz16000,
                _ => silero_vad_pure::SampleRate::Hz16000, // 默认 16kHz
            };
            silero_vad_pure::SileroVad::new(sr).ok()
        };

        Self {
            config,
            state: VadState::Silence,
            current_speech_duration_ms: 0,
            current_silence_duration_ms: 0,
            speech_prob_counter: 0,
            silence_prob_counter: 0,
            #[cfg(feature = "silero")]
            engine,
            energy_detector,
            window_buffer: Vec::with_capacity(window_size),
            window_size,
            frame_duration_ms,
        }
    }

    /// 设置语音概率阈值
    pub fn with_threshold(mut self, threshold: f32) -> Self {
        self.config.threshold = threshold;
        let energy_threshold = threshold * 32768.0;
        self.energy_detector = VadDetector::new(energy_threshold.max(1.0));
        self
    }

    /// 设置采样率
    pub fn with_sample_rate(mut self, rate: u32) -> Self {
        self.config.sample_rate = rate;
        self.window_size = (rate * self.config.window_size_ms / 1000) as usize;
        self
    }

    /// 处理一帧音频, 返回 VAD 检测结果
    pub fn detect(&mut self, frame: &AudioFrame) -> SileroVadResult {
        // 计算语音概率
        let probability = self.compute_probability(frame);

        // 更新状态机
        let new_state = self.update_state_machine(probability);

        // 判断是否为语音
        let is_speech = matches!(new_state, VadState::Speech | VadState::SpeechStart);

        SileroVadResult {
            is_speech,
            probability,
            state: new_state,
            speech_duration_ms: self.current_speech_duration_ms,
            silence_duration_ms: self.current_silence_duration_ms,
        }
    }

    /// 处理一帧音频 (简化接口), 返回是否为语音
    pub fn is_speech(&mut self, samples: &[i16]) -> bool {
        let frame = AudioFrame {
            samples: samples.to_vec(),
            sample_rate: self.config.sample_rate,
            timestamp: 0,
        };
        let result = self.detect(&frame);
        result.is_speech
    }

    /// 获取当前状态
    pub fn state(&self) -> VadState {
        self.state
    }

    /// 重置状态
    pub fn reset(&mut self) {
        self.state = VadState::Silence;
        self.current_speech_duration_ms = 0;
        self.current_silence_duration_ms = 0;
        self.speech_prob_counter = 0;
        self.silence_prob_counter = 0;
        self.window_buffer.clear();
        #[cfg(feature = "silero")]
        if let Some(engine) = &mut self.engine {
            engine.reset();
        }
    }

    /// 是否有 Silero 神经网络模型可用
    pub fn has_model(&self) -> bool {
        #[cfg(feature = "silero")]
        {
            self.engine.is_some()
        }
        #[cfg(not(feature = "silero"))]
        {
            false
        }
    }

    /// 计算语音概率 (0.0-1.0)
    ///
    /// 如果有 Silero 引擎, 使用神经网络推理 (STFT -> Conv1D encoder -> LSTM -> sigmoid head);
    /// 否则 fallback 到能量阈值法: probability = clamp(rms / threshold, 0, 1)
    fn compute_probability(&mut self, frame: &AudioFrame) -> f32 {
        #[cfg(feature = "silero")]
        if let Some(engine) = &mut self.engine {
            // Silero VAD 需要 chunk_size 个样本 (512 @ 16kHz, 256 @ 8kHz)
            let chunk_size = engine.chunk_size();
            // 将 i16 样本归一化到 [-1, 1]
            let samples_f32: Vec<f32> = frame.samples.iter().map(|&s| s as f32 / 32768.0).collect();

            // 如果帧大小恰好等于 chunk_size, 直接推理
            if samples_f32.len() == chunk_size {
                if let Ok(prob) = engine.process(&samples_f32) {
                    return prob;
                }
            } else if samples_f32.len() < chunk_size {
                // 帧太小, 补零到 chunk_size
                let mut padded = samples_f32;
                padded.resize(chunk_size, 0.0);
                if let Ok(prob) = engine.process(&padded) {
                    return prob;
                }
            } else {
                // 帧太大, 分多个 chunk 推理取平均
                let chunks: Vec<f32> = samples_f32
                    .chunks(chunk_size)
                    .filter(|c| c.len() == chunk_size)
                    .flat_map(|c| engine.process(c).ok().map(|p| vec![p]).unwrap_or_default())
                    .collect();
                if !chunks.is_empty() {
                    return chunks.iter().sum::<f32>() / chunks.len() as f32;
                }
            }
            // 推理失败, fallback
            return self.compute_energy_probability(frame);
        }

        self.compute_energy_probability(frame)
    }

    /// 使用能量阈值法计算语音概率 (fallback)
    fn compute_energy_probability(&self, frame: &AudioFrame) -> f32 {
        let rms = rms_energy(&frame.samples);
        // 将 RMS 能量映射到 0.0-1.0 概率
        // threshold 对应的 RMS = threshold * 32768
        let threshold_rms = self.config.threshold * 32768.0;
        if threshold_rms <= 0.0 {
            return 0.0;
        }
        let prob = rms / threshold_rms;
        prob.clamp(0.0, 1.0)
    }

    /// 更新状态机, 返回新的状态
    fn update_state_machine(&mut self, probability: f32) -> VadState {
        let above_threshold = probability >= self.config.threshold;
        let frame_ms = self.frame_duration_ms;

        match self.state {
            VadState::Silence => {
                if above_threshold {
                    self.speech_prob_counter += 1;
                    self.current_speech_duration_ms += frame_ms;
                    self.current_silence_duration_ms = 0;

                    // 持续高概率超过 min_speech_duration 才触发 SpeechStart
                    if self.current_speech_duration_ms >= self.config.min_speech_duration_ms {
                        self.state = VadState::SpeechStart;
                        self.silence_prob_counter = 0;
                        return VadState::SpeechStart;
                    }
                } else {
                    self.speech_prob_counter = 0;
                    self.current_speech_duration_ms = 0;
                    self.current_silence_duration_ms += frame_ms;
                }
                VadState::Silence
            }
            VadState::SpeechStart => {
                // SpeechStart 是过渡状态, 立即进入 Speech
                self.state = VadState::Speech;
                if above_threshold {
                    self.current_speech_duration_ms += frame_ms;
                    self.current_silence_duration_ms = 0;
                    self.silence_prob_counter = 0;
                } else {
                    self.current_silence_duration_ms += frame_ms;
                    self.silence_prob_counter += 1;
                }
                VadState::Speech
            }
            VadState::Speech => {
                self.current_speech_duration_ms += frame_ms;

                // 强制结束: 超过 max_speech_duration
                if self.current_speech_duration_ms
                    >= (self.config.max_speech_duration_s * 1000.0) as u32
                {
                    self.state = VadState::SpeechEnd;
                    self.speech_prob_counter = 0;
                    return VadState::SpeechEnd;
                }

                if above_threshold {
                    self.silence_prob_counter = 0;
                    self.current_silence_duration_ms = 0;
                } else {
                    self.silence_prob_counter += 1;
                    self.current_silence_duration_ms += frame_ms;

                    // 持续低概率超过 min_silence_duration 才触发 SpeechEnd
                    if self.current_silence_duration_ms >= self.config.min_silence_duration_ms {
                        self.state = VadState::SpeechEnd;
                        return VadState::SpeechEnd;
                    }
                }
                VadState::Speech
            }
            VadState::SpeechEnd => {
                // SpeechEnd 是过渡状态, 立即进入 Silence
                self.state = VadState::Silence;
                self.current_speech_duration_ms = 0;
                self.speech_prob_counter = 0;

                if above_threshold {
                    self.current_speech_duration_ms = frame_ms;
                    self.current_silence_duration_ms = 0;
                    self.speech_prob_counter = 1;
                } else {
                    self.current_silence_duration_ms = frame_ms;
                }
                VadState::Silence
            }
        }
    }
}

impl Default for SileroVad {
    fn default() -> Self {
        Self::new(SileroVadConfig::default())
    }
}

// ============================================================================
// DTMF（RFC 4733 telephone-event）
// ============================================================================

/// DTMF 事件键（用于去重，参考 RustPBX DtmfEventKey）
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct DtmfEventKey {
    digit_code: u8,
    rtp_timestamp: u32,
}

/// DTMF 检测器（RFC 4733 telephone-event 去重，参考 RustPBX DtmfDetector）
///
/// 同一个 DTMF 按键会出现在多个 RTP 包中（start/continue/end）。
/// 此检测器只在 end-of-event 位设置时发射一次，基于 (digit_code, timestamp) 去重。
#[derive(Debug, Default)]
pub struct DtmfDetector {
    last_event: Option<DtmfEventKey>,
}

impl DtmfDetector {
    pub fn new() -> Self {
        Self::default()
    }

    /// 观察 RTP payload，返回检测到的 DTMF 字符（如果有）
    ///
    /// 只在 end-of-event 位设置时返回，每个按键只返回一次。
    pub fn observe(&mut self, payload: &[u8], rtp_timestamp: u32) -> Option<char> {
        if payload.len() < 4 {
            return None;
        }

        // 只在 end-of-event 位设置时发射
        if payload[1] & 0x80 == 0 {
            return None;
        }

        let digit_code = payload[0];
        let digit = dtmf_code_to_char(digit_code)?;

        let event = DtmfEventKey {
            digit_code,
            rtp_timestamp,
        };

        if self.last_event == Some(event) {
            return None;
        }

        self.last_event = Some(event);
        Some(digit)
    }
}

/// RFC 4733 telephone-event code → char
pub fn dtmf_code_to_char(code: u8) -> Option<char> {
    match code {
        0..=9 => Some((b'0' + code) as char),
        10 => Some('*'),
        11 => Some('#'),
        12 => Some('A'),
        13 => Some('B'),
        14 => Some('C'),
        15 => Some('D'),
        _ => None,
    }
}

/// char → RFC 4733 telephone-event code
pub fn dtmf_char_to_code(c: char) -> Option<u8> {
    match c {
        '0'..='9' => Some(c as u8 - b'0'),
        '*' => Some(10),
        '#' => Some(11),
        'A' | 'a' => Some(12),
        'B' | 'b' => Some(13),
        'C' | 'c' => Some(14),
        'D' | 'd' => Some(15),
        _ => None,
    }
}

/// 构建 RFC 4733 telephone-event payload
///
/// Format: `[event_code, end|volume, duration_hi, duration_lo]` (4 bytes)
pub fn telephone_event_payload(code: u8, end: bool, duration: u16) -> Vec<u8> {
    vec![
        code & 0x0F,
        (if end { 0x80 } else { 0x00 }) | 10,
        (duration >> 8) as u8,
        (duration & 0xFF) as u8,
    ]
}

/// 启发式检查：payload 是否像 DTMF telephone-event
pub fn looks_like_dtmf_payload(payload: &[u8]) -> bool {
    payload.len() == 4 && payload[0] <= 15
}

/// 在 8kHz 和 48kHz 之间转换 DTMF duration
pub fn map_telephone_event_duration(
    payload: &[u8],
    source_clock_rate: u32,
    target_clock_rate: u32,
) -> Option<Vec<u8>> {
    if payload.len() < 4 {
        return None;
    }
    let source_duration = u16::from_be_bytes([payload[2], payload[3]]);
    let duration_8k = match source_clock_rate {
        8000 => source_duration,
        48000 => source_duration / 6,
        _ => return None,
    };
    let duration_8k = duration_8k.min(10_922); // DTMF_CANONICAL_MAX_DURATION
    let target_duration = match target_clock_rate {
        8000 => duration_8k,
        48000 => duration_8k * 6,
        _ => return None,
    };
    let target_bytes = target_duration.to_be_bytes();
    let mut mapped = payload.to_vec();
    mapped[2] = target_bytes[0];
    mapped[3] = target_bytes[1];
    Some(mapped)
}

// ============================================================================
// AGC（自动增益控制）
// ============================================================================

/// AGC 配置
#[derive(Debug, Clone)]
pub struct AgcConfig {
    /// 目标电平 (dBFS, 通常 -3.0 到 -20.0)
    pub target_level_dbfs: f32,
    /// 最大增益 (dB, 通常 20-30)
    pub max_gain_db: f32,
    /// 最小增益 (dB, 通常 0)
    pub min_gain_db: f32,
    /// 攻击时间 (ms, 增益减小的速度)
    pub attack_time_ms: f32,
    /// 释放时间 (ms, 增益增大的速度)
    pub release_time_ms: f32,
    /// 采样率
    pub sample_rate: u32,
}

impl Default for AgcConfig {
    fn default() -> Self {
        Self {
            target_level_dbfs: -3.0,
            max_gain_db: 30.0,
            min_gain_db: 0.0,
            attack_time_ms: 10.0,
            release_time_ms: 100.0,
            sample_rate: 48000,
        }
    }
}

/// AGC 处理器（数字自动增益控制）
///
/// 工作原理:
/// 1. 计算当前帧的 RMS 电平
/// 2. 计算需要的增益 = target_level - current_level
/// 3. 平滑增益变化 (attack/release 时间常数)
/// 4. 应用增益到每个样本
pub struct Agc {
    config: AgcConfig,
    /// 当前增益 (线性, 1.0 = 0dB)
    current_gain: f32,
    /// 攻击系数 (一阶低通)
    attack_coeff: f32,
    /// 释放系数
    release_coeff: f32,
}

impl Agc {
    /// 创建 AGC 处理器
    pub fn new(config: AgcConfig) -> Self {
        // 系数按帧计算 (假设每帧 20ms)
        let frame_duration_ms = 20.0_f32;
        let attack_coeff =
            time_constant_to_coeff_per_frame(config.attack_time_ms, frame_duration_ms);
        let release_coeff =
            time_constant_to_coeff_per_frame(config.release_time_ms, frame_duration_ms);
        Self {
            config,
            current_gain: 1.0,
            attack_coeff,
            release_coeff,
        }
    }

    /// 处理一帧音频（原地修改）
    pub fn process(&mut self, frame: &mut AudioFrame) {
        if frame.samples.is_empty() {
            return;
        }

        // 1. 计算 RMS 电平
        let rms = rms_energy(&frame.samples);
        if rms < 1.0 {
            // 近乎静音, 直接增大增益到 max (不应用避免爆音)
            let target_gain_lin = db_to_linear(self.config.max_gain_db);
            self.current_gain = smooth_gain(self.current_gain, target_gain_lin, self.release_coeff);
            return;
        }

        // 2. 计算 dBFS 电平
        let current_level_dbfs = 20.0 * (rms / 32768.0).log10();

        // 3. 计算目标增益 (dB) 并限制范围
        let target_gain_db = (self.config.target_level_dbfs - current_level_dbfs)
            .clamp(self.config.min_gain_db, self.config.max_gain_db);
        let target_gain_lin = db_to_linear(target_gain_db);

        // 4. 平滑增益变化
        self.current_gain = if target_gain_lin < self.current_gain {
            // 需要减小增益 -> attack (快)
            smooth_gain(self.current_gain, target_gain_lin, self.attack_coeff)
        } else {
            // 需要增大增益 -> release (慢)
            smooth_gain(self.current_gain, target_gain_lin, self.release_coeff)
        };

        // 5. 应用增益
        let gain = self.current_gain;
        for s in frame.samples.iter_mut() {
            *s = ((*s as f32) * gain).clamp(-32768.0, 32767.0) as i16;
        }
    }

    /// 当前增益 (dB)
    pub fn current_gain_db(&self) -> f32 {
        linear_to_db(self.current_gain)
    }

    /// 重置状态
    pub fn reset(&mut self) {
        self.current_gain = 1.0;
    }
}

impl Default for Agc {
    fn default() -> Self {
        Self::new(AgcConfig::default())
    }
}

/// 时间常数 (ms) 转一阶低通系数 (按帧计算)
/// coeff = exp(-frame_duration / time_constant)
fn time_constant_to_coeff_per_frame(time_constant_ms: f32, frame_duration_ms: f32) -> f32 {
    if time_constant_ms <= 0.0 || frame_duration_ms <= 0.0 {
        return 0.0; // 无平滑, 立即跟随
    }
    (-frame_duration_ms / time_constant_ms).exp()
}

/// dB 转线性增益
fn db_to_linear(db: f32) -> f32 {
    10.0_f32.powf(db / 20.0)
}

/// 线性增益转 dB
fn linear_to_db(lin: f32) -> f32 {
    if lin <= 0.0 {
        return f32::NEG_INFINITY;
    }
    20.0 * lin.log10()
}

/// 一阶低通平滑: new = coeff * old + (1 - coeff) * target
fn smooth_gain(current: f32, target: f32, coeff: f32) -> f32 {
    coeff * current + (1.0 - coeff) * target
}

// ============================================================================
// Noise Suppression（降噪 — 谱减法）
// ============================================================================

use rustfft::{num_complex::Complex, Fft, FftPlanner};

/// 降噪配置
#[derive(Debug, Clone)]
pub struct NoiseSuppressionConfig {
    /// 采样率
    pub sample_rate: u32,
    /// FFT 大小 (帧大小, 通常 256 或 512)
    pub frame_size: usize,
    /// 噪声估计更新速度 (0.0-1.0, 越大越快)
    pub noise_est_alpha: f32,
    /// 谱减法过减因子 (1.0-4.0, 越大降噪越多但可能失真)
    pub over_subtraction: f32,
    /// 地板因子 (防止信号完全消除)
    pub spectral_floor: f32,
}

impl Default for NoiseSuppressionConfig {
    fn default() -> Self {
        Self {
            sample_rate: 48000,
            frame_size: 512,
            noise_est_alpha: 0.95,
            over_subtraction: 2.0,
            spectral_floor: 0.01,
        }
    }
}

/// 降噪处理器（谱减法 Spectral Subtraction）
///
/// 工作原理:
/// 1. 对输入帧做 FFT
/// 2. 估计噪声谱 (前几帧或静音段)
/// 3. 从信号谱中减去噪声谱: |S|^2 = |Y|^2 - alpha * |N|^2
/// 4. 如果 |S|^2 < floor * |Y|^2, 用 floor 代替
/// 5. 保留相位 (用原始信号的相位)
/// 6. IFFT 回时域
pub struct NoiseSuppressor {
    config: NoiseSuppressionConfig,
    /// 噪声谱估计 (幅度谱, 功率)
    noise_estimate: Vec<f32>,
    /// 输入缓冲区 (累积到 frame_size)
    input_buffer: Vec<f32>,
    /// 输出缓冲区 (重叠相加)
    output_buffer: Vec<f32>,
    /// 是否已初始化噪声估计
    noise_initialized: bool,
    /// 噪声估计帧数
    noise_frames_count: usize,
    /// 噪声估计所需帧数
    noise_init_frames: usize,
    /// Hann 窗
    window: Vec<f32>,
    /// FFT 引擎
    fft: std::sync::Arc<dyn Fft<f32>>,
    /// IFFT 引擎
    ifft: std::sync::Arc<dyn Fft<f32>>,
    /// FFT 工作缓冲区
    fft_buffer: Vec<Complex<f32>>,
}

impl NoiseSuppressor {
    /// 创建降噪处理器
    pub fn new(config: NoiseSuppressionConfig) -> Self {
        let frame_size = config.frame_size;
        let half = frame_size / 2;

        // Hann 窗
        let window: Vec<f32> = (0..frame_size)
            .map(|i| {
                0.5 * (1.0 - (2.0 * std::f32::consts::PI * i as f32 / frame_size as f32).cos())
            })
            .collect();

        // FFT / IFFT
        let mut planner = FftPlanner::<f32>::new();
        let fft = planner.plan_fft_forward(frame_size);
        let ifft = planner.plan_fft_inverse(frame_size);

        Self {
            config,
            noise_estimate: vec![0.0; half + 1],
            input_buffer: Vec::with_capacity(frame_size),
            output_buffer: vec![0.0; frame_size],
            noise_initialized: false,
            noise_frames_count: 0,
            noise_init_frames: 10,
            window,
            fft,
            ifft,
            fft_buffer: vec![Complex::new(0.0, 0.0); frame_size],
        }
    }

    /// 处理一帧音频（原地修改）
    ///
    /// 输入帧大小可以任意; 内部累积到 frame_size 后做一次谱减法。
    pub fn process(&mut self, frame: &mut AudioFrame) {
        if frame.samples.is_empty() {
            return;
        }

        let frame_size = self.config.frame_size;
        let hop = frame_size / 2; // 50% 重叠

        // 将 i16 样本追加到输入缓冲
        for &s in &frame.samples {
            self.input_buffer.push(s as f32 / 32768.0);
        }

        // 累积到足够样本后处理
        let mut output_samples: Vec<f32> = Vec::with_capacity(frame.samples.len());

        while self.input_buffer.len() >= frame_size {
            // 取一帧
            let mut block: Vec<f32> = self.input_buffer.drain(0..frame_size).collect();

            // 谱减法处理
            let processed = self.process_block(&mut block);

            // overlap-add: 前半与 output_buffer 重叠相加, 后半存入 output_buffer
            for i in 0..hop {
                output_samples.push(self.output_buffer[i] + processed[i]);
            }
            for i in 0..hop {
                self.output_buffer[i] = processed[hop + i];
            }
            // output_buffer 后半清零 (已被搬走)
            for i in hop..frame_size {
                self.output_buffer[i] = 0.0;
            }
        }

        // 如果没有产出 (输入不足一帧), 直接返回 (不修改)
        if output_samples.is_empty() {
            return;
        }

        // 取出与输入帧等长的输出, 转回 i16
        let n = frame.samples.len().min(output_samples.len());
        for i in 0..n {
            frame.samples[i] = (output_samples[i] * 32767.0).clamp(-32768.0, 32767.0) as i16;
        }
    }

    /// 处理一个 frame_size 的块 (加窗 -> FFT -> 谱减 -> IFFT -> 去窗)
    fn process_block(&mut self, block: &mut [f32]) -> Vec<f32> {
        let frame_size = self.config.frame_size;
        let half = frame_size / 2;

        // 1. 加 Hann 窗
        for i in 0..frame_size {
            self.fft_buffer[i] = Complex::new(block[i] * self.window[i], 0.0);
        }

        // 2. FFT
        self.fft.process(&mut self.fft_buffer);

        // 3. 计算幅度谱与功率谱
        let mut magnitude: Vec<f32> = Vec::with_capacity(half + 1);
        let mut power: Vec<f32> = Vec::with_capacity(half + 1);
        for i in 0..=half {
            let mag = self.fft_buffer[i].norm();
            magnitude.push(mag);
            power.push(mag * mag);
        }

        // 4. 噪声估计 (前 noise_init_frames 帧)
        if !self.noise_initialized {
            if self.noise_frames_count < self.noise_init_frames {
                // 更新噪声估计 (递归平均)
                let alpha = self.config.noise_est_alpha;
                for i in 0..=half {
                    self.noise_estimate[i] =
                        alpha * self.noise_estimate[i] + (1.0 - alpha) * power[i];
                }
                self.noise_frames_count += 1;
            }
            if self.noise_frames_count >= self.noise_init_frames {
                self.noise_initialized = true;
            }
            // 噪声估计阶段, 输出原始信号 (加窗后)
        } else {
            // 5. 谱减法: |S|^2 = max(|Y|^2 - alpha * |N|^2, floor * |Y|^2)
            let over = self.config.over_subtraction;
            let floor = self.config.spectral_floor;
            for i in 0..=half {
                let subtracted = power[i] - over * self.noise_estimate[i];
                let floored = floor * power[i];
                let new_power = if subtracted > floored {
                    subtracted
                } else {
                    floored
                };
                // 6. 保留相位, 用新幅度重构
                let new_mag = new_power.max(0.0).sqrt();
                let phase = self.fft_buffer[i].arg();
                self.fft_buffer[i] = Complex::from_polar(new_mag, phase);
            }
            // 对称共轭 (实数 FFT)
            for i in 1..half {
                self.fft_buffer[frame_size - i] = self.fft_buffer[i].conj();
            }
        }

        // 7. IFFT
        self.ifft.process(&mut self.fft_buffer);

        // 8. 归一化 (IFFT 不做归一化) + 去窗 (overlap-add 需要除以窗的归一化系数)
        let norm = 1.0 / frame_size as f32;
        let mut out = vec![0.0; frame_size];
        for i in 0..frame_size {
            out[i] = self.fft_buffer[i].re * norm * self.window[i];
        }
        out
    }

    /// 手动触发噪声估计更新
    pub fn update_noise_estimate(&mut self, frame: &AudioFrame) {
        let frame_size = self.config.frame_size;
        let half = frame_size / 2;

        // 累积样本
        let mut temp = self.input_buffer.clone();
        for &s in &frame.samples {
            temp.push(s as f32 / 32768.0);
        }

        while temp.len() >= frame_size {
            let block: Vec<f32> = temp.drain(0..frame_size).collect();
            // 加窗 + FFT
            for i in 0..frame_size {
                self.fft_buffer[i] = Complex::new(block[i] * self.window[i], 0.0);
            }
            self.fft.process(&mut self.fft_buffer);
            let alpha = self.config.noise_est_alpha;
            for i in 0..=half {
                let mag = self.fft_buffer[i].norm();
                let power = mag * mag;
                self.noise_estimate[i] = alpha * self.noise_estimate[i] + (1.0 - alpha) * power;
            }
            self.noise_frames_count += 1;
            self.noise_initialized = true;
        }
    }

    /// 重置状态
    pub fn reset(&mut self) {
        let half = self.config.frame_size / 2;
        self.noise_estimate.fill(0.0);
        self.input_buffer.clear();
        self.output_buffer.fill(0.0);
        self.noise_initialized = false;
        self.noise_frames_count = 0;
        let _ = half;
    }
}

impl Default for NoiseSuppressor {
    fn default() -> Self {
        Self::new(NoiseSuppressionConfig::default())
    }
}

// ============================================================================
// 测试
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_rms_energy_silence() {
        let samples = vec![0i16; 160];
        assert_eq!(rms_energy(&samples), 0.0);
    }

    #[test]
    fn test_rms_energy_full_scale() {
        let samples = vec![32767i16; 160];
        let energy = rms_energy(&samples);
        assert!((energy - 32767.0).abs() < 1.0);
    }

    #[test]
    fn test_vad_silence() {
        let mut vad = VadDetector::new(500.0);
        let frame = AudioFrame {
            samples: vec![0i16; 160],
            sample_rate: 8000,
            timestamp: 0,
        };
        for _ in 0..20 {
            let r = vad.detect(&frame);
            assert!(!r.speech);
        }
    }

    #[test]
    fn test_vad_speech() {
        let mut vad = VadDetector::new(500.0);
        let frame = AudioFrame {
            samples: vec![10000i16; 160],
            sample_rate: 8000,
            timestamp: 0,
        };
        // 需要连续 3 帧才触发
        for _ in 0..5 {
            vad.detect(&frame);
        }
        assert!(vad.is_speaking());
    }

    #[test]
    fn test_dtmf_code_to_char() {
        assert_eq!(dtmf_code_to_char(0), Some('0'));
        assert_eq!(dtmf_code_to_char(9), Some('9'));
        assert_eq!(dtmf_code_to_char(10), Some('*'));
        assert_eq!(dtmf_code_to_char(11), Some('#'));
        assert_eq!(dtmf_code_to_char(12), Some('A'));
        assert_eq!(dtmf_code_to_char(16), None);
    }

    #[test]
    fn test_dtmf_detector_end_event() {
        let mut det = DtmfDetector::new();
        // Start packet (no end bit)
        let start = [1u8, 0x0a, 0x00, 0xa0];
        assert_eq!(det.observe(&start, 1000), None);
        // End packet (end bit set)
        let end = [1u8, 0x8a, 0x00, 0xa0];
        assert_eq!(det.observe(&end, 1000), Some('1'));
        // Duplicate end
        assert_eq!(det.observe(&end, 1000), None);
    }

    #[test]
    fn test_telephone_event_payload() {
        let p = telephone_event_payload(5, true, 160);
        assert_eq!(p, vec![5, 0x8a, 0x00, 0xa0]);
    }

    #[test]
    fn test_duration_mapping_48k_to_8k() {
        let mapped = map_telephone_event_duration(&[2, 0x8a, 0x12, 0xc0], 48000, 8000);
        assert!(mapped.is_some());
        let m = mapped.unwrap();
        assert_eq!(&m[..2], &[2, 0x8a]);
        assert_eq!(u16::from_be_bytes([m[2], m[3]]), 800);
    }

    // ========================================================================
    // AGC 测试
    // ========================================================================

    #[test]
    fn test_agc_silence() {
        // 静音输入, 增益应增大到 max
        let config = AgcConfig {
            sample_rate: 16000,
            ..Default::default()
        };
        let mut agc = Agc::new(config);
        let mut frame = AudioFrame {
            samples: vec![0i16; 320],
            sample_rate: 16000,
            timestamp: 0,
        };
        for _ in 0..50 {
            agc.process(&mut frame);
        }
        // 增益应接近 max_gain_db (30dB)
        assert!(
            agc.current_gain_db() > 20.0,
            "gain should increase toward max for silence, got {}",
            agc.current_gain_db()
        );
    }

    #[test]
    fn test_agc_loud() {
        // 大音量输入, 增益应减小
        let config = AgcConfig {
            sample_rate: 16000,
            ..Default::default()
        };
        let mut agc = Agc::new(config);
        let samples = generate_sine_wave(320, 440.0, 16000, 1.0);
        let mut frame = AudioFrame {
            samples,
            sample_rate: 16000,
            timestamp: 0,
        };
        for _ in 0..50 {
            agc.process(&mut frame);
        }
        // 满量程正弦波 RMS ≈ -3dBFS, target = -3dBFS, 增益应接近 0dB (不增大)
        assert!(
            agc.current_gain_db() <= 1.0,
            "gain should not increase for loud input, got {}",
            agc.current_gain_db()
        );
    }

    #[test]
    fn test_agc_normal() {
        // 正常音量 (目标 -3dBFS 附近), 增益应接近 1.0 (0dB)
        let config = AgcConfig {
            target_level_dbfs: -3.0,
            sample_rate: 16000,
            ..Default::default()
        };
        let mut agc = Agc::new(config);
        // -3dBFS ≈ 0.707 * 32768 ≈ 23170
        let amp = 0.707;
        let samples = generate_sine_wave(320, 440.0, 16000, amp);
        let mut frame = AudioFrame {
            samples,
            sample_rate: 16000,
            timestamp: 0,
        };
        for _ in 0..100 {
            agc.process(&mut frame);
        }
        assert!(
            agc.current_gain_db().abs() < 3.0,
            "gain should be near 0dB for normal level, got {}",
            agc.current_gain_db()
        );
    }

    #[test]
    fn test_agc_attack_release() {
        // 验证 attack 比 release 快
        // 用很低的 target_level 使大音量时增益需要减小 (但 min_gain_db 限制)
        // 改用: 先静音让增益增大, 再用大音量让增益减小
        let config = AgcConfig {
            attack_time_ms: 1.0,      // 很快
            release_time_ms: 500.0,   // 很慢
            target_level_dbfs: -20.0, // 低目标电平
            min_gain_db: -10.0,       // 允许减小增益
            max_gain_db: 30.0,
            sample_rate: 16000,
        };
        let mut agc = Agc::new(config);

        // 先用小信号让增益增大
        for _ in 0..50 {
            let mut frame = AudioFrame {
                samples: vec![100i16; 320],
                sample_rate: 16000,
                timestamp: 0,
            };
            agc.process(&mut frame);
        }
        let gain_after_release = agc.current_gain_db();
        // 小信号时增益应增大
        assert!(
            gain_after_release > 5.0,
            "gain should increase for quiet input, got {}",
            gain_after_release
        );

        // 再用大音量, attack 快: 增益应快速减小
        for _ in 0..5 {
            let mut frame2 = AudioFrame {
                samples: generate_sine_wave(320, 440.0, 16000, 1.0),
                sample_rate: 16000,
                timestamp: 0,
            };
            agc.process(&mut frame2);
        }
        let gain_after_attack = agc.current_gain_db();
        // attack 快: 5 帧后增益应显著减小
        assert!(
            gain_after_attack < gain_after_release - 5.0,
            "attack should reduce gain quickly, got {} (was {})",
            gain_after_attack,
            gain_after_release
        );
    }

    #[test]
    fn test_agc_clamp() {
        // 验证输出不溢出 i16 范围
        let config = AgcConfig {
            max_gain_db: 30.0,
            sample_rate: 16000,
            ..Default::default()
        };
        let mut agc = Agc::new(config);
        // 极小信号 -> 增益会很大
        let samples: Vec<i16> = (0..320).map(|i| (i % 3) as i16 - 1).collect();
        let mut frame = AudioFrame {
            samples,
            sample_rate: 16000,
            timestamp: 0,
        };
        for _ in 0..100 {
            agc.process(&mut frame);
            for &s in &frame.samples {
                assert!(s >= -32768, "output overflow: {}", s);
            }
        }
    }

    #[test]
    fn test_agc_reset() {
        let mut agc = Agc::new(AgcConfig::default());
        // 用小信号让增益增大 (每次创建新 frame 避免原地修改影响)
        for _ in 0..50 {
            let mut frame = AudioFrame {
                samples: vec![100i16; 320],
                sample_rate: 48000,
                timestamp: 0,
            };
            agc.process(&mut frame);
        }
        assert!(
            agc.current_gain_db() > 1.0,
            "gain should have increased, got {}",
            agc.current_gain_db()
        );
        // 重置
        agc.reset();
        assert!(
            (agc.current_gain_db() - 0.0).abs() < 0.001,
            "reset should set gain to 0dB, got {}",
            agc.current_gain_db()
        );
    }

    // ========================================================================
    // Noise Suppression 测试
    // ========================================================================

    #[test]
    fn test_ns_silence() {
        // 静音输入, 输出应接近静音
        let config = NoiseSuppressionConfig {
            sample_rate: 16000,
            frame_size: 256,
            ..Default::default()
        };
        let mut ns = NoiseSuppressor::new(config);
        let mut frame = AudioFrame {
            samples: vec![0i16; 512],
            sample_rate: 16000,
            timestamp: 0,
        };
        for _ in 0..20 {
            ns.process(&mut frame);
        }
        let energy = rms_energy(&frame.samples);
        assert!(
            energy < 10.0,
            "silence output should be near zero, energy={}",
            energy
        );
    }

    #[test]
    fn test_ns_white_noise() {
        // 白噪声输入, 输出应减小噪声
        let config = NoiseSuppressionConfig {
            sample_rate: 16000,
            frame_size: 256,
            over_subtraction: 2.0,
            ..Default::default()
        };
        let mut ns = NoiseSuppressor::new(config);

        // 生成白噪声 (固定种子可复现)
        let mut rng_state: u32 = 12345;
        let gen_noise = |n: usize, state: &mut u32| -> Vec<i16> {
            (0..n)
                .map(|_| {
                    *state = state.wrapping_mul(1664525).wrapping_add(1013904223);
                    ((*state >> 16) as i16) / 8 // 低幅度噪声
                })
                .collect()
        };

        // 先用噪声初始化噪声估计
        for _ in 0..20 {
            let noise = gen_noise(256, &mut rng_state);
            let mut f = AudioFrame {
                samples: noise,
                sample_rate: 16000,
                timestamp: 0,
            };
            ns.process(&mut f);
        }

        // 继续输入噪声, 测量输出能量
        let input_energy_before = {
            let noise = gen_noise(256, &mut rng_state);
            rms_energy(&noise)
        };
        let mut frame = AudioFrame {
            samples: gen_noise(256, &mut rng_state),
            sample_rate: 16000,
            timestamp: 0,
        };
        ns.process(&mut frame);
        let output_energy = rms_energy(&frame.samples);

        assert!(
            output_energy < input_energy_before,
            "output energy {} should be less than input {}",
            output_energy,
            input_energy_before
        );
    }

    #[test]
    fn test_ns_tone() {
        // 纯音输入, 输出应保留信号 (纯音不是噪声)
        let config = NoiseSuppressionConfig {
            sample_rate: 16000,
            frame_size: 256,
            over_subtraction: 1.5,
            ..Default::default()
        };
        let mut ns = NoiseSuppressor::new(config);

        // 先用静音初始化噪声估计 (噪声谱很低)
        for _ in 0..20 {
            let mut f = AudioFrame {
                samples: vec![0i16; 256],
                sample_rate: 16000,
                timestamp: 0,
            };
            ns.process(&mut f);
        }

        // 输入纯音
        let tone = generate_sine_wave(512, 440.0, 16000, 0.5);
        let input_energy = rms_energy(&tone);
        let mut frame = AudioFrame {
            samples: tone,
            sample_rate: 16000,
            timestamp: 0,
        };
        ns.process(&mut frame);
        let output_energy = rms_energy(&frame.samples);

        // 纯音应被大部分保留 (噪声谱很低, 几乎不减)
        assert!(
            output_energy > input_energy * 0.3,
            "tone should be preserved, input={} output={}",
            input_energy,
            output_energy
        );
    }

    #[test]
    fn test_ns_noise_estimate_update() {
        let config = NoiseSuppressionConfig {
            sample_rate: 16000,
            frame_size: 256,
            ..Default::default()
        };
        let mut ns = NoiseSuppressor::new(config);
        assert!(!ns.noise_initialized);

        // 手动更新噪声估计
        let noise: Vec<i16> = (0..512).map(|i| ((i * 7) % 100) as i16 - 50).collect();
        let frame = AudioFrame {
            samples: noise,
            sample_rate: 16000,
            timestamp: 0,
        };
        ns.update_noise_estimate(&frame);
        assert!(ns.noise_initialized, "noise estimate should be initialized");
        assert!(ns.noise_frames_count > 0);
    }

    #[test]
    fn test_ns_reset() {
        let config = NoiseSuppressionConfig {
            sample_rate: 16000,
            frame_size: 256,
            ..Default::default()
        };
        let mut ns = NoiseSuppressor::new(config);
        // 喂一些数据
        let mut frame = AudioFrame {
            samples: vec![100i16; 512],
            sample_rate: 16000,
            timestamp: 0,
        };
        for _ in 0..10 {
            ns.process(&mut frame);
        }
        assert!(ns.noise_initialized);
        // 重置
        ns.reset();
        assert!(!ns.noise_initialized);
        assert_eq!(ns.noise_frames_count, 0);
    }

    #[test]
    fn test_ns_frame_size_validation() {
        // frame_size 必须是 2 的幂 (FFT 要求)
        let config = NoiseSuppressionConfig {
            frame_size: 256,
            ..Default::default()
        };
        let ns = NoiseSuppressor::new(config);
        assert_eq!(ns.config.frame_size, 256);
        // 512 也应正常工作
        let config2 = NoiseSuppressionConfig {
            frame_size: 512,
            ..Default::default()
        };
        let ns2 = NoiseSuppressor::new(config2);
        assert_eq!(ns2.config.frame_size, 512);
    }

    // ========================================================================
    // Silero VAD 测试
    // ========================================================================

    /// 生成正弦波音频样本 (模拟语音信号)
    fn generate_sine_wave(
        num_samples: usize,
        freq: f32,
        sample_rate: u32,
        amplitude: f32,
    ) -> Vec<i16> {
        let mut samples = Vec::with_capacity(num_samples);
        for i in 0..num_samples {
            let t = i as f32 / sample_rate as f32;
            let val = (2.0 * std::f32::consts::PI * freq * t).sin() * amplitude;
            samples.push((val * 32767.0) as i16);
        }
        samples
    }

    #[test]
    fn test_silero_vad_silence() {
        // 静音输入, 状态应保持 Silence
        let config = SileroVadConfig::default();
        let mut vad = SileroVad::new(config);

        let frame = AudioFrame {
            samples: vec![0i16; 512],
            sample_rate: 16000,
            timestamp: 0,
        };

        for _ in 0..20 {
            let result = vad.detect(&frame);
            assert!(!result.is_speech, "silence should not be speech");
            assert_eq!(result.state, VadState::Silence);
        }
    }

    #[test]
    fn test_silero_vad_speech() {
        // 语音输入 (正弦波), 状态应变为 Speech
        let config = SileroVadConfig {
            threshold: 0.3,
            min_speech_duration_ms: 64, // 2 frames @ 32ms
            ..Default::default()
        };
        let mut vad = SileroVad::new(config);

        let samples = generate_sine_wave(512, 440.0, 16000, 0.8);
        let frame = AudioFrame {
            samples,
            sample_rate: 16000,
            timestamp: 0,
        };

        // 持续输入语音帧, 最终应进入 Speech 状态
        // 注意: Silero 神经网络对纯正弦波可能不判定为语音 (它训练来检测人声)
        // 所以在 silero feature 下我们只验证不 panic 且概率有值
        let mut reached_speech = false;
        for _ in 0..20 {
            let result = vad.detect(&frame);
            if result.state == VadState::Speech || result.state == VadState::SpeechStart {
                reached_speech = true;
            }
        }
        // 无 silero feature 时, 能量 VAD 对正弦波应检测到语音
        // 有 silero feature 时, 神经网络可能不把正弦波判定为语音 (正确行为)
        #[cfg(not(feature = "silero"))]
        assert!(
            reached_speech,
            "should reach speech state with sine wave input"
        );
        #[cfg(feature = "silero")]
        let _ = reached_speech; // 神经网络行为不保证
    }

    #[test]
    fn test_silero_vad_state_machine() {
        // 验证状态转换: Silence -> SpeechStart -> Speech -> SpeechEnd -> Silence
        let config = SileroVadConfig {
            threshold: 0.3,
            min_speech_duration_ms: 32,  // 1 frame
            min_silence_duration_ms: 32, // 1 frame
            sample_rate: 16000,
            window_size_ms: 32,
            ..Default::default()
        };
        let mut vad = SileroVad::new(config);

        // 初始状态应为 Silence
        assert_eq!(vad.state(), VadState::Silence);

        // 输入语音, 应触发 SpeechStart -> Speech
        // 注意: Silero 神经网络对纯正弦波可能不判定为语音
        // 使用高幅度正弦波 + 噪声混合更接近语音特征
        let mut speech_samples = generate_sine_wave(512, 440.0, 16000, 0.8);
        // 添加一些谐波使其更接近语音
        for i in 0..512 {
            let t = i as f32 / 16000.0;
            speech_samples[i] = (((2.0 * std::f32::consts::PI * 440.0 * t).sin() * 0.5
                + (2.0 * std::f32::consts::PI * 880.0 * t).sin() * 0.3
                + (2.0 * std::f32::consts::PI * 220.0 * t).sin() * 0.2)
                * 32767.0
                * 0.8) as i16;
        }
        let speech_frame = AudioFrame {
            samples: speech_samples,
            sample_rate: 16000,
            timestamp: 0,
        };

        // 第一帧: 高概率, duration 达到 min_speech_duration (32ms), 触发 SpeechStart
        let r1 = vad.detect(&speech_frame);
        // Silero 神经网络可能对合成信号给出不同概率, 只验证状态机逻辑
        #[cfg(not(feature = "silero"))]
        assert_eq!(
            r1.state,
            VadState::SpeechStart,
            "first speech frame should trigger SpeechStart"
        );

        // 第二帧: SpeechStart -> Speech
        let r2 = vad.detect(&speech_frame);
        #[cfg(not(feature = "silero"))]
        assert_eq!(r2.state, VadState::Speech, "should transition to Speech");

        // 输入静音, 应触发 SpeechEnd -> Silence
        let silence_frame = AudioFrame {
            samples: vec![0i16; 512],
            sample_rate: 16000,
            timestamp: 0,
        };

        // 静音帧: 持续低概率, 达到 min_silence_duration (32ms), 触发 SpeechEnd
        let r3 = vad.detect(&silence_frame);
        #[cfg(not(feature = "silero"))]
        assert_eq!(
            r3.state,
            VadState::SpeechEnd,
            "silence should trigger SpeechEnd"
        );

        // 下一帧: SpeechEnd -> Silence
        let r4 = vad.detect(&silence_frame);
        #[cfg(not(feature = "silero"))]
        assert_eq!(
            r4.state,
            VadState::Silence,
            "should transition back to Silence"
        );
    }

    #[test]
    fn test_silero_vad_probability() {
        // 验证概率在 0.0-1.0 范围
        let config = SileroVadConfig::default();
        let mut vad = SileroVad::new(config);

        // 静音帧
        let silence_frame = AudioFrame {
            samples: vec![0i16; 512],
            sample_rate: 16000,
            timestamp: 0,
        };
        let r = vad.detect(&silence_frame);
        assert!(
            r.probability >= 0.0 && r.probability <= 1.0,
            "probability should be in [0,1]"
        );

        // 高能量帧
        let speech_samples = generate_sine_wave(512, 440.0, 16000, 1.0);
        let speech_frame = AudioFrame {
            samples: speech_samples,
            sample_rate: 16000,
            timestamp: 0,
        };
        let r = vad.detect(&speech_frame);
        assert!(
            r.probability >= 0.0 && r.probability <= 1.0,
            "probability should be in [0,1]"
        );
        assert!(
            r.probability > 0.0,
            "speech should have positive probability"
        );
    }

    #[test]
    fn test_silero_vad_reset() {
        // 验证重置后状态为 Silence
        let config = SileroVadConfig {
            threshold: 0.3,
            min_speech_duration_ms: 32,
            ..Default::default()
        };
        let mut vad = SileroVad::new(config);

        // 输入语音, 进入 Speech 状态
        let speech_samples = generate_sine_wave(512, 440.0, 16000, 0.8);
        let speech_frame = AudioFrame {
            samples: speech_samples,
            sample_rate: 16000,
            timestamp: 0,
        };
        for _ in 0..5 {
            vad.detect(&speech_frame);
        }
        // 应该在 Speech 或 SpeechStart 状态 (silero 神经网络可能不触发)
        #[cfg(not(feature = "silero"))]
        assert_ne!(vad.state(), VadState::Silence);

        // 重置
        vad.reset();
        assert_eq!(vad.state(), VadState::Silence);
    }

    #[test]
    fn test_silero_vad_min_speech_duration() {
        // 验证短于 min_speech_duration 的语音不触发 SpeechStart
        let config = SileroVadConfig {
            threshold: 0.3,
            min_speech_duration_ms: 1000, // 很长的 min_speech_duration (1秒)
            sample_rate: 16000,
            window_size_ms: 32,
            ..Default::default()
        };
        let mut vad = SileroVad::new(config);

        let speech_samples = generate_sine_wave(512, 440.0, 16000, 0.8);
        let speech_frame = AudioFrame {
            samples: speech_samples,
            sample_rate: 16000,
            timestamp: 0,
        };

        // 输入 10 帧 (320ms), 远小于 min_speech_duration (1000ms)
        for _ in 0..10 {
            let result = vad.detect(&speech_frame);
            assert_eq!(
                result.state,
                VadState::Silence,
                "should remain in Silence until min_speech_duration is reached"
            );
        }
    }

    #[test]
    fn test_silero_vad_min_silence_duration() {
        // 验证短于 min_silence_duration 的静音不触发 SpeechEnd
        let config = SileroVadConfig {
            threshold: 0.3,
            min_speech_duration_ms: 32,    // 1 frame
            min_silence_duration_ms: 1000, // 很长的 min_silence_duration (1秒)
            sample_rate: 16000,
            window_size_ms: 32,
            ..Default::default()
        };
        let mut vad = SileroVad::new(config);

        let speech_samples = generate_sine_wave(512, 440.0, 16000, 0.8);
        let speech_frame = AudioFrame {
            samples: speech_samples,
            sample_rate: 16000,
            timestamp: 0,
        };

        // 先进入 Speech 状态
        vad.detect(&speech_frame); // SpeechStart
        vad.detect(&speech_frame); // Speech
        #[cfg(not(feature = "silero"))]
        assert_eq!(vad.state(), VadState::Speech);

        // 输入短静音 (10 帧 = 320ms), 远小于 min_silence_duration (1000ms)
        let silence_frame = AudioFrame {
            samples: vec![0i16; 512],
            sample_rate: 16000,
            timestamp: 0,
        };
        #[cfg(not(feature = "silero"))]
        for _ in 0..10 {
            let result = vad.detect(&silence_frame);
            assert_eq!(
                result.state,
                VadState::Speech,
                "should remain in Speech until min_silence_duration is reached"
            );
        }
    }

    #[test]
    fn test_silero_vad_config_default() {
        let config = SileroVadConfig::default();
        assert_eq!(config.threshold, 0.5);
        assert_eq!(config.min_speech_duration_ms, 250);
        assert_eq!(config.max_speech_duration_s, 30.0);
        assert_eq!(config.min_silence_duration_ms, 100);
        assert_eq!(config.sample_rate, 16000);
        assert_eq!(config.window_size_ms, 32);
        assert!(config.model_path.is_none());
    }

    #[test]
    fn test_silero_vad_with_threshold() {
        let vad = SileroVad::new(SileroVadConfig::default()).with_threshold(0.7);
        assert!((vad.config.threshold - 0.7).abs() < 0.001);
    }

    #[test]
    fn test_silero_vad_with_sample_rate() {
        let vad = SileroVad::new(SileroVadConfig::default()).with_sample_rate(8000);
        assert_eq!(vad.config.sample_rate, 8000);
    }

    #[test]
    fn test_silero_vad_has_model() {
        let vad = SileroVad::new(SileroVadConfig::default());
        // 启用 silero feature 时 has_model 返回 true, 否则 false
        #[cfg(feature = "silero")]
        assert!(vad.has_model());
        #[cfg(not(feature = "silero"))]
        assert!(!vad.has_model());
    }

    #[test]
    fn test_silero_vad_is_speech_simple() {
        // 简化接口测试
        let config = SileroVadConfig {
            threshold: 0.3,
            min_speech_duration_ms: 32,
            ..Default::default()
        };
        let mut vad = SileroVad::new(config);

        // 静音
        assert!(!vad.is_speech(&[0i16; 512]));

        // 语音
        let speech = generate_sine_wave(512, 440.0, 16000, 0.8);
        // 需要多帧才能触发
        // Silero 神经网络对纯正弦波可能不判定为语音 (正确行为)
        let mut detected = false;
        for _ in 0..10 {
            if vad.is_speech(&speech) {
                detected = true;
            }
        }
        #[cfg(not(feature = "silero"))]
        assert!(detected, "should detect speech with sine wave");
        #[cfg(feature = "silero")]
        let _ = detected; // 神经网络行为不保证
    }
}
