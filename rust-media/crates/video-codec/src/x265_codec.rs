//! H.265/HEVC 编码器 — FFI 绑定系统 x265 库
//!
//! x265 是开源的 H.265/HEVC 编码器，广泛用于视频编码。
//! 本模块通过 FFI 直接调用 x265 C API。
//!
//! 解码使用 libde265（见 libde265_codec.rs）。

use crate::{EncodedFrame, EncoderConfig, VideoCodecError, VideoEncoder, YuvFrame};
use libc::{c_char, c_int, c_void};
use std::ffi::CString;
use std::ptr;

#[allow(dead_code)]
const X265_BUILD: c_int = 215;

const X265_TYPE_AUTO: c_int = 0x0000;
const X265_TYPE_IDR: c_int = 0x0001;
const X265_CSP_I420: c_int = 1;

#[repr(C)]
struct X265Nal {
    nal_type: u32,
    size_bytes: u32,
    payload: *mut u8,
}

#[repr(C)]
struct X265Picture {
    pts: i64,
    dts: i64,
    vbv_end_flag: c_int,
    user_data: *mut c_void,
    planes: [*mut c_void; 4],
    stride: [c_int; 4],
    bit_depth: c_int,
    slice_type: c_int,
    poc: c_int,
    color_space: c_int,
    force_qp: c_int,
    forced_pic: c_int,
    _padding: [c_int; 12],
}

#[repr(C)]
struct X265Param {
    _opaque: [u8; 0],
}

extern "C" {
    fn x265_param_alloc() -> *mut X265Param;
    fn x265_param_free(p: *mut X265Param);
    fn x265_param_parse(p: *mut X265Param, name: *const c_char, value: *const c_char) -> c_int;
    fn x265_picture_alloc() -> *mut X265Picture;
    fn x265_picture_free(p: *mut X265Picture);
    fn x265_picture_init(param: *mut X265Param, pic: *mut X265Picture);
    fn x265_encoder_open_215(param: *mut X265Param) -> *mut c_void;
    fn x265_encoder_encode(
        encoder: *mut c_void,
        pp_nal: *mut *mut X265Nal,
        pi_nal: *mut u32,
        pic_in: *mut X265Picture,
        pic_out: *mut X265Picture,
    ) -> c_int;
    fn x265_encoder_close(encoder: *mut c_void);
    #[allow(dead_code)]
    fn x265_encoder_intra_refresh(encoder: *mut c_void);
}

pub struct X265Encoder {
    encoder: *mut c_void,
    param: *mut X265Param,
    pic: *mut X265Picture,
    config: EncoderConfig,
    force_keyframe: bool,
}

unsafe impl Send for X265Encoder {}
unsafe impl Sync for X265Encoder {}

impl X265Encoder {
    pub fn new(config: EncoderConfig) -> Result<Self, VideoCodecError> {
        if config.width == 0 || config.height == 0 {
            return Err(VideoCodecError::InvalidInput(
                "width and height must be non-zero".into(),
            ));
        }

        unsafe {
            let param = x265_param_alloc();
            if param.is_null() {
                return Err(VideoCodecError::EncodeFailed(
                    "x265_param_alloc failed".into(),
                ));
            }

            let parse =
                |p: *mut X265Param, name: &str, value: &str| -> Result<(), VideoCodecError> {
                    let c_name = CString::new(name).unwrap();
                    let c_value = CString::new(value).unwrap();
                    let ret = x265_param_parse(p, c_name.as_ptr(), c_value.as_ptr());
                    if ret != 0 {
                        return Err(VideoCodecError::EncodeFailed(format!(
                            "x265_param_parse({}={}) failed: {}",
                            name, value, ret
                        )));
                    }
                    Ok(())
                };

            parse(param, "log-level", "error")?;
            parse(param, "preset", "ultrafast")?;
            parse(param, "tune", "zerolatency")?;
            parse(param, "width", &config.width.to_string())?;
            parse(param, "height", &config.height.to_string())?;
            parse(param, "bitrate", &config.bitrate.to_string())?;
            parse(param, "fps", &config.framerate.to_string())?;
            parse(param, "keyint", &config.keyframe_interval.to_string())?;
            parse(param, "min-keyint", &config.keyframe_interval.to_string())?;
            parse(param, "bframes", "0")?;
            parse(param, "b-adapt", "0")?;
            parse(param, "ref", "1")?;
            parse(param, "threads", &config.threads.to_string())?;
            parse(param, "input-csp", "i420")?;
            parse(param, "repeat-headers", "1")?;

            let encoder = x265_encoder_open_215(param);
            if encoder.is_null() {
                x265_param_free(param);
                return Err(VideoCodecError::EncodeFailed(
                    "x265_encoder_open failed".into(),
                ));
            }

            let pic = x265_picture_alloc();
            if pic.is_null() {
                x265_encoder_close(encoder);
                x265_param_free(param);
                return Err(VideoCodecError::EncodeFailed(
                    "x265_picture_alloc failed".into(),
                ));
            }
            x265_picture_init(param, pic);
            (*pic).color_space = X265_CSP_I420;
            (*pic).bit_depth = 8;

            Ok(Self {
                encoder,
                param,
                pic,
                config,
                force_keyframe: false,
            })
        }
    }
}

impl Drop for X265Encoder {
    fn drop(&mut self) {
        unsafe {
            if !self.encoder.is_null() {
                x265_encoder_close(self.encoder);
            }
            if !self.pic.is_null() {
                x265_picture_free(self.pic);
            }
            if !self.param.is_null() {
                x265_param_free(self.param);
            }
        }
    }
}

impl VideoEncoder for X265Encoder {
    fn encode(&mut self, frame: &YuvFrame) -> Result<EncodedFrame, VideoCodecError> {
        if frame.width != self.config.width || frame.height != self.config.height {
            return Err(VideoCodecError::InvalidInput(format!(
                "frame dimensions {}x{} != encoder {}x{}",
                frame.width, frame.height, self.config.width, self.config.height
            )));
        }

        unsafe {
            (*self.pic).pts = frame.timestamp as i64;
            (*self.pic).planes[0] = frame.y.as_ptr() as *mut c_void;
            (*self.pic).planes[1] = frame.u.as_ptr() as *mut c_void;
            (*self.pic).planes[2] = frame.v.as_ptr() as *mut c_void;
            (*self.pic).stride[0] = frame.y_stride() as c_int;
            (*self.pic).stride[1] = frame.uv_stride() as c_int;
            (*self.pic).stride[2] = frame.uv_stride() as c_int;
            (*self.pic).bit_depth = 8;
            (*self.pic).color_space = X265_CSP_I420;

            if self.force_keyframe {
                self.force_keyframe = false;
                (*self.pic).slice_type = X265_TYPE_IDR;
            } else {
                (*self.pic).slice_type = X265_TYPE_AUTO;
            }

            let mut pp_nal: *mut X265Nal = ptr::null_mut();
            let mut pi_nal: u32 = 0;
            let mut pic_out: X265Picture = std::mem::zeroed();

            let ret = x265_encoder_encode(
                self.encoder,
                &mut pp_nal,
                &mut pi_nal,
                self.pic,
                &mut pic_out,
            );

            if ret < 0 {
                return Err(VideoCodecError::EncodeFailed(format!(
                    "x265_encoder_encode failed: {}",
                    ret
                )));
            }

            if pi_nal == 0 || pp_nal.is_null() {
                return Err(VideoCodecError::EncodeFailed("no NAL output".into()));
            }

            // Pre-allocate based on estimated compressed size
            let estimated_size = (self.config.width * self.config.height / 4) as usize;
            let mut data = Vec::with_capacity(estimated_size.max(1024));
            let mut is_keyframe = false;

            for i in 0..pi_nal as usize {
                let nal = &*pp_nal.add(i);
                if nal.size_bytes > 0 && !nal.payload.is_null() {
                    let slice = std::slice::from_raw_parts(nal.payload, nal.size_bytes as usize);
                    data.extend_from_slice(slice);
                    // H.265 keyframe NAL types: 19=IDR_W_RADL, 20=IDR_N_LP, 21=CRA
                    if nal.nal_type >= 19 && nal.nal_type <= 21 {
                        is_keyframe = true;
                    }
                }
            }

            if data.is_empty() {
                return Err(VideoCodecError::EncodeFailed("empty NAL data".into()));
            }

            Ok(EncodedFrame {
                data: bytes::Bytes::from(data),
                width: self.config.width,
                height: self.config.height,
                keyframe: is_keyframe,
                timestamp: frame.timestamp,
            })
        }
    }

    fn request_keyframe(&mut self) {
        self.force_keyframe = true;
    }

    fn codec(&self) -> lm_core::CodecType {
        lm_core::CodecType::H265
    }

    fn set_bitrate(&mut self, bps: u32) {
        self.config.bitrate = bps;
    }

    fn set_framerate(&mut self, fps: u32) {
        self.config.framerate = fps;
    }
}
