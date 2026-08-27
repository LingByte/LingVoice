//! H.265/HEVC 解码器 — FFI 绑定系统 libde265 库
//!
//! libde265 是开源的 H.265/HEVC 解码器。
//! 需要系统安装 libde265-dev >= 1.0。
//!
//! H.265 编码使用 x265（见 x265_codec.rs）。

use crate::{VideoCodecError, VideoDecoder, YuvFrame};
use libde265_sys2::{
    de265_chroma_format, de265_decode, de265_error, de265_flush_data, de265_free_decoder,
    de265_get_chroma_format, de265_get_image_height, de265_get_image_plane, de265_get_image_width,
    de265_get_next_picture, de265_new_decoder, de265_push_data, de265_release_next_picture,
};
use std::os::raw::c_int;
use std::ptr;

const DE265_CHROMA_420: c_int = 1;

pub struct Libde265Decoder {
    ctx: *mut libde265_sys2::de265_decoder_context,
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
            if !is_de265_ok(ret) {
                return Err(VideoCodecError::DecodeFailed(format!(
                    "de265_push_data failed: {:?}",
                    ret
                )));
            }

            let mut more: c_int = 0;
            let ret = de265_decode(self.ctx, &mut more);
            if !is_de265_ok(ret) && ret != de265_error::DE265_ERROR_WAITING_FOR_INPUT_DATA {
                return Err(VideoCodecError::DecodeFailed(format!(
                    "de265_decode failed: {:?}",
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
                return extract_yuv(img, timestamp);
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
    img: *mut libde265_sys2::de265_image,
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
            "unsupported chroma format: {:?} (only 4:2:0 supported)",
            chroma
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

    let mut y = vec![0u8; w * h];
    let mut u = vec![0u8; uv_w * uv_h];
    let mut v = vec![0u8; uv_w * uv_h];

    for row in 0..h {
        let src = std::slice::from_raw_parts(y_ptr.add(row * y_stride), w);
        y[row * w..(row + 1) * w].copy_from_slice(src);
    }
    for row in 0..uv_h {
        let src_u = std::slice::from_raw_parts(u_ptr.add(row * u_stride), uv_w);
        u[row * uv_w..(row + 1) * uv_w].copy_from_slice(src_u);
        let src_v = std::slice::from_raw_parts(v_ptr.add(row * v_stride), uv_w);
        v[row * uv_w..(row + 1) * uv_w].copy_from_slice(src_v);
    }

    Ok(YuvFrame {
        y,
        u,
        v,
        width,
        height,
        timestamp,
        keyframe: true,
    })
}

fn is_de265_ok(err: de265_error) -> bool {
    matches!(err, de265_error::DE265_OK)
}
