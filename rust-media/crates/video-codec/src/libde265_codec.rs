//! H.265/HEVC 解码器 — FFI 绑定系统 libde265 库
//!
//! libde265 是开源的 H.265/HEVC 解码器。
//! 需要系统安装 libde265-dev >= 1.0。
//!
//! H.265 编码使用 x265（见 x265_codec.rs）。

use crate::{VideoCodecError, VideoDecoder, YuvFrame};
use libde265_sys2::{
    de265_decode, de265_decoder_context, de265_flush_data, de265_free_decoder,
    de265_get_chroma_format, de265_get_image_NAL_header, de265_get_image_height,
    de265_get_image_plane, de265_get_image_width, de265_get_next_picture, de265_image,
    de265_new_decoder, de265_push_data, de265_release_next_picture,
};
use std::os::raw::c_int;
use std::ptr;

const DE265_CHROMA_420: c_int = 1;
const DE265_OK: u32 = 0;
const DE265_ERROR_WAITING_FOR_INPUT_DATA: u32 = 13;

pub struct Libde265Decoder {
    ctx: *mut de265_decoder_context,
}

unsafe impl Send for Libde265Decoder {}
unsafe impl Sync for Libde265Decoder {}

impl Libde265Decoder {
    pub fn new() -> Result<Self, VideoCodecError> {
        let ctx = unsafe { de265_new_decoder() };
        if ctx.is_null() {
            return Err(VideoCodecError::DecodeFailed(
                "de265_new_decoder failed".into(),
            ));
        }
        Ok(Self { ctx })
    }
}

impl Drop for Libde265Decoder {
    fn drop(&mut self) {
        if !self.ctx.is_null() {
            unsafe { de265_free_decoder(self.ctx) };
            self.ctx = ptr::null_mut();
        }
    }
}

impl VideoDecoder for Libde265Decoder {
    fn decode(&mut self, data: &[u8], timestamp: u64) -> Result<YuvFrame, VideoCodecError> {
        if data.is_empty() {
            return Err(VideoCodecError::InvalidInput("empty input data".into()));
        }

        unsafe {
            let ret = de265_push_data(
                self.ctx,
                data.as_ptr() as *const _,
                data.len() as _,
                0,
                ptr::null_mut(),
            );
            if ret != DE265_OK {
                return Err(VideoCodecError::DecodeFailed(format!(
                    "de265_push_data failed: {}",
                    ret
                )));
            }

            let mut more: c_int = 0;
            let ret = de265_decode(self.ctx, &mut more);
            if ret != DE265_OK && ret != DE265_ERROR_WAITING_FOR_INPUT_DATA {
                return Err(VideoCodecError::DecodeFailed(format!(
                    "de265_decode failed: {}",
                    ret
                )));
            }

            let img = de265_get_next_picture(self.ctx);
            if img.is_null() {
                de265_flush_data(self.ctx);
                let mut more2: c_int = 0;
                let _ = de265_decode(self.ctx, &mut more2);
                let img = de265_get_next_picture(self.ctx);
                if img.is_null() {
                    return Err(VideoCodecError::DecodeFailed("no output frame yet".into()));
                }
                let result = extract_yuv(img, timestamp);
                de265_release_next_picture(self.ctx);
                return result;
            }

            let result = extract_yuv(img, timestamp);
            de265_release_next_picture(self.ctx);
            result
        }
    }

    fn codec(&self) -> lm_core::CodecType {
        lm_core::CodecType::H265
    }
}

unsafe fn extract_yuv(
    img: *const de265_image,
    timestamp: u64,
) -> Result<YuvFrame, VideoCodecError> {
    let width = de265_get_image_width(img, 0) as u32;
    let height = de265_get_image_height(img, 0) as u32;
    if width == 0 || height == 0 {
        return Err(VideoCodecError::DecodeFailed("zero dimensions".into()));
    }

    let chroma = de265_get_chroma_format(img);
    if chroma as c_int != DE265_CHROMA_420 {
        return Err(VideoCodecError::DecodeFailed(format!(
            "unsupported chroma format: {} (only 4:2:0 supported)",
            chroma as c_int
        )));
    }

    let uv_w = (width / 2) as usize;
    let uv_h = (height / 2) as usize;

    let mut y_stride: c_int = 0;
    let mut u_stride: c_int = 0;
    let mut v_stride: c_int = 0;

    let y_ptr = de265_get_image_plane(img, 0, &mut y_stride);
    let u_ptr = de265_get_image_plane(img, 1, &mut u_stride);
    let v_ptr = de265_get_image_plane(img, 2, &mut v_stride);

    if y_ptr.is_null() || u_ptr.is_null() || v_ptr.is_null() {
        return Err(VideoCodecError::DecodeFailed("null plane pointer".into()));
    }

    let y_stride = y_stride as usize;
    let u_stride = u_stride as usize;
    let v_stride = v_stride as usize;
    let w = width as usize;
    let h = height as usize;
    let y_size = w * h;
    let uv_size = uv_w * uv_h;

    let mut y = vec![0u8; y_size];
    let mut u = vec![0u8; uv_size];
    let mut v = vec![0u8; uv_size];

    if y_stride == w {
        let src = std::slice::from_raw_parts(y_ptr, y_size);
        y.copy_from_slice(src);
    } else {
        for row in 0..h {
            let src = std::slice::from_raw_parts(y_ptr.add(row * y_stride), w);
            y[row * w..(row + 1) * w].copy_from_slice(src);
        }
    }
    if u_stride == uv_w && v_stride == uv_w {
        let src_u = std::slice::from_raw_parts(u_ptr, uv_size);
        u.copy_from_slice(src_u);
        let src_v = std::slice::from_raw_parts(v_ptr, uv_size);
        v.copy_from_slice(src_v);
    } else {
        for row in 0..uv_h {
            let src_u = std::slice::from_raw_parts(u_ptr.add(row * u_stride), uv_w);
            u[row * uv_w..(row + 1) * uv_w].copy_from_slice(src_u);
            let src_v = std::slice::from_raw_parts(v_ptr.add(row * v_stride), uv_w);
            v[row * uv_w..(row + 1) * uv_w].copy_from_slice(src_v);
        }
    }

    // Detect keyframe from NAL unit type: 19=IDR_W_RADL, 20=IDR_N_LP, 21=CRA
    let mut nal_type: c_int = 0;
    de265_get_image_NAL_header(
        img,
        &mut nal_type,
        std::ptr::null_mut(),
        std::ptr::null_mut(),
        std::ptr::null_mut(),
    );
    let is_keyframe = (19..=21).contains(&nal_type);

    Ok(YuvFrame {
        y,
        u,
        v,
        width,
        height,
        timestamp,
        keyframe: is_keyframe,
    })
}
