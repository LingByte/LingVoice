//! lm-codecs — 编解码器工厂
//!
//! 基于 `audio-codec` crate 实现 Opus/G.711/G.722/G.729 编解码，
//! 适配到 `lm-core` 的 `AudioCodec` trait。

use audio_codec::{self as ac, CodecType as AcCodecType};
use lm_core::{AudioCodec, AudioFrame, CodecType};

// ============================================================================
// 适配器：把 audio-codec 的 Encoder/Decoder 包装为 lm-core 的 AudioCodec
// ============================================================================

/// 编解码适配器：同时持有 encoder 和 decoder
pub struct CodecAdapter {
    codec: CodecType,
    encoder: Box<dyn ac::Encoder>,
    decoder: Box<dyn ac::Decoder>,
}

impl CodecAdapter {
    pub fn new(codec: CodecType) -> Self {
        let ac_codec = to_ac_codec(codec);
        Self {
            codec,
            encoder: ac::create_encoder(ac_codec),
            decoder: ac::create_decoder(ac_codec),
        }
    }
}

impl AudioCodec for CodecAdapter {
    fn encode(&mut self, frame: &AudioFrame) -> anyhow::Result<Vec<u8>> {
        let encoded = self.encoder.encode(&frame.samples);
        Ok(encoded)
    }

    fn decode(&mut self, payload: &[u8], timestamp: u64) -> anyhow::Result<AudioFrame> {
        let samples = self.decoder.decode(payload);
        Ok(AudioFrame {
            samples,
            sample_rate: self.decoder.sample_rate(),
            timestamp,
        })
    }

    fn codec_type(&self) -> CodecType {
        self.codec
    }
}

// ============================================================================
// 工厂函数
// ============================================================================

/// 创建编解码器（同时支持编码和解码）
pub fn create_codec(codec: CodecType) -> Box<dyn AudioCodec> {
    Box::new(CodecAdapter::new(codec))
}

/// 创建编码器（仅编码）
pub fn create_encoder(codec: CodecType) -> Box<dyn ac::Encoder> {
    ac::create_encoder(to_ac_codec(codec))
}

/// 创建解码器（仅解码）
pub fn create_decoder(codec: CodecType) -> Box<dyn ac::Decoder> {
    ac::create_decoder(to_ac_codec(codec))
}

/// 创建重采样器
pub fn create_resampler(
    input_rate: u32,
    output_rate: u32,
) -> anyhow::Result<ac::BoxedResampler> {
    ac::BoxedResampler::new(input_rate as usize, output_rate as usize)
        .map_err(|e| anyhow::anyhow!("create resampler: {e}"))
}

// ============================================================================
// 类型转换
// ============================================================================

fn to_ac_codec(codec: CodecType) -> AcCodecType {
    match codec {
        CodecType::Opus => AcCodecType::Opus,
        CodecType::PcmU => AcCodecType::PCMU,
        CodecType::PcmA => AcCodecType::PCMA,
        CodecType::G722 => AcCodecType::G722,
        _ => panic!("unsupported codec: {:?}", codec),
    }
}

#[allow(dead_code)]
fn from_ac_codec(codec: AcCodecType) -> CodecType {
    match codec {
        AcCodecType::Opus => CodecType::Opus,
        AcCodecType::PCMU => CodecType::PcmU,
        AcCodecType::PCMA => CodecType::PcmA,
        AcCodecType::G722 => CodecType::G722,
        AcCodecType::G729 => CodecType::G722, // G729 暂映射到 G722
        AcCodecType::TelephoneEvent => CodecType::PcmU, // TelephoneEvent 不在我们的枚举里
    }
}

/// 从 RTP payload type 推断编解码类型
pub fn codec_from_payload_type(pt: u8) -> Option<CodecType> {
    match pt {
        0 => Some(CodecType::PcmU),
        8 => Some(CodecType::PcmA),
        9 => Some(CodecType::G722),
        111 => Some(CodecType::Opus),
        _ => None,
    }
}

/// 获取编解码的默认 payload type
pub fn default_payload_type(codec: CodecType) -> u8 {
    match codec {
        CodecType::PcmU => 0,
        CodecType::PcmA => 8,
        CodecType::G722 => 9,
        CodecType::Opus => 111,
        _ => 0,
    }
}

/// 获取编解码的时钟率
pub fn clock_rate(codec: CodecType) -> u32 {
    match codec {
        CodecType::PcmU | CodecType::PcmA => 8000,
        CodecType::G722 => 8000, // RTP clock rate is 8000, sample rate is 16000
        CodecType::Opus => 48000,
        _ => 8000,
    }
}

/// 获取编解码的采样率
pub fn sample_rate(codec: CodecType) -> u32 {
    match codec {
        CodecType::PcmU | CodecType::PcmA => 8000,
        CodecType::G722 => 16000,
        CodecType::Opus => 48000,
        _ => 8000,
    }
}

/// 获取编解码的声道数
pub fn channels(codec: CodecType) -> u16 {
    match codec {
        CodecType::Opus => 2,
        _ => 1,
    }
}

/// 从字符串解析编解码类型
pub fn codec_from_name(name: &str) -> Option<CodecType> {
    match name.to_lowercase().as_str() {
        "opus" => Some(CodecType::Opus),
        "pcmu" => Some(CodecType::PcmU),
        "pcma" => Some(CodecType::PcmA),
        "g722" => Some(CodecType::G722),
        "h264" | "h.264" | "avc" => Some(CodecType::H264),
        "vp8" => Some(CodecType::Vp8),
        "vp9" => Some(CodecType::Vp9),
        _ => None,
    }
}

/// 编解码类型转字符串
pub fn codec_name(codec: CodecType) -> &'static str {
    match codec {
        CodecType::Opus => "opus",
        CodecType::PcmU => "pcmu",
        CodecType::PcmA => "pcma",
        CodecType::G722 => "g722",
        CodecType::Pcm => "pcm",
        CodecType::H264 => "h264",
        CodecType::Vp8 => "vp8",
        CodecType::Vp9 => "vp9",
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_codec_roundtrip_pcma() {
        let mut codec = create_codec(CodecType::PcmA);
        let frame = AudioFrame {
            samples: vec![100i16; 160], // 20ms @ 8kHz
            sample_rate: 8000,
            timestamp: 0,
        };
        let encoded = codec.encode(&frame).unwrap();
        assert!(!encoded.is_empty());
        let decoded = codec.decode(&encoded, 0).unwrap();
        assert!(!decoded.samples.is_empty());
    }

    #[test]
    fn test_codec_roundtrip_opus() {
        let mut codec = create_codec(CodecType::Opus);
        let frame = AudioFrame {
            samples: vec![0i16; 960], // 20ms @ 48kHz mono (but opus expects 2ch)
            sample_rate: 48000,
            timestamp: 0,
        };
        let encoded = codec.encode(&frame).unwrap();
        assert!(!encoded.is_empty());
        let decoded = codec.decode(&encoded, 0).unwrap();
        assert!(!decoded.samples.is_empty());
    }

    #[test]
    fn test_codec_from_name() {
        assert_eq!(codec_from_name("opus"), Some(CodecType::Opus));
        assert_eq!(codec_from_name("PCMU"), Some(CodecType::PcmU));
        assert_eq!(codec_from_name("unknown"), None);
    }
}
