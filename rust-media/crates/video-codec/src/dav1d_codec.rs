//! AV1 解码器 — FFI 绑定系统 dav1d 库
//!
//! dav1d 是 VideoLAN 开发的 AV1 解码器，目前最快的 AV1 软解码器。
//! 本模块通过 FFI 直接调用 dav1d C API。
//!
//! AV1 编码使用 libaom（见 aom_codec.rs）。

use crate::{VideoCodecError, VideoDecoder, YuvFrame};
use libc::{c_int, c_void, ptrdiff_t, size_t};
use std::ptr;

const DAV1D_PIXEL_LAYOUT_I420: c_int = 1;

#[repr(C)]
struct Dav1dDataProps {
    timestamp: i64,
    duration: i64,
    offset: i64,
    size: size_t,
    user_data: *mut c_void,
}

#[repr(C)]
struct Dav1dPicAllocator {
    alloc_pic_callback: *mut c_void,
    release_pic_callback: *mut c_void,
    cookie: *mut c_void,
}

#[repr(C)]
struct Dav1dLogger {
    callback: *mut c_void,
    cookie: *mut c_void,
}

#[repr(C)]
struct Dav1dSettings {
    n_threads: c_int,
    max_frame_delay: c_int,
    apply_grain: c_int,
    operating_point: c_int,
    all_layers: c_int,
    frame_size_limit: u32,
    allocator: Dav1dPicAllocator,
    logger: Dav1dLogger,
    strict_std_compliance: c_int,
    output_invisible_frames: c_int,
    skip_frame_refs: c_int,
}

#[repr(C)]
struct Dav1dPictureParameters {
    w: c_int,
    h: c_int,
    layout: c_int,
    bpc: c_int,
}

#[repr(C)]
struct Dav1dPicture {
    seq_hdr: *mut c_void,
    frame_hdr: *mut c_void,
    data: [*mut c_void; 3],
    stride: [ptrdiff_t; 2],
    p: Dav1dPictureParameters,
    m: Dav1dDataProps,
    content_light: *mut c_void,
    mastering_display: *mut c_void,
    itut_t35: *mut c_void,
    reserved: [*mut c_void; 4],
    pic_ref: *mut c_void,
}

#[repr(C)]
struct Dav1dData {
    data: *const u8,
    sz: size_t,
    data_ref: *mut c_void,
    m: Dav1dDataProps,
}

#[repr(C)]
struct Dav1dContext {
    _opaque: [u8; 0],
}

extern "C" {
    fn dav1d_default_settings(s: *mut Dav1dSettings);
    fn dav1d_open(c_out: *mut *mut Dav1dContext, s: *const Dav1dSettings) -> c_int;
    fn dav1d_send_data(c: *mut Dav1dContext, r#in: *mut Dav1dData) -> c_int;
    fn dav1d_get_picture(c: *mut Dav1dContext, out: *mut Dav1dPicture) -> c_int;
    fn dav1d_close(c_out: *mut *mut Dav1dContext);
    #[allow(dead_code)]
    fn dav1d_flush(c: *mut Dav1dContext);
    fn dav1d_data_create(data: *mut Dav1dData, sz: size_t) -> *mut u8;
    fn dav1d_data_unref(data: *mut Dav1dData);
    fn dav1d_picture_unref(p: *mut Dav1dPicture);
}

pub struct Dav1dDecoder {
    ctx: *mut Dav1dContext,
}

unsafe impl Send for Dav1dDecoder {}
unsafe impl Sync for Dav1dDecoder {}

impl Dav1dDecoder {
    pub fn new() -> Result<Self, VideoCodecError> {
        unsafe {
            let mut settings: Dav1dSettings = std::mem::zeroed();
            dav1d_default_settings(&mut settings);
            settings.max_frame_delay = 1;
            settings.n_threads = 0;

            let mut ctx: *mut Dav1dContext = ptr::null_mut();
            let ret = dav1d_open(&mut ctx, &settings);
            if ret != 0 {
                return Err(VideoCodecError::DecodeFailed(format!(
                    "dav1d_open failed: {}",
                    ret
                )));
            }

            Ok(Self { ctx })
        }
    }
}

impl Drop for Dav1dDecoder {
    fn drop(&mut self) {
        unsafe {
            if !self.ctx.is_null() {
                let mut ctx = self.ctx;
                dav1d_close(&mut ctx);
                self.ctx = ptr::null_mut();
            }
        }
    }
}

impl VideoDecoder for Dav1dDecoder {
    fn decode(&mut self, data: &[u8], timestamp: u64) -> Result<YuvFrame, VideoCodecError> {
        if data.is_empty() {
            return Err(VideoCodecError::InvalidInput("empty input data".into()));
        }

        unsafe {
            let mut dav1d_data: Dav1dData = std::mem::zeroed();
            let buf_ptr = dav1d_data_create(&mut dav1d_data, data.len());
            if buf_ptr.is_null() {
                return Err(VideoCodecError::DecodeFailed(
                    "dav1d_data_create failed".into(),
                ));
            }
            ptr::copy_nonoverlapping(data.as_ptr(), buf_ptr, data.len());

            let send_ret = dav1d_send_data(self.ctx, &mut dav1d_data);
            dav1d_data_unref(&mut dav1d_data);

            if send_ret < 0 && send_ret != libc::EAGAIN {
                return Err(VideoCodecError::DecodeFailed(format!(
                    "dav1d_send_data failed: {}",
                    send_ret
                )));
            }

            let mut pic: Dav1dPicture = std::mem::zeroed();
            let ret = dav1d_get_picture(self.ctx, &mut pic);

            if ret == libc::EAGAIN {
                dav1d_picture_unref(&mut pic);
                return Err(VideoCodecError::DecodeFailed("no output frame yet".into()));
            }
            if ret < 0 {
                dav1d_picture_unref(&mut pic);
                return Err(VideoCodecError::DecodeFailed(format!(
                    "dav1d_get_picture failed: {}",
                    ret
                )));
            }

            let width = pic.p.w as u32;
            let height = pic.p.h as u32;
            debug_assert!(
                width > 0 && height > 0,
                "dav1d decoded frame has zero dimensions"
            );

            if pic.p.layout != DAV1D_PIXEL_LAYOUT_I420 {
                dav1d_picture_unref(&mut pic);
                return Err(VideoCodecError::DecodeFailed(format!(
                    "unsupported pixel layout: {} (only I420 supported)",
                    pic.p.layout
                )));
            }

            if pic.p.bpc != 8 {
                dav1d_picture_unref(&mut pic);
                return Err(VideoCodecError::DecodeFailed(format!(
                    "unsupported bit depth: {} (only 8-bit supported)",
                    pic.p.bpc
                )));
            }

            let y_stride = pic.stride[0] as usize;
            let uv_stride = pic.stride[1] as usize;
            let uv_w = (width / 2) as usize;
            let uv_h = (height / 2) as usize;

            let mut y = vec![0u8; (width * height) as usize];
            let mut u = vec![0u8; uv_w * uv_h];
            let mut v = vec![0u8; uv_w * uv_h];

            let y_ptr = pic.data[0] as *const u8;
            let u_ptr = pic.data[1] as *const u8;
            let v_ptr = pic.data[2] as *const u8;

            for row in 0..height as usize {
                let src = std::slice::from_raw_parts(y_ptr.add(row * y_stride), width as usize);
                y[row * width as usize..(row + 1) * width as usize].copy_from_slice(src);
            }
            for row in 0..uv_h {
                let src_u = std::slice::from_raw_parts(u_ptr.add(row * uv_stride), uv_w);
                u[row * uv_w..(row + 1) * uv_w].copy_from_slice(src_u);
                let src_v = std::slice::from_raw_parts(v_ptr.add(row * uv_stride), uv_w);
                v[row * uv_w..(row + 1) * uv_w].copy_from_slice(src_v);
            }

            let keyframe = pic.frame_hdr.is_null()
                || (*(pic.frame_hdr as *const Dav1dFrameHeaderLite)).frame_type == 0;

            dav1d_picture_unref(&mut pic);

            Ok(YuvFrame {
                y,
                u,
                v,
                width,
                height,
                timestamp,
                keyframe,
            })
        }
    }

    fn codec(&self) -> lm_core::CodecType {
        lm_core::CodecType::Av1
    }
}

#[repr(C)]
struct Dav1dFrameHeaderLite {
    frame_type: c_int,
}
