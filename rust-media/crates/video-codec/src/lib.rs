//! 视频编解码库 — VP8 (libvpx) + H.264 (openh264)
//!
//! 提供 `VideoDecoder` / `VideoEncoder` trait，用于在 Rust 媒体面做视频转码。
//!
//! ## 架构
//!
//! ```text
//! RTP payload → Depacketizer → VideoFrame (编码帧)
//!                                ↓
//!                          VideoDecoder → YUV420p (原始帧)
//!                                ↓
//!                          VideoEncoder → VideoFrame (目标编码帧)
//!                                ↓
//!                          Packetizer  → RTP payload
//! ```
//!
//! 转码路径：VP8 → decode → YUV420p → encode → H.264（或反向）

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
    /// Y 平面（亮度），宽×高 个样本
    pub y: Vec<u8>,
    /// U 平面（色度），(宽/2)×(高/2) 个样本
    pub u: Vec<u8>,
    /// V 平面（色度），(宽/2)×(高/2) 个样本
    pub v: Vec<u8>,
    /// 帧宽度
    pub width: u32,
    /// 帧高度
    pub height: u32,
    /// 时间戳（RTP 时钟域，90kHz for video）
    pub timestamp: u64,
    /// 是否为关键帧
    pub keyframe: bool,
}

impl YuvFrame {
    /// 创建一个全黑的 YUV420p 帧
    pub fn black(width: u32, height: u32, timestamp: u64) -> Self {
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

    /// Y 平面行跨度（紧凑布局 = width）
    #[inline]
    pub fn y_stride(&self) -> usize {
        self.width as usize
    }

    /// UV 平面行跨度
    #[inline]
    pub fn uv_stride(&self) -> usize {
        (self.width / 2) as usize
    }
}

/// 编码后的视频帧（一个完整帧的 NALU 或 VP8 payload）
#[derive(Debug, Clone)]
pub struct EncodedFrame {
    /// 编码数据（H.264: Annex-B NALU；VP8: raw payload）
    pub data: Bytes,
    /// 帧宽度（可能未知 = 0）
    pub width: u32,
    /// 帧高度（可能未知 = 0）
    pub height: u32,
    /// 是否为关键帧
    pub keyframe: bool,
    /// 时间戳
    pub timestamp: u64,
}

/// 视频解码器 trait
pub trait VideoDecoder: Send + Sync {
    /// 解码一个完整的编码帧 → YUV420p
    ///
    /// 输入是一个完整的编码帧（已从 RTP depacketizer 组装完毕）：
    /// - H.264: Annex-B 格式的 NALU（含起始码）
    /// - VP8: 完整 VP8 payload
    fn decode(&mut self, data: &[u8], timestamp: u64) -> Result<YuvFrame, VideoCodecError>;

    /// 编解码类型
    fn codec(&self) -> lm_core::CodecType;
}

/// 视频编码器 trait
pub trait VideoEncoder: Send + Sync {
    /// 编码一帧 YUV420p → 编码帧
    ///
    /// 输出：
    /// - H.264: Annex-B 格式（含起始码），可能包含多个 NALU（SPS/PPS/IDR 或 P）
    /// - VP8: 完整 VP8 payload
    fn encode(&mut self, frame: &YuvFrame) -> Result<EncodedFrame, VideoCodecError>;

    /// 请求下一帧为关键帧
    fn request_keyframe(&mut self);

    /// 编解码类型
    fn codec(&self) -> lm_core::CodecType;

    /// 设置目标码率（bps）
    fn set_bitrate(&mut self, _bps: u32) {}

    /// 设置帧率
    fn set_framerate(&mut self, _fps: u32) {}
}

/// 创建视频解码器
pub fn create_decoder(codec: lm_core::CodecType) -> Result<Box<dyn VideoDecoder>, VideoCodecError> {
    match codec {
        #[cfg(feature = "vpx")]
        lm_core::CodecType::Vp8 => Ok(Box::new(crate::vpx_codec::VpxDecoder::new_vp8())),
        #[cfg(feature = "openh264")]
        lm_core::CodecType::H264 => Ok(Box::new(crate::openh264_codec::Openh264Decoder::new())),
        _ => Err(VideoCodecError::Unsupported(format!("{:?}", codec))),
    }
}

/// 创建视频编码器
pub fn create_encoder(
    codec: lm_core::CodecType,
    width: u32,
    height: u32,
) -> Result<Box<dyn VideoEncoder>, VideoCodecError> {
    match codec {
        #[cfg(feature = "vpx")]
        lm_core::CodecType::Vp8 => Ok(Box::new(crate::vpx_codec::VpxEncoder::new_vp8(
            width, height,
        )?)),
        #[cfg(feature = "openh264")]
        lm_core::CodecType::H264 => Ok(Box::new(crate::openh264_codec::Openh264Encoder::new(
            width, height,
        )?)),
        _ => Err(VideoCodecError::Unsupported(format!("{:?}", codec))),
    }
}

#[cfg(feature = "vpx")]
pub mod vpx_codec;

#[cfg(feature = "openh264")]
pub mod openh264_codec;

#[cfg(test)]
mod tests;
