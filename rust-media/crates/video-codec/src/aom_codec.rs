//! AV1 编码器 — FFI 绑定系统 libaom 库
//!
//! libaom 是 Alliance for Open Media 的 AV1 参考实现。
//! 本模块通过 FFI 直接调用 libaom C API。
//!
//! AV1 解码使用 dav1d（见 dav1d_codec.rs）。

use crate::{EncodedFrame, EncoderConfig, VideoCodecError, VideoEncoder, YuvFrame};
use libc::{c_char, c_int, c_uint, c_void, size_t};
use std::ptr;

const AOM_IMAGE_ABI_VERSION: c_int = 9;
const AOM_CODEC_ABI_VERSION: c_int = 7 + AOM_IMAGE_ABI_VERSION;
const AOM_ENCODER_ABI_VERSION: c_int = 10 + AOM_CODEC_ABI_VERSION + 3;

const AOM_USAGE_REALTIME: c_uint = 1;

const AOM_IMG_FMT_I420: c_uint = 0x100 | 2;
const AOM_IMG_FMT_I42016: c_uint = 0x100 | 258; // FMT_I42016 = 0x1 << 8 | 2 (highbitdepth flag)
const AOM_BITS_8: c_uint = 8;
const AOM_BITS_10: c_uint = 10;

const AOM_EFLAG_FORCE_KF: c_int = 1 << 0;

const AOM_CODEC_CX_FRAME_PKT: c_int = 0;

const AOM_CODEC_OK: c_int = 0;

#[repr(C)]
#[derive(Clone, Copy)]
struct AomRational {
    num: c_int,
    den: c_int,
}

#[repr(C)]
struct AomCodecEncCfg {
    g_usage: c_uint,
    g_threads: c_uint,
    g_profile: c_uint,
    g_w: c_uint,
    g_h: c_uint,
    g_bit_depth: c_uint,
    g_input_bit_depth: c_uint,
    g_timebase: AomRational,
    g_error_resilient: c_uint,
    g_pass: c_uint,
    g_lag_in_frames: c_uint,
    _rc_pad: [c_uint; 10],
    rc_dropframe_thresh: c_uint,
    rc_resize_allowed: c_uint,
    rc_scaled_width: c_uint,
    rc_scaled_height: c_uint,
    rc_resize_up_thresh: c_uint,
    rc_resize_down_thresh: c_uint,
    rc_target_bitrate: c_uint,
    rc_min_quantizer: c_uint,
    rc_max_quantizer: c_uint,
    rc_undershoot_pct: c_uint,
    rc_overshoot_pct: c_uint,
    rc_buf_sz: c_uint,
    rc_buf_initial_sz: c_uint,
    rc_buf_optimal_sz: c_uint,
    rc_2pass_vbr_bias_pct: c_uint,
    rc_2pass_vbr_minsection_pct: c_uint,
    rc_2pass_vbr_maxsection_pct: c_uint,
    _rc_pad2: [c_uint; 4],
    kf_mode: c_uint,
    kf_min_dist: c_uint,
    kf_max_dist: c_uint,
    sframe_dist: c_uint,
    sframe_mode: c_uint,
    use_160x160_superblock: c_uint,
    _padding: [c_uint; 64],
}

#[repr(C)]
struct AomImage {
    fmt: c_uint,
    cp: c_int,
    tc: c_int,
    mc: c_int,
    monochrome: c_int,
    csp: c_int,
    range: c_int,
    w: c_uint,
    h: c_uint,
    bit_depth: c_uint,
    d_w: c_uint,
    d_h: c_uint,
    r_w: c_uint,
    r_h: c_uint,
    planes: [*mut u8; 3],
    stride: [c_int; 3],
    bps: c_int,
    _pad1: c_int,
    user_priv: *mut c_void,
    img_data: *mut u8,
    img_data_owner: c_int,
    self_allocd: c_int,
    fb_priv: *mut c_void,
}

#[repr(C)]
struct AomCodecCtx {
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
struct AomCodecIter {
    _private: [u8; 0],
}

#[repr(C)]
struct AomCodecCxPkt {
    kind: c_int,
    data: AomCodecCxPktData,
}

#[repr(C)]
union AomCodecCxPktData {
    frame: AomCodecCxFrame,
    raw: [u8; 64],
}

#[repr(C)]
#[derive(Clone, Copy)]
struct AomCodecCxFrame {
    buf: *mut c_void,
    sz: size_t,
    pts: i64,
    duration: u64,
    flags: c_int,
    partition_id: c_int,
    visible_w: c_uint,
    visible_h: c_uint,
}

unsafe impl Send for AomCodecCtx {}
unsafe impl Sync for AomCodecCtx {}
unsafe impl Send for AomImage {}
unsafe impl Sync for AomImage {}

extern "C" {
    fn aom_codec_av1_cx() -> *mut c_void;

    fn aom_codec_enc_config_default(
        iface: *mut c_void,
        cfg: *mut AomCodecEncCfg,
        usage: c_uint,
    ) -> c_int;

    fn aom_codec_enc_init_ver(
        ctx: *mut AomCodecCtx,
        iface: *mut c_void,
        cfg: *const AomCodecEncCfg,
        flags: c_uint,
        ver: c_int,
    ) -> c_int;

    #[allow(dead_code)]
    fn aom_codec_enc_config_set(ctx: *mut AomCodecCtx, cfg: *const AomCodecEncCfg) -> c_int;

    fn aom_codec_encode(
        ctx: *mut AomCodecCtx,
        img: *const AomImage,
        pts: i64,
        duration: u64,
        flags: c_int,
    ) -> c_int;

    fn aom_codec_get_cx_data(
        ctx: *mut AomCodecCtx,
        iter: *mut *mut AomCodecIter,
    ) -> *const AomCodecCxPkt;

    fn aom_codec_destroy(ctx: *mut AomCodecCtx) -> c_int;

    fn aom_codec_error(ctx: *mut AomCodecCtx) -> *const c_char;
    #[allow(dead_code)]
    fn aom_codec_error_detail(ctx: *mut AomCodecCtx) -> *const c_char;

    fn aom_img_alloc(
        img: *mut AomImage,
        fmt: c_uint,
        d_w: c_uint,
        d_h: c_uint,
        align: c_uint,
    ) -> *mut AomImage;

    fn aom_img_free(img: *mut AomImage);

    #[allow(dead_code)]
    fn aom_codec_set_frame_buffer_functions(
        ctx: *mut AomCodecCtx,
        cb: *const c_void,
        cb_priv: *mut c_void,
    ) -> c_int;
}

fn aom_err_str(ctx: &mut AomCodecCtx) -> String {
    let err = unsafe { aom_codec_error(ctx) };
    if !err.is_null() {
        unsafe { std::ffi::CStr::from_ptr(err).to_string_lossy().into_owned() }
    } else {
        "unknown aom error".to_string()
    }
}

pub struct AomEncoder {
    ctx: AomCodecCtx,
    img: AomImage,
    config: EncoderConfig,
    initialized: bool,
    force_keyframe: bool,
}

impl AomEncoder {
    pub fn new(config: EncoderConfig) -> Result<Self, VideoCodecError> {
        if config.width == 0 || config.height == 0 {
            return Err(VideoCodecError::InvalidInput(
                "width and height must be non-zero".into(),
            ));
        }

        let iface = unsafe { aom_codec_av1_cx() };
        if iface.is_null() {
            return Err(VideoCodecError::EncodeFailed(
                "aom_codec_av1_cx() returned null".into(),
            ));
        }

        let mut cfg = AomCodecEncCfg {
            g_usage: 0,
            g_threads: 0,
            g_profile: 0,
            g_w: 0,
            g_h: 0,
            g_bit_depth: 0,
            g_input_bit_depth: 0,
            g_timebase: AomRational { num: 0, den: 0 },
            g_error_resilient: 0,
            g_pass: 0,
            g_lag_in_frames: 0,
            _rc_pad: [0; 10],
            rc_dropframe_thresh: 0,
            rc_resize_allowed: 0,
            rc_scaled_width: 0,
            rc_scaled_height: 0,
            rc_resize_up_thresh: 0,
            rc_resize_down_thresh: 0,
            rc_target_bitrate: 0,
            rc_min_quantizer: 0,
            rc_max_quantizer: 0,
            rc_undershoot_pct: 0,
            rc_overshoot_pct: 0,
            rc_buf_sz: 0,
            rc_buf_initial_sz: 0,
            rc_buf_optimal_sz: 0,
            rc_2pass_vbr_bias_pct: 0,
            rc_2pass_vbr_minsection_pct: 0,
            rc_2pass_vbr_maxsection_pct: 0,
            _rc_pad2: [0; 4],
            kf_mode: 0,
            kf_min_dist: 0,
            kf_max_dist: 0,
            sframe_dist: 0,
            sframe_mode: 0,
            use_160x160_superblock: 0,
            _padding: [0; 64],
        };

        let ret = unsafe { aom_codec_enc_config_default(iface, &mut cfg, AOM_USAGE_REALTIME) };
        if ret != AOM_CODEC_OK {
            return Err(VideoCodecError::EncodeFailed(format!(
                "aom_codec_enc_config_default failed: {}",
                ret
            )));
        }

        cfg.g_w = config.width;
        cfg.g_h = config.height;
        cfg.g_timebase = AomRational { num: 1, den: 90000 };
        cfg.rc_target_bitrate = config.bitrate;
        cfg.g_lag_in_frames = 0;
        cfg.kf_max_dist = config.keyframe_interval;
        cfg.g_threads = config.threads;
        cfg.g_error_resilient = 1;

        // 10-bit 色深配置
        let is_10bit = config.bit_depth == 10;
        if is_10bit {
            cfg.g_bit_depth = AOM_BITS_10;
            cfg.g_input_bit_depth = AOM_BITS_10;
            cfg.g_profile = 2; // 10-bit 4:2:0 profile
        } else {
            cfg.g_bit_depth = AOM_BITS_8;
            cfg.g_input_bit_depth = AOM_BITS_8;
            cfg.g_profile = 0; // 8-bit 4:2:0 profile
        }

        let mut ctx = AomCodecCtx {
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
            unsafe { aom_codec_enc_init_ver(&mut ctx, iface, &cfg, 0, AOM_ENCODER_ABI_VERSION) };
        if ret != AOM_CODEC_OK {
            return Err(VideoCodecError::EncodeFailed(format!(
                "aom_codec_enc_init failed: {} ({})",
                ret,
                aom_err_str(&mut ctx)
            )));
        }

        let mut img = AomImage {
            fmt: 0,
            cp: 0,
            tc: 0,
            mc: 0,
            monochrome: 0,
            csp: 0,
            range: 0,
            w: 0,
            h: 0,
            bit_depth: 0,
            d_w: 0,
            d_h: 0,
            r_w: 0,
            r_h: 0,
            planes: [ptr::null_mut(); 3],
            stride: [0; 3],
            bps: 0,
            _pad1: 0,
            user_priv: ptr::null_mut(),
            img_data: ptr::null_mut(),
            img_data_owner: 0,
            self_allocd: 0,
            fb_priv: ptr::null_mut(),
        };

        let img_fmt = if is_10bit {
            AOM_IMG_FMT_I42016
        } else {
            AOM_IMG_FMT_I420
        };
        let img_ptr = unsafe { aom_img_alloc(&mut img, img_fmt, config.width, config.height, 32) };
        if img_ptr.is_null() {
            unsafe { aom_codec_destroy(&mut ctx) };
            return Err(VideoCodecError::EncodeFailed("aom_img_alloc failed".into()));
        }

        Ok(Self {
            ctx,
            img,
            config,
            initialized: true,
            force_keyframe: false,
        })
    }
}

impl Drop for AomEncoder {
    fn drop(&mut self) {
        if self.initialized {
            unsafe {
                aom_img_free(&mut self.img);
                aom_codec_destroy(&mut self.ctx);
            }
        }
    }
}

impl VideoEncoder for AomEncoder {
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

        let is_10bit = self.config.bit_depth == 10;

        if is_10bit {
            // 10-bit: 从 y16/u16/v16 填充图像数据 (u16 planes, little-endian)
            let w = self.config.width as usize;
            for row in 0..h {
                let dst = unsafe { self.img.planes[0].add(row * y_stride) as *mut u16 };
                let src = &frame.y16[row * frame.y_stride()..row * frame.y_stride() + w];
                unsafe {
                    ptr::copy_nonoverlapping(src.as_ptr(), dst, w);
                }
            }
            for row in 0..uv_h {
                let dst_u = unsafe { self.img.planes[1].add(row * u_stride) as *mut u16 };
                let src_u = &frame.u16[row * frame.uv_stride()..row * frame.uv_stride() + uv_w];
                unsafe {
                    ptr::copy_nonoverlapping(src_u.as_ptr(), dst_u, uv_w);
                }
                let dst_v = unsafe { self.img.planes[2].add(row * v_stride) as *mut u16 };
                let src_v = &frame.v16[row * frame.uv_stride()..row * frame.uv_stride() + uv_w];
                unsafe {
                    ptr::copy_nonoverlapping(src_v.as_ptr(), dst_v, uv_w);
                }
            }
        } else {
            // 8-bit: 从 y/u/v 填充图像数据
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
        }

        let flags = if self.force_keyframe {
            self.force_keyframe = false;
            AOM_EFLAG_FORCE_KF
        } else {
            0
        };

        let pts = frame.timestamp as i64;
        let ret = unsafe { aom_codec_encode(&mut self.ctx, &self.img, pts, 1, flags) };
        if ret != AOM_CODEC_OK {
            return Err(VideoCodecError::EncodeFailed(format!(
                "aom_codec_encode failed: {} ({})",
                ret,
                aom_err_str(&mut self.ctx)
            )));
        }

        let mut iter: *mut AomCodecIter = ptr::null_mut();
        // Pre-allocate based on estimated compressed size
        let estimated_size = (self.config.width * self.config.height / 4) as usize;
        let mut data = Vec::with_capacity(estimated_size.max(1024));
        let mut is_keyframe = false;

        loop {
            let pkt = unsafe { aom_codec_get_cx_data(&mut self.ctx, &mut iter) };
            if pkt.is_null() {
                break;
            }
            let pkt_ref = unsafe { &*pkt };
            if pkt_ref.kind == AOM_CODEC_CX_FRAME_PKT {
                let frame_pkt = unsafe { pkt_ref.data.frame };
                if !frame_pkt.buf.is_null() && frame_pkt.sz > 0 {
                    let slice = unsafe {
                        std::slice::from_raw_parts(frame_pkt.buf as *const u8, frame_pkt.sz)
                    };
                    data.extend_from_slice(slice);
                    // AOM_FRAME_IS_KEY = 0x1, so keyframe when flags & 1 != 0
                    if frame_pkt.flags & 1 != 0 {
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
            bit_depth: self.config.bit_depth,
        })
    }

    fn request_keyframe(&mut self) {
        self.force_keyframe = true;
    }

    fn codec(&self) -> lm_core::CodecType {
        lm_core::CodecType::Av1
    }

    fn set_bitrate(&mut self, bps: u32) {
        self.config.bitrate = bps;
    }

    fn set_framerate(&mut self, fps: u32) {
        self.config.framerate = fps;
    }
}
