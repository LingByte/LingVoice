//! H.264 解码器/编码器 — 基于 Cisco OpenH264 (openh264 crate)
//!
//! ## 10-bit (High 10 Profile) 支持 [experimental]
//!
//! OpenH264 的 High 10 profile 支持有限。当前 openh264 crate (v0.9) 的
//! `DecodedYUV` 仅暴露 8-bit `&[u8]` 切片，不直接提供 bit depth 字段。
//! 完整的 10-bit 解码需要通过 `raw_api()` 访问 `SBufferInfo` 中的
//! `sSystemBuffer.iFormat` 来判断色深，并从 16-bit 平面提取数据。
//! 此功能标注为 experimental，当前实现保持 8-bit 路径。

use crate::{
    EncodedFrame, EncoderConfig as LmEncoderConfig, VideoCodecError, VideoDecoder, VideoEncoder,
    YuvFrame,
};
use openh264::decoder::{Decoder, DecoderConfig};
use openh264::encoder::{
    BitRate, Encoder, EncoderConfig as Oh264EncConfig, FrameRate, FrameType, RateControlMode,
};
use openh264::formats::YUVSource;
use openh264::{OpenH264API, Timestamp};

fn detect_h264_keyframe(data: &[u8]) -> bool {
    let mut i = 0;
    while i + 4 < data.len() {
        if data[i] == 0 && data[i + 1] == 0 {
            let (start_len, nal_start) = if data[i + 2] == 1 {
                (3, i + 3)
            } else if data[i + 2] == 0 && i + 3 < data.len() && data[i + 3] == 1 {
                (4, i + 4)
            } else {
                i += 1;
                continue;
            };
            if nal_start < data.len() {
                let nal_type = data[nal_start] & 0x1F;
                if nal_type == 5 || nal_type == 7 || nal_type == 8 {
                    return true;
                }
            }
            i += start_len;
        } else {
            i += 1;
        }
    }
    false
}

pub struct Openh264Decoder {
    decoder: Decoder,
}

impl Openh264Decoder {
    pub fn new() -> Result<Self, VideoCodecError> {
        let api = OpenH264API::from_source();
        let config = DecoderConfig::new().debug(false);
        let decoder = Decoder::with_api_config(api, config).map_err(|e| {
            VideoCodecError::DecodeFailed(format!("Failed to create H.264 decoder: {}", e))
        })?;
        Ok(Self { decoder })
    }
}

impl VideoDecoder for Openh264Decoder {
    fn decode(&mut self, data: &[u8], timestamp: u64) -> Result<YuvFrame, VideoCodecError> {
        if data.is_empty() {
            return Err(VideoCodecError::InvalidInput("empty input data".into()));
        }

        let keyframe = detect_h264_keyframe(data);

        let yuv = self
            .decoder
            .decode(data)
            .map_err(|e| VideoCodecError::DecodeFailed(e.to_string()))?;

        let yuv = yuv.ok_or_else(|| VideoCodecError::DecodeFailed("no output frame yet".into()))?;

        let (width, height) = yuv.dimensions();
        if width == 0 || height == 0 {
            return Err(VideoCodecError::DecodeFailed("zero dimensions".into()));
        }

        // 10-bit (High 10 profile) 检测: openh264 crate v0.9 的 DecodedYUV 不暴露
        // bit depth 字段 (SSysMEMBuffer.iFormat 为私有)。完整 10-bit 支持需要
        // 通过 decoder.raw_api() 访问 SBufferInfo，此处保持 8-bit 路径。
        // [experimental] 待 openh264 crate 暴露 bit depth 后实现 y16/u16/v16 填充。

        let (y_stride, u_stride, v_stride) = yuv.strides();
        let uv_w = width / 2;
        let uv_h = height / 2;
        let uv_size = uv_w * uv_h;
        let y_size = width * height;

        let mut y = vec![0u8; y_size];
        let mut u = vec![0u8; uv_size];
        let mut v = vec![0u8; uv_size];

        if y_stride == width {
            y.copy_from_slice(&yuv.y()[..y_size]);
        } else {
            for row in 0..height {
                let src_start = row * y_stride;
                y[row * width..(row + 1) * width]
                    .copy_from_slice(&yuv.y()[src_start..src_start + width]);
            }
        }
        if u_stride == uv_w && v_stride == uv_w {
            u.copy_from_slice(&yuv.u()[..uv_size]);
            v.copy_from_slice(&yuv.v()[..uv_size]);
        } else {
            for row in 0..uv_h {
                let src_u_start = row * u_stride;
                u[row * uv_w..(row + 1) * uv_w]
                    .copy_from_slice(&yuv.u()[src_u_start..src_u_start + uv_w]);
                let src_v_start = row * v_stride;
                v[row * uv_w..(row + 1) * uv_w]
                    .copy_from_slice(&yuv.v()[src_v_start..src_v_start + uv_w]);
            }
        }

        Ok(YuvFrame {
            y,
            u,
            v,
            width: width as u32,
            height: height as u32,
            timestamp,
            keyframe,
            bit_depth: 8,
            y16: Vec::new(),
            u16: Vec::new(),
            v16: Vec::new(),
        })
    }

    fn codec(&self) -> lm_core::CodecType {
        lm_core::CodecType::H264
    }

    fn decode_with_pool(
        &mut self,
        data: &[u8],
        timestamp: u64,
        pool: &crate::YuvFramePool,
    ) -> Result<YuvFrame, VideoCodecError> {
        if data.is_empty() {
            return Err(VideoCodecError::InvalidInput("empty input data".into()));
        }

        let keyframe = detect_h264_keyframe(data);

        let yuv = self
            .decoder
            .decode(data)
            .map_err(|e| VideoCodecError::DecodeFailed(e.to_string()))?;
        let yuv = yuv.ok_or_else(|| VideoCodecError::DecodeFailed("no output frame yet".into()))?;

        let (width, height) = yuv.dimensions();
        if width == 0 || height == 0 {
            return Err(VideoCodecError::DecodeFailed("zero dimensions".into()));
        }

        let (y_stride, u_stride, v_stride) = yuv.strides();
        let uv_w = width / 2;
        let uv_h = height / 2;

        let mut frame = pool.acquire(width as u32, height as u32, timestamp);

        if y_stride == width {
            frame.y.copy_from_slice(&yuv.y()[..width * height]);
        } else {
            for row in 0..height {
                let src_start = row * y_stride;
                frame.y[row * width..(row + 1) * width]
                    .copy_from_slice(&yuv.y()[src_start..src_start + width]);
            }
        }
        if u_stride == uv_w && v_stride == uv_w {
            frame.u.copy_from_slice(&yuv.u()[..uv_w * uv_h]);
            frame.v.copy_from_slice(&yuv.v()[..uv_w * uv_h]);
        } else {
            for row in 0..uv_h {
                let src_u_start = row * u_stride;
                frame.u[row * uv_w..(row + 1) * uv_w]
                    .copy_from_slice(&yuv.u()[src_u_start..src_u_start + uv_w]);
                let src_v_start = row * v_stride;
                frame.v[row * uv_w..(row + 1) * uv_w]
                    .copy_from_slice(&yuv.v()[src_v_start..src_v_start + uv_w]);
            }
        }

        frame.keyframe = keyframe;
        Ok(frame)
    }
}

impl YUVSource for YuvFrame {
    fn dimensions(&self) -> (usize, usize) {
        (self.width as usize, self.height as usize)
    }

    fn strides(&self) -> (usize, usize, usize) {
        (self.y_stride(), self.uv_stride(), self.uv_stride())
    }

    fn y(&self) -> &[u8] {
        &self.y
    }

    fn u(&self) -> &[u8] {
        &self.u
    }

    fn v(&self) -> &[u8] {
        &self.v
    }
}

pub struct Openh264Encoder {
    encoder: Encoder,
    config: LmEncoderConfig,
    force_keyframe: bool,
}

impl Openh264Encoder {
    pub fn new(config: LmEncoderConfig) -> Result<Self, VideoCodecError> {
        // 10-bit (High 10 profile) 编码支持 [experimental]:
        // OpenH264 编码器的 10-bit 支持有限，当前 openh264 crate v0.9 的
        // EncoderConfig 不直接暴露 profile 设置。完整 High 10 编码需要
        // 通过 raw API 设置 eSpsPpsIdStrategy 和 profile_idc。
        if config.width == 0 || config.height == 0 {
            return Err(VideoCodecError::InvalidInput(
                "width and height must be non-zero".into(),
            ));
        }

        let api = OpenH264API::from_source();
        let enc_config = Oh264EncConfig::new()
            .max_frame_rate(FrameRate::from_hz(config.framerate as f32))
            .bitrate(BitRate::from_bps(config.bitrate))
            .rate_control_mode(RateControlMode::Bitrate);

        let encoder = Encoder::with_api_config(api, enc_config).map_err(|e| {
            VideoCodecError::EncodeFailed(format!("Failed to create H.264 encoder: {}", e))
        })?;

        Ok(Self {
            encoder,
            config,
            force_keyframe: false,
        })
    }
}

impl VideoEncoder for Openh264Encoder {
    fn encode(&mut self, frame: &YuvFrame) -> Result<EncodedFrame, VideoCodecError> {
        if frame.width != self.config.width || frame.height != self.config.height {
            return Err(VideoCodecError::InvalidInput(format!(
                "frame dimensions {}x{} != encoder {}x{}",
                frame.width, frame.height, self.config.width, self.config.height
            )));
        }

        let timestamp = Timestamp::from_millis(frame.timestamp / 90);

        if self.force_keyframe {
            self.force_keyframe = false;
            self.encoder.force_intra_frame();
        }

        let stream = self
            .encoder
            .encode_at(frame, timestamp)
            .map_err(|e| VideoCodecError::EncodeFailed(e.to_string()))?;

        let keyframe = stream.frame_type() == FrameType::IDR || stream.frame_type() == FrameType::I;

        let mut data = Vec::new();
        for layer_idx in 0..stream.num_layers() {
            if let Some(layer) = stream.layer(layer_idx) {
                for nal_idx in 0..layer.nal_count() {
                    if let Some(nal) = layer.nal_unit(nal_idx) {
                        data.extend_from_slice(nal);
                    }
                }
            }
        }

        if data.is_empty() {
            return Err(VideoCodecError::EncodeFailed("empty output".into()));
        }

        Ok(EncodedFrame {
            data: bytes::Bytes::from(data),
            width: frame.width,
            height: frame.height,
            keyframe,
            timestamp: frame.timestamp,
        })
    }

    fn request_keyframe(&mut self) {
        self.force_keyframe = true;
    }

    fn codec(&self) -> lm_core::CodecType {
        lm_core::CodecType::H264
    }

    fn set_bitrate(&mut self, bps: u32) {
        self.config.bitrate = bps;
    }

    fn set_framerate(&mut self, fps: u32) {
        self.config.framerate = fps;
    }
}
