//! 视频编解码库 — VP8/VP9 (libvpx) + H.264 (openh264) + H.265 (x265/libde265) + AV1 (libaom/dav1d)
//!
//! 提供 `VideoDecoder` / `VideoEncoder` trait，用于在 Rust 媒体面做视频转码。
//!
//! ## 架构
//!
//! ```text
//! RTP payload → Depacketizer → EncodedFrame (编码帧)
//!                                ↓
//!                          VideoDecoder → YuvFrame (原始帧)
//!                                ↓
//!                          VideoEncoder → EncodedFrame (目标编码帧)
//!                                ↓
//!                          Packetizer  → RTP payload
//! ```
//!
//! 转码路径示例：VP8 → decode → YUV420p → encode → H.264（或反向）

#![allow(clippy::missing_safety_doc)]

use bytes::Bytes;
use thiserror::Error;

#[derive(Debug, Error)]
pub enum VideoCodecError {
    #[error("decode failed: {0}")]
    DecodeFailed(String),
    #[error("encode failed: {0}")]
    EncodeFailed(String),
    #[error("unsupported codec: {0}")]
    Unsupported(String),
    #[error("invalid input: {0}")]
    InvalidInput(String),
    #[error("not initialized")]
    NotInitialized,
}

/// YUV420p 原始帧（I420 平面格式）
#[derive(Debug, Clone)]
pub struct YuvFrame {
    pub y: Vec<u8>,
    pub u: Vec<u8>,
    pub v: Vec<u8>,
    pub width: u32,
    pub height: u32,
    pub timestamp: u64,
    pub keyframe: bool,
}

impl YuvFrame {
    pub fn black(width: u32, height: u32, timestamp: u64) -> Self {
        debug_assert!(
            width > 0 && height > 0,
            "YuvFrame dimensions must be non-zero"
        );
        let y_size = (width * height) as usize;
        let uv_size = ((width / 2) * (height / 2)) as usize;
        Self {
            y: vec![0u8; y_size],
            u: vec![128u8; uv_size],
            v: vec![128u8; uv_size],
            width,
            height,
            timestamp,
            keyframe: true,
        }
    }

    pub fn with_gradient(width: u32, height: u32, timestamp: u64) -> Self {
        debug_assert!(width > 0 && height > 0);
        let mut frame = Self::black(width, height, timestamp);
        for row in 0..height as usize {
            for col in 0..width as usize {
                frame.y[row * width as usize + col] = ((row + col) & 0xFF) as u8;
            }
        }
        let uv_w = (width / 2) as usize;
        let uv_h = (height / 2) as usize;
        for row in 0..uv_h {
            for col in 0..uv_w {
                let idx = row * uv_w + col;
                frame.u[idx] = ((row * 3 + 64) & 0xFF) as u8;
                frame.v[idx] = ((col * 5 + 96) & 0xFF) as u8;
            }
        }
        frame
    }

    #[inline]
    pub fn y_stride(&self) -> usize {
        self.width as usize
    }

    #[inline]
    pub fn uv_stride(&self) -> usize {
        (self.width / 2) as usize
    }

    #[inline]
    pub fn y_size(&self) -> usize {
        (self.width * self.height) as usize
    }

    #[inline]
    pub fn uv_size(&self) -> usize {
        ((self.width / 2) * (self.height / 2)) as usize
    }
}

/// 编码后的视频帧
#[derive(Debug, Clone)]
pub struct EncodedFrame {
    pub data: Bytes,
    pub width: u32,
    pub height: u32,
    pub keyframe: bool,
    pub timestamp: u64,
}

/// 编码器配置 — 运行时可调参数
#[derive(Debug, Clone)]
pub struct EncoderConfig {
    pub width: u32,
    pub height: u32,
    pub bitrate: u32,
    pub framerate: u32,
    pub keyframe_interval: u32,
    pub threads: u32,
    pub speed: u32,
    pub cpu_used: i32,
}

impl EncoderConfig {
    pub fn new(width: u32, height: u32) -> Self {
        Self {
            width,
            height,
            bitrate: 500_000,
            framerate: 30,
            keyframe_interval: 300,
            threads: 2,
            speed: 6,
            cpu_used: -1,
        }
    }

    pub fn with_bitrate(mut self, bps: u32) -> Self {
        self.bitrate = bps;
        self
    }

    pub fn with_framerate(mut self, fps: u32) -> Self {
        self.framerate = fps;
        self
    }

    pub fn with_keyframe_interval(mut self, interval: u32) -> Self {
        self.keyframe_interval = interval;
        self
    }
}

/// 视频解码器 trait
pub trait VideoDecoder: Send + Sync {
    fn decode(&mut self, data: &[u8], timestamp: u64) -> Result<YuvFrame, VideoCodecError>;
    fn codec(&self) -> lm_core::CodecType;
}

/// 视频编码器 trait
pub trait VideoEncoder: Send + Sync {
    fn encode(&mut self, frame: &YuvFrame) -> Result<EncodedFrame, VideoCodecError>;
    fn request_keyframe(&mut self);
    fn codec(&self) -> lm_core::CodecType;
    fn set_bitrate(&mut self, bps: u32);
    fn set_framerate(&mut self, fps: u32);
}

/// 创建视频解码器
pub fn create_decoder(codec: lm_core::CodecType) -> Result<Box<dyn VideoDecoder>, VideoCodecError> {
    match codec {
        #[cfg(feature = "vpx")]
        lm_core::CodecType::Vp8 => Ok(Box::new(crate::vpx_codec::VpxDecoder::new_vp8()?)),
        #[cfg(feature = "vpx")]
        lm_core::CodecType::Vp9 => Ok(Box::new(crate::vpx_codec::VpxDecoder::new_vp9()?)),
        #[cfg(feature = "openh264")]
        lm_core::CodecType::H264 => Ok(Box::new(crate::openh264_codec::Openh264Decoder::new()?)),
        #[cfg(feature = "libde265")]
        lm_core::CodecType::H265 => Ok(Box::new(crate::libde265_codec::Libde265Decoder::new()?)),
        #[cfg(feature = "dav1d")]
        lm_core::CodecType::Av1 => Ok(Box::new(crate::dav1d_codec::Dav1dDecoder::new()?)),
        _ => Err(VideoCodecError::Unsupported(format!("{:?}", codec))),
    }
}

/// 创建视频编码器
pub fn create_encoder(
    codec: lm_core::CodecType,
    width: u32,
    height: u32,
) -> Result<Box<dyn VideoEncoder>, VideoCodecError> {
    let config = EncoderConfig::new(width, height);
    create_encoder_with_config(codec, config)
}

/// 使用配置创建视频编码器
pub fn create_encoder_with_config(
    codec: lm_core::CodecType,
    config: EncoderConfig,
) -> Result<Box<dyn VideoEncoder>, VideoCodecError> {
    match codec {
        #[cfg(feature = "vpx")]
        lm_core::CodecType::Vp8 => Ok(Box::new(crate::vpx_codec::VpxEncoder::new_vp8(config)?)),
        #[cfg(feature = "vpx")]
        lm_core::CodecType::Vp9 => Ok(Box::new(crate::vpx_codec::VpxEncoder::new_vp9(config)?)),
        #[cfg(feature = "openh264")]
        lm_core::CodecType::H264 => Ok(Box::new(crate::openh264_codec::Openh264Encoder::new(
            config,
        )?)),
        #[cfg(feature = "x265")]
        lm_core::CodecType::H265 => Ok(Box::new(crate::x265_codec::X265Encoder::new(config)?)),
        #[cfg(feature = "libaom")]
        lm_core::CodecType::Av1 => Ok(Box::new(crate::aom_codec::AomEncoder::new(config)?)),
        _ => Err(VideoCodecError::Unsupported(format!("{:?}", codec))),
    }
}

/// 返回当前编译支持的解码器列表
pub fn supported_decoders() -> Vec<lm_core::CodecType> {
    let mut codecs = Vec::new();
    #[cfg(feature = "vpx")]
    {
        codecs.push(lm_core::CodecType::Vp8);
        codecs.push(lm_core::CodecType::Vp9);
    }
    #[cfg(feature = "openh264")]
    {
        codecs.push(lm_core::CodecType::H264);
    }
    #[cfg(feature = "libde265")]
    {
        codecs.push(lm_core::CodecType::H265);
    }
    #[cfg(feature = "dav1d")]
    {
        codecs.push(lm_core::CodecType::Av1);
    }
    codecs
}

/// 返回当前编译支持的编码器列表
pub fn supported_encoders() -> Vec<lm_core::CodecType> {
    let mut codecs = Vec::new();
    #[cfg(feature = "vpx")]
    {
        codecs.push(lm_core::CodecType::Vp8);
        codecs.push(lm_core::CodecType::Vp9);
    }
    #[cfg(feature = "openh264")]
    {
        codecs.push(lm_core::CodecType::H264);
    }
    #[cfg(feature = "x265")]
    {
        codecs.push(lm_core::CodecType::H265);
    }
    #[cfg(feature = "libaom")]
    {
        codecs.push(lm_core::CodecType::Av1);
    }
    codecs
}

#[cfg(feature = "vpx")]
pub mod vpx_codec;

#[cfg(feature = "openh264")]
pub mod openh264_codec;

#[cfg(feature = "x265")]
pub mod x265_codec;

#[cfg(feature = "libde265")]
pub mod libde265_codec;

#[cfg(feature = "dav1d")]
pub mod dav1d_codec;

#[cfg(feature = "libaom")]
pub mod aom_codec;

#[cfg(test)]
mod tests;
