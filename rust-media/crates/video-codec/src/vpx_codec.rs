//! VP8/VP9 解码器/编码器 — 直接 FFI 绑定 libvpx
//!
//! libvpx 是 Google 的 VP8/VP9 编解码库，WebRTC 默认使用。
//! 本模块通过 FFI 直接调用 libvpx C API，无需 nightly Rust。

use crate::{EncodedFrame, EncoderConfig, VideoCodecError, VideoDecoder, VideoEncoder, YuvFrame};
use libc::{c_char, c_int, c_uint, c_void};
use std::ptr;

#[repr(C)]
#[derive(Default, Clone, Copy)]
struct VpxRational {
    num: c_int,
    den: c_int,
}

#[repr(C)]
#[derive(Clone, Copy)]
pub struct VpxCodecEncCfg {
    g_usage: c_uint,
    g_threads: c_uint,
    g_profile: c_uint,
    g_w: c_uint,
    g_h: c_uint,
    g_bit_depth: c_uint,
    g_input_bit_depth: c_uint,
    g_timebase: VpxRational,
    g_error_resilient: c_uint,
    g_pass: c_uint,
    g_lag_in_frames: c_uint,
    _rc_pad: [c_uint; 16],
    rc_target_bitrate: c_uint,
    _rc_pad2: [c_uint; 11],
    kf_mode: c_uint,
    kf_min_dist: c_uint,
    kf_max_dist: c_uint,
    _tail: [u8; 332],
}

impl Default for VpxCodecEncCfg {
    fn default() -> Self {
        unsafe { std::mem::zeroed() }
    }
}

#[repr(C)]
pub struct VpxCodecCtx {
    name: *const c_char,
    iface: *mut c_void,
    err: c_int,
    _pad1: c_int,
    err_detail: *const c_char,
    init_flags: i64,
    config: *const c_void,
    priv_: *mut c_void,
}

#[repr(C)]
#[derive(Default, Clone, Copy)]
pub struct VpxImage {
    fmt: c_int,
    cs: c_int,
    range: c_int,
    w: c_uint,
    h: c_uint,
    bit_depth: c_uint,
    d_w: c_uint,
    d_h: c_uint,
    r_w: c_uint,
    r_h: c_uint,
    x_chroma_shift: c_uint,
    y_chroma_shift: c_uint,
    planes: [*mut u8; 4],
    stride: [c_int; 4],
    bps: c_int,
    _pad1: c_int,
    user_priv: *mut c_void,
    img_data: *mut u8,
    img_data_owner: c_int,
    self_allocd: c_int,
    fb_priv: *mut c_void,
}

#[repr(C)]
struct VpxCodecIter {
    _private: [u8; 0],
}

#[repr(C)]
#[derive(Default, Clone, Copy)]
struct VpxFixedBuf {
    buf: *mut u8,
    sz: usize,
}

#[repr(C)]
#[derive(Default, Clone, Copy)]
struct VpxCodecCxFrame {
    buf: *mut u8,
    sz: usize,
    pts: i64,
    duration: u64,
    flags: u32,
    partition_id: c_int,
}

#[repr(C)]
#[derive(Default, Clone, Copy)]
struct VpxCodecCxPkt {
    kind: c_int,
    data: VpxCodecCxPktData,
}

#[repr(C)]
#[derive(Clone, Copy)]
union VpxCodecCxPktData {
    raw: VpxFixedBuf,
    frame: VpxCodecCxFrame,
    stats: VpxFixedBuf,
    psize: usize,
}

impl Default for VpxCodecCxPktData {
    fn default() -> Self {
        VpxCodecCxPktData {
            raw: VpxFixedBuf::default(),
        }
    }
}

const VPX_IMG_FMT_I420: c_int = 258;

const VPX_DECODER_ABI_VERSION: c_int = 12;
const VPX_ENCODER_ABI_VERSION: c_int = 37;

const VPX_CODEC_CX_FRAME_PKT: c_int = 0;

const VPX_FRAME_IS_KEY: c_int = 1;
const VPX_EFLAG_FORCE_KF: c_int = 1;
const VPX_DL_GOOD_QUALITY: c_uint = 0;
const VPX_DL_REALTIME: c_uint = 1;

unsafe impl Send for VpxCodecCtx {}
unsafe impl Sync for VpxCodecCtx {}
unsafe impl Send for VpxImage {}
unsafe impl Sync for VpxImage {}

extern "C" {
    fn vpx_codec_vp8_dx() -> *const c_void;
    fn vpx_codec_vp8_cx() -> *const c_void;
    fn vpx_codec_vp9_dx() -> *const c_void;
    fn vpx_codec_vp9_cx() -> *const c_void;

    fn vpx_codec_dec_init_ver(
        ctx: *mut VpxCodecCtx,
        iface: *const c_void,
        cfg: *const c_void,
        flags: c_uint,
        ver: c_int,
    ) -> c_int;

    fn vpx_codec_decode(
        ctx: *mut VpxCodecCtx,
        data: *const u8,
        data_sz: usize,
        user_priv: *mut c_void,
        deadline: c_int,
    ) -> c_int;

    fn vpx_codec_get_frame(ctx: *mut VpxCodecCtx, iter: *mut *mut VpxCodecIter) -> *mut VpxImage;

    fn vpx_codec_destroy(ctx: *mut VpxCodecCtx) -> c_int;

    fn vpx_codec_enc_init_ver(
        ctx: *mut VpxCodecCtx,
        iface: *const c_void,
        cfg: *const VpxCodecEncCfg,
        flags: c_uint,
        ver: c_int,
    ) -> c_int;

    fn vpx_codec_enc_config_default(
        iface: *const c_void,
        cfg: *mut VpxCodecEncCfg,
        usage: c_uint,
    ) -> c_int;

    fn vpx_codec_encode(
        ctx: *mut VpxCodecCtx,
        img: *const VpxImage,
        pts: c_int,
        duration: c_int,
        flags: c_int,
        deadline: c_int,
    ) -> c_int;

    fn vpx_codec_get_cx_data(
        ctx: *mut VpxCodecCtx,
        iter: *mut *mut VpxCodecIter,
    ) -> *const VpxCodecCxPkt;

    fn vpx_codec_enc_config_set(ctx: *mut VpxCodecCtx, cfg: *const VpxCodecEncCfg) -> c_int;

    fn vpx_img_alloc(
        img: *mut VpxImage,
        fmt: c_int,
        d_w: c_uint,
        d_h: c_uint,
        align: c_uint,
    ) -> *mut VpxImage;

    fn vpx_img_free(img: *mut VpxImage);

    fn vpx_codec_error(ctx: *mut VpxCodecCtx) -> *const c_char;
    #[allow(dead_code)]
    fn vpx_codec_error_detail(ctx: *mut VpxCodecCtx) -> *const c_char;
}

fn vpx_err_str(ctx: &mut VpxCodecCtx) -> String {
    let err = unsafe { vpx_codec_error(ctx) };
    if !err.is_null() {
        unsafe { std::ffi::CStr::from_ptr(err).to_string_lossy().into_owned() }
    } else {
        "unknown vpx error".to_string()
    }
}

fn copy_yuv_from_image(
    img: &VpxImage,
    width: u32,
    height: u32,
    timestamp: u64,
    keyframe: bool,
) -> YuvFrame {
    let y_size = (width * height) as usize;
    let uv_w = (width / 2) as usize;
    let uv_h = (height / 2) as usize;
    let uv_size = uv_w * uv_h;

    let mut y = vec![0u8; y_size];
    let mut u = vec![0u8; uv_size];
    let mut v = vec![0u8; uv_size];

    let y_stride = img.stride[0] as usize;
    for row in 0..height as usize {
        let src = unsafe {
            std::slice::from_raw_parts(img.planes[0].add(row * y_stride), width as usize)
        };
        y[row * width as usize..(row + 1) * width as usize].copy_from_slice(src);
    }

    let u_stride = img.stride[1] as usize;
    let v_stride = img.stride[2] as usize;
    for row in 0..uv_h {
        let src_u = unsafe { std::slice::from_raw_parts(img.planes[1].add(row * u_stride), uv_w) };
        u[row * uv_w..(row + 1) * uv_w].copy_from_slice(src_u);
        let src_v = unsafe { std::slice::from_raw_parts(img.planes[2].add(row * v_stride), uv_w) };
        v[row * uv_w..(row + 1) * uv_w].copy_from_slice(src_v);
    }

    YuvFrame {
        y,
        u,
        v,
        width,
        height,
        timestamp,
        keyframe,
    }
}

pub struct VpxDecoder {
    ctx: VpxCodecCtx,
    initialized: bool,
    codec_type: lm_core::CodecType,
}

impl VpxDecoder {
    pub fn new_vp8() -> Result<Self, VideoCodecError> {
        Self::new(lm_core::CodecType::Vp8, unsafe { vpx_codec_vp8_dx() })
    }

    pub fn new_vp9() -> Result<Self, VideoCodecError> {
        Self::new(lm_core::CodecType::Vp9, unsafe { vpx_codec_vp9_dx() })
    }

    fn new(codec_type: lm_core::CodecType, iface: *const c_void) -> Result<Self, VideoCodecError> {
        if iface.is_null() {
            return Err(VideoCodecError::DecodeFailed(format!(
                "Failed to get {:?} decoder interface",
                codec_type
            )));
        }

        let mut ctx = VpxCodecCtx {
            name: ptr::null(),
            iface: ptr::null_mut(),
            err: 0,
            _pad1: 0,
            err_detail: ptr::null(),
            init_flags: 0,
            config: ptr::null(),
            priv_: ptr::null_mut(),
        };

        let ret = unsafe {
            vpx_codec_dec_init_ver(&mut ctx, iface, ptr::null(), 0, VPX_DECODER_ABI_VERSION)
        };
        if ret != 0 {
            return Err(VideoCodecError::DecodeFailed(format!(
                "{:?} decoder init failed: {}",
                codec_type,
                vpx_err_str(&mut ctx)
            )));
        }

        Ok(Self {
            ctx,
            initialized: true,
            codec_type,
        })
    }
}

impl Drop for VpxDecoder {
    fn drop(&mut self) {
        if self.initialized {
            unsafe { vpx_codec_destroy(&mut self.ctx) };
        }
    }
}

impl VideoDecoder for VpxDecoder {
    fn decode(&mut self, data: &[u8], timestamp: u64) -> Result<YuvFrame, VideoCodecError> {
        if data.is_empty() {
            return Err(VideoCodecError::InvalidInput("empty input data".into()));
        }

        let ret = unsafe {
            vpx_codec_decode(&mut self.ctx, data.as_ptr(), data.len(), ptr::null_mut(), 0)
        };
        if ret != 0 {
            return Err(VideoCodecError::DecodeFailed(vpx_err_str(&mut self.ctx)));
        }

        let mut iter: *mut VpxCodecIter = ptr::null_mut();
        let img = unsafe { vpx_codec_get_frame(&mut self.ctx, &mut iter) };
        if img.is_null() {
            return Err(VideoCodecError::DecodeFailed("no output frame yet".into()));
        }

        let img_ref = unsafe { &*img };
        let width = img_ref.d_w as u32;
        let height = img_ref.d_h as u32;
        debug_assert!(width > 0 && height > 0, "decoded frame has zero dimensions");

        let keyframe = (img_ref.fmt & VPX_FRAME_IS_KEY) != 0;
        Ok(copy_yuv_from_image(
            img_ref, width, height, timestamp, keyframe,
        ))
    }

    fn codec(&self) -> lm_core::CodecType {
        self.codec_type
    }
}

pub struct VpxEncoder {
    ctx: VpxCodecCtx,
    img: VpxImage,
    config: EncoderConfig,
    initialized: bool,
    force_keyframe: bool,
    codec_type: lm_core::CodecType,
}

impl VpxEncoder {
    pub fn new_vp8(config: EncoderConfig) -> Result<Self, VideoCodecError> {
        Self::new(lm_core::CodecType::Vp8, config, unsafe {
            vpx_codec_vp8_cx()
        })
    }

    pub fn new_vp9(config: EncoderConfig) -> Result<Self, VideoCodecError> {
        Self::new(lm_core::CodecType::Vp9, config, unsafe {
            vpx_codec_vp9_cx()
        })
    }

    fn new(
        codec_type: lm_core::CodecType,
        config: EncoderConfig,
        iface: *const c_void,
    ) -> Result<Self, VideoCodecError> {
        if config.width == 0 || config.height == 0 {
            return Err(VideoCodecError::InvalidInput(
                "width and height must be non-zero".into(),
            ));
        }
        if iface.is_null() {
            return Err(VideoCodecError::EncodeFailed(format!(
                "Failed to get {:?} encoder interface",
                codec_type
            )));
        }

        let mut cfg = VpxCodecEncCfg::default();
        let ret = unsafe { vpx_codec_enc_config_default(iface, &mut cfg, VPX_DL_GOOD_QUALITY) };
        if ret != 0 {
            return Err(VideoCodecError::EncodeFailed(format!(
                "enc_config_default failed: {}",
                ret
            )));
        }

        cfg.g_w = config.width as c_uint;
        cfg.g_h = config.height as c_uint;
        cfg.g_timebase = VpxRational { num: 1, den: 90000 };
        cfg.rc_target_bitrate = config.bitrate;
        cfg.g_lag_in_frames = 0;
        cfg.kf_max_dist = config.keyframe_interval;
        cfg.g_threads = config.threads;
        cfg.g_error_resilient = 1;

        let mut ctx = VpxCodecCtx {
            name: ptr::null(),
            iface: ptr::null_mut(),
            err: 0,
            _pad1: 0,
            err_detail: ptr::null(),
            init_flags: 0,
            config: ptr::null(),
            priv_: ptr::null_mut(),
        };

        let ret =
            unsafe { vpx_codec_enc_init_ver(&mut ctx, iface, &cfg, 0, VPX_ENCODER_ABI_VERSION) };
        if ret != 0 {
            return Err(VideoCodecError::EncodeFailed(format!(
                "{:?} enc_init failed: {}",
                codec_type,
                vpx_err_str(&mut ctx)
            )));
        }

        let mut img = VpxImage::default();
        let img_ptr =
            unsafe { vpx_img_alloc(&mut img, VPX_IMG_FMT_I420, config.width, config.height, 32) };
        if img_ptr.is_null() {
            unsafe { vpx_codec_destroy(&mut ctx) };
            return Err(VideoCodecError::EncodeFailed("vpx_img_alloc failed".into()));
        }

        Ok(Self {
            ctx,
            img,
            config,
            initialized: true,
            force_keyframe: false,
            codec_type,
        })
    }
}

impl Drop for VpxEncoder {
    fn drop(&mut self) {
        if self.initialized {
            unsafe {
                vpx_img_free(&mut self.img);
                vpx_codec_destroy(&mut self.ctx);
            }
        }
    }
}

impl VideoEncoder for VpxEncoder {
    fn encode(&mut self, frame: &YuvFrame) -> Result<EncodedFrame, VideoCodecError> {
        if frame.width != self.config.width || frame.height != self.config.height {
            return Err(VideoCodecError::InvalidInput(format!(
                "frame dimensions {}x{} != encoder {}x{}",
                frame.width, frame.height, self.config.width, self.config.height
            )));
        }

        let y_stride = self.img.stride[0] as usize;
        let u_stride = self.img.stride[1] as usize;
        let v_stride = self.img.stride[2] as usize;
        let h = self.config.height as usize;
        let uv_w = (self.config.width / 2) as usize;
        let uv_h = (self.config.height / 2) as usize;

        for row in 0..h {
            let dst = unsafe { self.img.planes[0].add(row * y_stride) };
            let src = &frame.y
                [row * frame.y_stride()..row * frame.y_stride() + self.config.width as usize];
            unsafe { ptr::copy_nonoverlapping(src.as_ptr(), dst, self.config.width as usize) };
        }
        for row in 0..uv_h {
            let dst_u = unsafe { self.img.planes[1].add(row * u_stride) };
            let src_u = &frame.u[row * frame.uv_stride()..row * frame.uv_stride() + uv_w];
            unsafe { ptr::copy_nonoverlapping(src_u.as_ptr(), dst_u, uv_w) };
            let dst_v = unsafe { self.img.planes[2].add(row * v_stride) };
            let src_v = &frame.v[row * frame.uv_stride()..row * frame.uv_stride() + uv_w];
            unsafe { ptr::copy_nonoverlapping(src_v.as_ptr(), dst_v, uv_w) };
        }

        let flags = if self.force_keyframe {
            self.force_keyframe = false;
            VPX_EFLAG_FORCE_KF
        } else {
            0
        };

        let pts = frame.timestamp as c_int;
        let ret = unsafe {
            vpx_codec_encode(
                &mut self.ctx,
                &self.img,
                pts,
                1,
                flags,
                VPX_DL_REALTIME as c_int,
            )
        };
        if ret != 0 {
            return Err(VideoCodecError::EncodeFailed(vpx_err_str(&mut self.ctx)));
        }

        let mut iter: *mut VpxCodecIter = ptr::null_mut();
        let mut data = Vec::new();
        let mut is_keyframe = false;

        loop {
            let pkt = unsafe { vpx_codec_get_cx_data(&mut self.ctx, &mut iter) };
            if pkt.is_null() {
                break;
            }
            let pkt_ref = unsafe { &*pkt };
            if pkt_ref.kind == VPX_CODEC_CX_FRAME_PKT {
                let frame_pkt = unsafe { pkt_ref.data.frame };
                if !frame_pkt.buf.is_null() && frame_pkt.sz > 0 {
                    let slice = unsafe { std::slice::from_raw_parts(frame_pkt.buf, frame_pkt.sz) };
                    data.extend_from_slice(slice);
                    if (frame_pkt.flags & VPX_FRAME_IS_KEY as u32) != 0 {
                        is_keyframe = true;
                    }
                }
            }
        }

        if data.is_empty() {
            return Err(VideoCodecError::EncodeFailed("no output packet".into()));
        }

        Ok(EncodedFrame {
            data: bytes::Bytes::from(data),
            width: self.config.width,
            height: self.config.height,
            keyframe: is_keyframe,
            timestamp: frame.timestamp,
        })
    }

    fn request_keyframe(&mut self) {
        self.force_keyframe = true;
    }

    fn codec(&self) -> lm_core::CodecType {
        self.codec_type
    }

    fn set_bitrate(&mut self, bps: u32) {
        self.config.bitrate = bps;
        let mut cfg = VpxCodecEncCfg::default();
        let iface = unsafe {
            if self.codec_type == lm_core::CodecType::Vp8 {
                vpx_codec_vp8_cx()
            } else {
                vpx_codec_vp9_cx()
            }
        };
        if unsafe { vpx_codec_enc_config_default(iface, &mut cfg, VPX_DL_GOOD_QUALITY) } == 0 {
            cfg.g_w = self.config.width as c_uint;
            cfg.g_h = self.config.height as c_uint;
            cfg.rc_target_bitrate = bps;
            cfg.g_lag_in_frames = 0;
            cfg.kf_max_dist = self.config.keyframe_interval;
            cfg.g_threads = self.config.threads;
            unsafe { vpx_codec_enc_config_set(&mut self.ctx, &cfg) };
        }
    }

    fn set_framerate(&mut self, fps: u32) {
        self.config.framerate = fps;
    }
}
