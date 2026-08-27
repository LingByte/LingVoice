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
///
/// 支持 8-bit 和 10-bit 色深。8-bit 时数据存储在 y/u/v (Vec<u8>)，
/// 10-bit 时数据存储在 y16/u16/v16 (Vec<u16>)，bit_depth 字段标识当前色深。
#[derive(Debug, Clone)]
pub struct YuvFrame {
    pub y: Vec<u8>,
    pub u: Vec<u8>,
    pub v: Vec<u8>,
    pub width: u32,
    pub height: u32,
    pub timestamp: u64,
    pub keyframe: bool,
    /// 色深位数 (8 或 10)。8-bit 时使用 y/u/v，10-bit 时使用 y16/u16/v16。
    pub bit_depth: u8,
    /// 10-bit Y plane (little-endian u16). 仅 bit_depth > 8 时有效。
    pub y16: Vec<u16>,
    /// 10-bit U plane. 仅 bit_depth > 8 时有效。
    pub u16: Vec<u16>,
    /// 10-bit V plane. 仅 bit_depth > 8 时有效。
    pub v16: Vec<u16>,
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
            bit_depth: 8,
            y16: Vec::new(),
            u16: Vec::new(),
            v16: Vec::new(),
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

    /// 创建 10-bit YUV420p 帧（全黑）。
    /// 10-bit 数据存储在 y16/u16/v16 (Vec<u16>)，y/u/v 为空。
    pub fn black_10bit(width: u32, height: u32, timestamp: u64) -> Self {
        debug_assert!(width > 0 && height > 0);
        let y_size = (width * height) as usize;
        let uv_size = ((width / 2) * (height / 2)) as usize;
        Self {
            y: Vec::new(),
            u: Vec::new(),
            v: Vec::new(),
            width,
            height,
            timestamp,
            keyframe: true,
            bit_depth: 10,
            y16: vec![0u16; y_size],
            u16: vec![512u16; uv_size],
            v16: vec![512u16; uv_size],
        }
    }

    /// 返回当前帧是否为 10-bit (或更高) 色深。
    #[inline]
    pub fn is_high_bit_depth(&self) -> bool {
        self.bit_depth > 8
    }

    /// 将帧的缓冲区归还到池中以便复用，避免重复分配。
    /// 调用后 self 的 y/u/v 变为空 Vec。
    pub fn recycle_buffers(&mut self, pool: &YuvFramePool) {
        let y_size = self.y_size();
        let uv_size = self.uv_size();
        let y = std::mem::take(&mut self.y);
        let u = std::mem::take(&mut self.u);
        let v = std::mem::take(&mut self.v);
        pool.return_buffers(self.width, self.height, (y, u, v), (y_size, uv_size));
    }
}

/// YUV 帧缓冲池 — 复用 Vec<u8> 缓冲区减少堆分配。
///
/// 在高频转码场景（每秒 30-60 帧）中，每帧 YUV420p 数据的
/// 分配/释放开销显著。缓冲池按分辨率缓存 (y, u, v) 三个 Vec，
/// 解码器从池中获取，编码器用完后归还。
///
/// 池内部用 Mutex 保护，线程安全。每个分辨率最多缓存 8 组缓冲区。
pub struct YuvFramePool {
    inner: std::sync::Mutex<YuvFramePoolInner>,
}

struct YuvFramePoolInner {
    /// 按 (width, height) 分组的空闲缓冲区
    buffers: std::collections::HashMap<(u32, u32), Vec<BufferSet>>,
    /// 每个分辨率最多缓存的缓冲区组数
    max_per_resolution: usize,
}

struct BufferSet {
    y: Vec<u8>,
    u: Vec<u8>,
    v: Vec<u8>,
    #[allow(dead_code)]
    y_capacity: usize,
    #[allow(dead_code)]
    uv_capacity: usize,
}

impl Default for YuvFramePool {
    fn default() -> Self {
        Self::new(8)
    }
}

impl YuvFramePool {
    /// 创建缓冲池，指定每个分辨率最多缓存的缓冲区组数。
    pub fn new(max_per_resolution: usize) -> Self {
        Self {
            inner: std::sync::Mutex::new(YuvFramePoolInner {
                buffers: std::collections::HashMap::new(),
                max_per_resolution,
            }),
        }
    }

    /// 从池中获取一个 YuvFrame，缓冲区已预分配到正确大小。
    /// 如果池中没有匹配分辨率的缓冲区，则新分配。
    pub fn acquire(&self, width: u32, height: u32, timestamp: u64) -> YuvFrame {
        let y_size = (width * height) as usize;
        let uv_size = ((width / 2) * (height / 2)) as usize;

        let (y, u, v) = {
            let mut inner = self.inner.lock().unwrap();
            if let Some(free_list) = inner.buffers.get_mut(&(width, height)) {
                if let Some(bufset) = free_list.pop() {
                    let mut y = bufset.y;
                    let mut u = bufset.u;
                    let mut v = bufset.v;
                    y.resize(y_size, 0);
                    u.resize(uv_size, 128);
                    v.resize(uv_size, 128);
                    return YuvFrame {
                        y,
                        u,
                        v,
                        width,
                        height,
                        timestamp,
                        keyframe: true,
                        bit_depth: 8,
                        y16: Vec::new(),
                        u16: Vec::new(),
                        v16: Vec::new(),
                    };
                }
            }
            (
                vec![0u8; y_size],
                vec![128u8; uv_size],
                vec![128u8; uv_size],
            )
        };

        YuvFrame {
            y,
            u,
            v,
            width,
            height,
            timestamp,
            keyframe: true,
            bit_depth: 8,
            y16: Vec::new(),
            u16: Vec::new(),
            v16: Vec::new(),
        }
    }

    /// 归还缓冲区到池中以便复用。
    fn return_buffers(
        &self,
        width: u32,
        height: u32,
        bufs: (Vec<u8>, Vec<u8>, Vec<u8>),
        capacities: (usize, usize),
    ) {
        let mut inner = self.inner.lock().unwrap();
        let max_per_resolution = inner.max_per_resolution;
        let free_list = inner.buffers.entry((width, height)).or_default();
        if free_list.len() >= max_per_resolution {
            return; // 池已满，丢弃缓冲区
        }
        free_list.push(BufferSet {
            y: bufs.0,
            u: bufs.1,
            v: bufs.2,
            y_capacity: capacities.0,
            uv_capacity: capacities.1,
        });
    }

    /// 清空池中所有缓冲区，释放内存。
    pub fn clear(&self) {
        let mut inner = self.inner.lock().unwrap();
        inner.buffers.clear();
    }

    /// 返回池中当前缓存的缓冲区组数。
    pub fn pooled_count(&self) -> usize {
        let inner = self.inner.lock().unwrap();
        inner.buffers.values().map(|v| v.len()).sum()
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

    /// 使用缓冲池解码，减少堆分配。池提供预分配的 Vec 缓冲区。
    /// 默认实现调用 decode() 然后复制到池帧中，解码器可覆盖以直接写入池缓冲区。
    fn decode_with_pool(
        &mut self,
        data: &[u8],
        timestamp: u64,
        pool: &YuvFramePool,
    ) -> Result<YuvFrame, VideoCodecError> {
        let frame = self.decode(data, timestamp)?;
        // 从池获取帧并复制数据
        let mut pooled = pool.acquire(frame.width, frame.height, timestamp);
        pooled.y.copy_from_slice(&frame.y);
        pooled.u.copy_from_slice(&frame.u);
        pooled.v.copy_from_slice(&frame.v);
        pooled.keyframe = frame.keyframe;
        Ok(pooled)
    }
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
        #[cfg(all(feature = "videotoolbox", target_os = "macos"))]
        lm_core::CodecType::H264 => {
            match crate::videotoolbox_codec::VideoToolboxEncoder::new_h264(config.clone()) {
                Ok(enc) => Ok(Box::new(enc)),
                Err(_) => {
                    #[cfg(feature = "openh264")]
                    {
                        Ok(Box::new(crate::openh264_codec::Openh264Encoder::new(
                            config,
                        )?))
                    }
                    #[cfg(not(feature = "openh264"))]
                    {
                        Err(VideoCodecError::Unsupported(
                            "H264 encoder not available".into(),
                        ))
                    }
                }
            }
        }
        #[cfg(all(not(feature = "videotoolbox"), feature = "openh264"))]
        lm_core::CodecType::H264 => Ok(Box::new(crate::openh264_codec::Openh264Encoder::new(
            config,
        )?)),
        #[cfg(all(
            feature = "videotoolbox",
            target_os = "macos",
            not(feature = "openh264")
        ))]
        lm_core::CodecType::H264 => Ok(Box::new(
            crate::videotoolbox_codec::VideoToolboxEncoder::new_h264(config)?,
        )),
        // H.265: always use x265 (software) for reliability.
        // VideoToolbox HEVC encoder is not reliable on all macOS versions.
        #[cfg(feature = "x265")]
        lm_core::CodecType::H265 => Ok(Box::new(crate::x265_codec::X265Encoder::new(config)?)),
        #[cfg(all(feature = "videotoolbox", target_os = "macos", not(feature = "x265")))]
        lm_core::CodecType::H265 => Ok(Box::new(
            crate::videotoolbox_codec::VideoToolboxEncoder::new_h265(config)?,
        )),
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

/// 编码器后端类型
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EncoderBackend {
    /// 纯软件编码
    Software,
    /// macOS VideoToolbox 硬件编码
    VideoToolbox,
    /// Linux NVENC 硬件编码
    Nvenc,
}

/// 检测指定 codec 是否有硬件加速编码器可用。
///
/// 返回硬件后端类型，如果只有软件编码器则返回 None。
/// 这是运行时检测，会实际尝试创建硬件编码器实例。
pub fn hardware_encoder_available(codec: lm_core::CodecType) -> Option<EncoderBackend> {
    match codec {
        lm_core::CodecType::H264 => {
            #[cfg(all(feature = "videotoolbox", target_os = "macos"))]
            {
                let config = EncoderConfig::new(64, 64);
                if crate::videotoolbox_codec::VideoToolboxEncoder::new_h264(config).is_ok() {
                    return Some(EncoderBackend::VideoToolbox);
                }
            }
            #[cfg(all(feature = "nvenc", target_os = "linux"))]
            {
                let config = EncoderConfig::new(64, 64);
                if crate::nvenc_codec::NvencEncoder::new_h264(config).is_ok() {
                    return Some(EncoderBackend::Nvenc);
                }
            }
            None
        }
        lm_core::CodecType::H265 => {
            #[cfg(all(feature = "videotoolbox", target_os = "macos"))]
            {
                let config = EncoderConfig::new(64, 64);
                if crate::videotoolbox_codec::VideoToolboxEncoder::new_h265(config).is_ok() {
                    return Some(EncoderBackend::VideoToolbox);
                }
            }
            None
        }
        _ => None,
    }
}

/// 创建编码器，优先使用硬件加速，失败时自动回退到软件编码。
///
/// `prefer_hardware`: true 时优先尝试硬件编码器，false 时直接使用软件编码器。
pub fn create_encoder_auto(
    codec: lm_core::CodecType,
    width: u32,
    height: u32,
    prefer_hardware: bool,
) -> Result<Box<dyn VideoEncoder>, VideoCodecError> {
    if prefer_hardware {
        if let Some(backend) = hardware_encoder_available(codec) {
            let config = EncoderConfig::new(width, height);
            match backend {
                EncoderBackend::VideoToolbox => {
                    #[cfg(all(feature = "videotoolbox", target_os = "macos"))]
                    {
                        match codec {
                            lm_core::CodecType::H264 => {
                                if let Ok(enc) =
                                    crate::videotoolbox_codec::VideoToolboxEncoder::new_h264(config)
                                {
                                    return Ok(Box::new(enc));
                                }
                            }
                            lm_core::CodecType::H265 => {
                                if let Ok(enc) =
                                    crate::videotoolbox_codec::VideoToolboxEncoder::new_h265(config)
                                {
                                    return Ok(Box::new(enc));
                                }
                            }
                            _ => {}
                        }
                    }
                }
                EncoderBackend::Nvenc => {
                    #[cfg(all(feature = "nvenc", target_os = "linux"))]
                    {
                        if let Ok(enc) = crate::nvenc_codec::NvencEncoder::new_h264(config) {
                            return Ok(Box::new(enc));
                        }
                    }
                }
                EncoderBackend::Software => {
                    // No hardware backend, fall through to software
                }
            }
            // Hardware failed, fall through to software
        }
    }
    // Fallback to default (which already has hardware-first logic for H264)
    create_encoder(codec, width, height)
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

#[cfg(all(feature = "videotoolbox", target_os = "macos"))]
pub mod videotoolbox_codec;

#[cfg(all(feature = "nvenc", target_os = "linux"))]
pub mod nvenc_codec;

#[cfg(test)]
mod tests;
