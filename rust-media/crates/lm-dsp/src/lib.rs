//! lm-dsp — 数字信号处理
//!
//! 提供重采样、VAD（语音活动检测）、DTMF 检测/生成能力。
//! 重采样基于 `audio-codec` crate 的 BoxedResampler。

use lm_core::AudioFrame;

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
}
