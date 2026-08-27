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
    /// ONNX session (如果有)
    #[cfg(feature = "silero")]
    session: Option<ort::Session>,
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

        Self {
            config,
            state: VadState::Silence,
            current_speech_duration_ms: 0,
            current_silence_duration_ms: 0,
            speech_prob_counter: 0,
            silence_prob_counter: 0,
            #[cfg(feature = "silero")]
            session: None,
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
    }

    /// 是否有 ONNX 模型可用
    pub fn has_model(&self) -> bool {
        #[cfg(feature = "silero")]
        {
            self.session.is_some()
        }
        #[cfg(not(feature = "silero"))]
        {
            false
        }
    }

    /// 计算语音概率 (0.0-1.0)
    ///
    /// 如果有 ONNX 模型, 使用神经网络推理;
    /// 否则 fallback 到能量阈值法: probability = clamp(rms / threshold, 0, 1)
    fn compute_probability(&mut self, frame: &AudioFrame) -> f32 {
        #[cfg(feature = "silero")]
        if let Some(_session) = &self.session {
            // TODO: 实际 ONNX 推理
            // 1. 将 samples 转换为 f32 归一化 [-1, 1]
            // 2. 构造输入 tensor (shape: [1, window_size])
            // 3. 运行 session.run()
            // 4. 从输出 tensor 提取概率值
            // 这里返回 fallback 结果作为占位
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
        let mut reached_speech = false;
        for _ in 0..20 {
            let result = vad.detect(&frame);
            if result.state == VadState::Speech || result.state == VadState::SpeechStart {
                reached_speech = true;
            }
        }
        assert!(
            reached_speech,
            "should reach speech state with sine wave input"
        );
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
        let speech_samples = generate_sine_wave(512, 440.0, 16000, 0.8);
        let speech_frame = AudioFrame {
            samples: speech_samples,
            sample_rate: 16000,
            timestamp: 0,
        };

        // 第一帧: 高概率, duration 达到 min_speech_duration (32ms), 触发 SpeechStart
        let r1 = vad.detect(&speech_frame);
        assert_eq!(
            r1.state,
            VadState::SpeechStart,
            "first speech frame should trigger SpeechStart"
        );

        // 第二帧: SpeechStart -> Speech
        let r2 = vad.detect(&speech_frame);
        assert_eq!(r2.state, VadState::Speech, "should transition to Speech");

        // 输入静音, 应触发 SpeechEnd -> Silence
        let silence_frame = AudioFrame {
            samples: vec![0i16; 512],
            sample_rate: 16000,
            timestamp: 0,
        };

        // 静音帧: 持续低概率, 达到 min_silence_duration (32ms), 触发 SpeechEnd
        let r3 = vad.detect(&silence_frame);
        assert_eq!(
            r3.state,
            VadState::SpeechEnd,
            "silence should trigger SpeechEnd"
        );

        // 下一帧: SpeechEnd -> Silence
        let r4 = vad.detect(&silence_frame);
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
        // 应该在 Speech 或 SpeechStart 状态
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
        assert_eq!(vad.state(), VadState::Speech);

        // 输入短静音 (10 帧 = 320ms), 远小于 min_silence_duration (1000ms)
        let silence_frame = AudioFrame {
            samples: vec![0i16; 512],
            sample_rate: 16000,
            timestamp: 0,
        };
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
        // 无 ONNX 模型时 has_model 应返回 false
        let vad = SileroVad::new(SileroVadConfig::default());
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
        let mut detected = false;
        for _ in 0..10 {
            if vad.is_speech(&speech) {
                detected = true;
            }
        }
        assert!(detected, "should detect speech with sine wave");
    }
}
