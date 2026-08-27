//! macOS VideoToolbox 硬件加速编码器
//!
//! 直接调用 VideoToolbox C API，支持 H.264 和 H.265/HEVC 硬件编码。
//! 仅 macOS 可用，Linux 上此模块不会被编译。
//!
//! VideoToolbox 编码是异步的（callback-based），通过 Mutex<Vec> 收集输出。

#![allow(dead_code)]

use crate::{EncodedFrame, EncoderConfig, VideoCodecError, VideoEncoder, YuvFrame};
use std::ffi::{c_void, CString};
use std::os::raw::c_char;
use std::ptr;
use std::sync::{Arc, Mutex};

type OSStatus = i32;
type CVReturn = i32;
type CMItemCount = i64;
type CMVideoCodecType = u32;

const NO_ERR: OSStatus = 0;
const KCM_VIDEO_CODEC_TYPE_H264: CMVideoCodecType = 0x61766331; // 'avc1'
const KCM_VIDEO_CODEC_TYPE_HEVC: CMVideoCodecType = 0x68766331; // 'hvc1'

const KCV_PIXEL_FORMAT_TYPE_420YPCBCR8_PLANAR: u32 = 0x79343230; // 'y420'

const KCV_RETURN_SUCCESS: CVReturn = 0;

const KVT_COMPRESSION_PROPERTY_KEY_AVERAGE_BIT_RATE: &str = "AverageBitRate";
const KVT_COMPRESSION_PROPERTY_KEY_MAX_KEY_FRAME_INTERVAL: &str = "MaxKeyFrameInterval";
const KVT_COMPRESSION_PROPERTY_KEY_REAL_TIME: &str = "RealTime";
const KVT_COMPRESSION_PROPERTY_KEY_EXPECTED_FRAME_RATE: &str = "ExpectedFrameRate";
const KVT_COMPRESSION_PROPERTY_KEY_ALLOW_FRAME_REORDERING: &str = "AllowFrameReordering";
const KVT_COMPRESSION_PROPERTY_KEY_PROFILE_LEVEL: &str = "ProfileLevel";

const KVT_ENCODE_FRAME_OPTION_KEY_FORCE_KEY_FRAME: &str = "ForceKeyFrame";

type VTCompressionSessionRef = *mut c_void;
type CVImageBufferRef = *mut c_void;
type CVPixelBufferRef = *mut c_void;
type CMSampleBufferRef = *mut c_void;
type CMFormatDescriptionRef = *mut c_void;
type CMBlockBufferRef = *mut c_void;
type CFAllocatorRef = *mut c_void;
type CFDictionaryRef = *mut c_void;
type CFStringRef = *const c_void;
type CFNumberRef = *mut c_void;
type CFBooleanRef = *mut c_void;
type CFTypeRef = *const c_void;

#[repr(C)]
#[derive(Clone, Copy, Default)]
struct CMTime {
    value: i64,
    timescale: i32,
    flags: u32,
    epoch: i64,
}

type VTCompressionOutputCallback = unsafe extern "C" fn(
    output_callback_ref_con: *mut c_void,
    source_frame_ref_con: *mut c_void,
    status: OSStatus,
    info_flags: u32,
    sample_buffer: CMSampleBufferRef,
);

type VTEncodeInfoFlags = u32;

extern "C" {
    // VideoToolbox
    fn VTCompressionSessionCreate(
        allocator: CFAllocatorRef,
        width: i32,
        height: i32,
        codec_type: CMVideoCodecType,
        encoder_specification: CFDictionaryRef,
        source_image_buffer_attributes: CFDictionaryRef,
        compressed_data_allocator: CFAllocatorRef,
        output_callback: VTCompressionOutputCallback,
        output_callback_ref_con: *mut c_void,
        compression_session_out: *mut VTCompressionSessionRef,
    ) -> OSStatus;

    fn VTCompressionSessionEncodeFrame(
        session: VTCompressionSessionRef,
        image_buffer: CVImageBufferRef,
        presentation_timestamp: CMTime,
        duration: CMTime,
        frame_properties: CFDictionaryRef,
        source_frame_refcon: *mut c_void,
        info_flags_out: *mut VTEncodeInfoFlags,
    ) -> OSStatus;

    fn VTCompressionSessionCompleteFrames(
        session: VTCompressionSessionRef,
        complete_until_presentation_time_stamp: CMTime,
    ) -> OSStatus;

    fn VTCompressionSessionInvalidate(session: VTCompressionSessionRef);

    fn VTSessionSetProperty(
        session: VTCompressionSessionRef,
        property_key: CFStringRef,
        property_value: CFTypeRef,
    ) -> OSStatus;

    fn VTCompressionSessionPrepareToEncodeFrames(session: VTCompressionSessionRef) -> OSStatus;

    // CoreMedia
    fn CMSampleBufferGetDataBuffer(sample_buffer: CMSampleBufferRef) -> CMBlockBufferRef;
    fn CMSampleBufferGetFormatDescription(
        sample_buffer: CMSampleBufferRef,
    ) -> CMFormatDescriptionRef;
    fn CMSampleBufferGetNumSamples(sample_buffer: CMSampleBufferRef) -> CMItemCount;
    fn CMSampleBufferGetSampleTimingArray(
        sample_buffer: CMSampleBufferRef,
        sample_timing_array_num_entries: CMItemCount,
        sample_timing_array_out: *mut c_void,
    ) -> OSStatus;

    fn CMBlockBufferGetDataLength(the_buffer: CMBlockBufferRef) -> usize;
    fn CMBlockBufferCopyDataBytes(
        the_buffer: CMBlockBufferRef,
        offset_to_data: usize,
        data_length: usize,
        destination: *mut c_void,
    ) -> OSStatus;

    fn CMVideoFormatDescriptionGetH264ParameterSetAtIndex(
        video_desc: CMFormatDescriptionRef,
        parameter_set_index: usize,
        parameter_set_pointer_out: *mut *const u8,
        parameter_set_size_out: *mut usize,
        parameter_set_count_out: *mut usize,
        nal_unit_header_size_out: *mut i32,
    ) -> OSStatus;

    fn CMVideoFormatDescriptionGetHEVCParameterSetAtIndex(
        video_desc: CMFormatDescriptionRef,
        parameter_set_index: usize,
        parameter_set_pointer_out: *mut *const u8,
        parameter_set_size_out: *mut usize,
        parameter_set_count_out: *mut usize,
        nal_unit_header_size_out: *mut i32,
    ) -> OSStatus;

    // CoreVideo
    fn CVPixelBufferCreate(
        allocator: CFAllocatorRef,
        width: usize,
        height: usize,
        pixel_format_type: u32,
        pixel_buffer_attributes: CFDictionaryRef,
        pixel_buffer_out: *mut CVPixelBufferRef,
    ) -> CVReturn;

    fn CVPixelBufferLockBaseAddress(pixel_buffer: CVPixelBufferRef, lock_flags: u32) -> CVReturn;
    fn CVPixelBufferUnlockBaseAddress(
        pixel_buffer: CVPixelBufferRef,
        unlock_flags: u32,
    ) -> CVReturn;
    fn CVPixelBufferGetBaseAddressOfPlane(
        pixel_buffer: CVPixelBufferRef,
        plane_index: usize,
    ) -> *mut c_void;
    fn CVPixelBufferGetBytesPerRowOfPlane(
        pixel_buffer: CVPixelBufferRef,
        plane_index: usize,
    ) -> usize;
    fn CVPixelBufferGetWidthOfPlane(pixel_buffer: CVPixelBufferRef, plane_index: usize) -> usize;
    fn CVPixelBufferGetHeightOfPlane(pixel_buffer: CVPixelBufferRef, plane_index: usize) -> usize;
    fn CVPixelBufferRelease(pixel_buffer: CVPixelBufferRef);

    // CoreFoundation
    fn CFStringCreateWithCString(
        alloc: CFAllocatorRef,
        c_str: *const c_char,
        encoding: u32,
    ) -> CFStringRef;
    fn CFNumberCreate(
        alloc: CFAllocatorRef,
        the_type: u32,
        value_ptr: *const c_void,
    ) -> CFNumberRef;
    fn CFRelease(cf: CFTypeRef);
    fn CFDictionaryCreate(
        alloc: CFAllocatorRef,
        keys: *const CFTypeRef,
        values: *const CFTypeRef,
        num_values: i64,
        key_callbacks: *const c_void,
        value_callbacks: *const c_void,
    ) -> CFDictionaryRef;

    fn CFRetain(cf: CFTypeRef);
}

extern "C" {
    static kCFBooleanTrue: CFBooleanRef;
}

const K_CF_NUMBER_S_INT32_TYPE: u32 = 3;
const K_CF_NUMBER_S_INT64_TYPE: u32 = 4;
const K_CF_STRING_ENCODING_UTF8: u32 = 0x08000100;

struct EncodedOutput {
    data: Vec<u8>,
    keyframe: bool,
}

struct CallbackContext {
    outputs: Mutex<Vec<EncodedOutput>>,
}

unsafe extern "C" fn compression_output_callback(
    ref_con: *mut c_void,
    _source_frame_ref_con: *mut c_void,
    status: OSStatus,
    _info_flags: u32,
    sample_buffer: CMSampleBufferRef,
) {
    if status != NO_ERR || sample_buffer.is_null() {
        return;
    }

    let ctx = &*(ref_con as *const CallbackContext);

    let format_desc = CMSampleBufferGetFormatDescription(sample_buffer);
    if format_desc.is_null() {
        return;
    }

    let mut nal_units = Vec::new();

    // Extract parameter sets (SPS/PPS for H.264, VPS/SPS/PPS for HEVC)
    let mut param_count: usize = 0;
    let mut nal_header_size: i32 = 0;

    // Try H.264 first
    let h264_ret = CMVideoFormatDescriptionGetH264ParameterSetAtIndex(
        format_desc,
        0,
        ptr::null_mut(),
        ptr::null_mut(),
        &mut param_count,
        &mut nal_header_size,
    );
    if h264_ret == NO_ERR && param_count > 0 {
        for i in 0..param_count {
            let mut ptr_out: *const u8 = ptr::null_mut();
            let mut size_out: usize = 0;
            let ret = CMVideoFormatDescriptionGetH264ParameterSetAtIndex(
                format_desc,
                i,
                &mut ptr_out,
                &mut size_out,
                ptr::null_mut(),
                ptr::null_mut(),
            );
            if ret == NO_ERR && !ptr_out.is_null() && size_out > 0 {
                let slice = std::slice::from_raw_parts(ptr_out, size_out);
                nal_units.push((slice.to_vec(), true));
            }
        }
    } else {
        // Try HEVC
        let hevc_ret = CMVideoFormatDescriptionGetHEVCParameterSetAtIndex(
            format_desc,
            0,
            ptr::null_mut(),
            ptr::null_mut(),
            &mut param_count,
            &mut nal_header_size,
        );
        if hevc_ret == NO_ERR && param_count > 0 {
            for i in 0..param_count {
                let mut ptr_out: *const u8 = ptr::null_mut();
                let mut size_out: usize = 0;
                let ret = CMVideoFormatDescriptionGetHEVCParameterSetAtIndex(
                    format_desc,
                    i,
                    &mut ptr_out,
                    &mut size_out,
                    ptr::null_mut(),
                    ptr::null_mut(),
                );
                if ret == NO_ERR && !ptr_out.is_null() && size_out > 0 {
                    let slice = std::slice::from_raw_parts(ptr_out, size_out);
                    nal_units.push((slice.to_vec(), true));
                }
            }
        }
    }

    // Extract the encoded frame data from the sample buffer's block buffer
    let data_buffer = CMSampleBufferGetDataBuffer(sample_buffer);
    if !data_buffer.is_null() {
        let data_length = CMBlockBufferGetDataLength(data_buffer);
        if data_length > 0 {
            let mut frame_data = vec![0u8; data_length];
            let ret = CMBlockBufferCopyDataBytes(
                data_buffer,
                0,
                data_length,
                frame_data.as_mut_ptr() as *mut c_void,
            );
            if ret == NO_ERR {
                nal_units.push((frame_data, false));
            }
        }
    }

    if nal_units.is_empty() {
        return;
    }

    // Prepend start codes and concatenate
    let mut data = Vec::new();
    let mut is_keyframe = false;

    // First, emit parameter set NALs (SPS/PPS/VPS) if present
    for (nal_data, is_param_set) in &nal_units {
        if *is_param_set {
            data.extend_from_slice(&[0u8, 0, 0, 1]);
            data.extend_from_slice(nal_data);
        }
    }

    // Then emit frame data, converting AVCC (4-byte length prefix) to Annex B (start codes)
    for (nal_data, is_param_set) in &nal_units {
        if *is_param_set {
            continue;
        }

        // VideoToolbox outputs AVCC format: [4-byte length][NAL unit]...
        // Convert to Annex B: [00 00 00 01][NAL unit]...
        let mut offset = 0usize;
        while offset + 4 < nal_data.len() {
            let nal_len = u32::from_be_bytes([
                nal_data[offset],
                nal_data[offset + 1],
                nal_data[offset + 2],
                nal_data[offset + 3],
            ]) as usize;
            let nal_start = offset + 4;
            let nal_end = nal_start + nal_len;
            if nal_start >= nal_data.len() || nal_end > nal_data.len() {
                break;
            }

            // Write start code + NAL unit
            data.extend_from_slice(&[0u8, 0, 0, 1]);
            data.extend_from_slice(&nal_data[nal_start..nal_end]);

            // Detect keyframe by NAL type
            let nal_type = if nal_header_size == 4 {
                nal_data[nal_start] & 0x1f
            } else {
                (nal_data[nal_start] >> 1) & 0x3f
            };
            // H.264: type 5 = IDR slice; HEVC: type 16-21 = IDR/CRA/BLA
            if nal_type == 5 || (16..=21).contains(&nal_type) {
                is_keyframe = true;
            }
            offset = nal_end;
        }
    }

    if let Ok(mut outputs) = ctx.outputs.lock() {
        outputs.push(EncodedOutput {
            data,
            keyframe: is_keyframe,
        });
    }
}

pub struct VideoToolboxEncoder {
    session: VTCompressionSessionRef,
    pixel_buffer: CVPixelBufferRef,
    callback_ctx: Arc<CallbackContext>,
    config: EncoderConfig,
    codec_type: CMVideoCodecType,
    force_keyframe: bool,
    frame_count: i64,
    timescale: i32,
}

unsafe impl Send for VideoToolboxEncoder {}
unsafe impl Sync for VideoToolboxEncoder {}

impl VideoToolboxEncoder {
    pub fn new_h264(config: EncoderConfig) -> Result<Self, VideoCodecError> {
        Self::new(config, KCM_VIDEO_CODEC_TYPE_H264)
    }

    pub fn new_h265(config: EncoderConfig) -> Result<Self, VideoCodecError> {
        Self::new(config, KCM_VIDEO_CODEC_TYPE_HEVC)
    }

    fn new(config: EncoderConfig, codec_type: CMVideoCodecType) -> Result<Self, VideoCodecError> {
        if config.width == 0 || config.height == 0 {
            return Err(VideoCodecError::InvalidInput(
                "width and height must be non-zero".into(),
            ));
        }

        let callback_ctx = Arc::new(CallbackContext {
            outputs: Mutex::new(Vec::new()),
        });

        // The callback context needs to live as long as the session.
        // We pass a raw pointer; the session holds it.
        let ctx_ptr = Arc::into_raw(callback_ctx.clone()) as *mut c_void;

        let mut session: VTCompressionSessionRef = ptr::null_mut();

        let ret = unsafe {
            VTCompressionSessionCreate(
                ptr::null_mut(),
                config.width as i32,
                config.height as i32,
                codec_type,
                ptr::null_mut(),
                ptr::null_mut(),
                ptr::null_mut(),
                compression_output_callback,
                ctx_ptr,
                &mut session,
            )
        };

        if ret != NO_ERR {
            unsafe { drop(Arc::from_raw(ctx_ptr as *const CallbackContext)) };
            return Err(VideoCodecError::EncodeFailed(format!(
                "VTCompressionSessionCreate failed: {}",
                ret
            )));
        }

        // Set properties

        let set_prop = |key: &str, value: CFTypeRef| -> OSStatus {
            let c_key = CString::new(key).unwrap();
            let cf_key = unsafe {
                CFStringCreateWithCString(
                    ptr::null_mut(),
                    c_key.as_ptr(),
                    K_CF_STRING_ENCODING_UTF8,
                )
            };
            let ret = unsafe { VTSessionSetProperty(session, cf_key, value) };
            unsafe { CFRelease(cf_key as CFTypeRef) };
            ret
        };

        // AverageBitRate
        let bitrate = config.bitrate as i32;
        let cf_bitrate = unsafe {
            CFNumberCreate(
                ptr::null_mut(),
                K_CF_NUMBER_S_INT32_TYPE,
                &bitrate as *const i32 as *const c_void,
            )
        };
        let _ = set_prop(
            KVT_COMPRESSION_PROPERTY_KEY_AVERAGE_BIT_RATE,
            cf_bitrate as CFTypeRef,
        );
        unsafe { CFRelease(cf_bitrate as CFTypeRef) };

        // MaxKeyFrameInterval
        let kf_interval = config.keyframe_interval as i32;
        let cf_kf = unsafe {
            CFNumberCreate(
                ptr::null_mut(),
                K_CF_NUMBER_S_INT32_TYPE,
                &kf_interval as *const i32 as *const c_void,
            )
        };
        let _ = set_prop(
            KVT_COMPRESSION_PROPERTY_KEY_MAX_KEY_FRAME_INTERVAL,
            cf_kf as CFTypeRef,
        );
        unsafe { CFRelease(cf_kf as CFTypeRef) };

        // RealTime = true (use CFNumber 1 as boolean proxy)
        let one: i32 = 1;
        let cf_true = unsafe {
            CFNumberCreate(
                ptr::null_mut(),
                K_CF_NUMBER_S_INT32_TYPE,
                &one as *const i32 as *const c_void,
            )
        };
        let _ = set_prop(KVT_COMPRESSION_PROPERTY_KEY_REAL_TIME, cf_true as CFTypeRef);
        unsafe { CFRelease(cf_true as CFTypeRef) };

        // ExpectedFrameRate
        let fps = config.framerate as i32;
        let cf_fps = unsafe {
            CFNumberCreate(
                ptr::null_mut(),
                K_CF_NUMBER_S_INT32_TYPE,
                &fps as *const i32 as *const c_void,
            )
        };
        let _ = set_prop(
            KVT_COMPRESSION_PROPERTY_KEY_EXPECTED_FRAME_RATE,
            cf_fps as CFTypeRef,
        );
        unsafe { CFRelease(cf_fps as CFTypeRef) };

        // AllowFrameReordering = false (low latency)
        let zero: i32 = 0;
        let cf_false = unsafe {
            CFNumberCreate(
                ptr::null_mut(),
                K_CF_NUMBER_S_INT32_TYPE,
                &zero as *const i32 as *const c_void,
            )
        };
        let _ = set_prop(
            KVT_COMPRESSION_PROPERTY_KEY_ALLOW_FRAME_REORDERING,
            cf_false as CFTypeRef,
        );
        unsafe { CFRelease(cf_false as CFTypeRef) };

        // Prepare to encode

        let ret = unsafe { VTCompressionSessionPrepareToEncodeFrames(session) };

        if ret != NO_ERR {
            unsafe {
                VTCompressionSessionInvalidate(session);
                drop(Arc::from_raw(ctx_ptr as *const CallbackContext));
            }
            return Err(VideoCodecError::EncodeFailed(format!(
                "VTCompressionSessionPrepareToEncodeFrames failed: {}",
                ret
            )));
        }

        // Create a reusable pixel buffer
        let mut pixel_buffer: CVPixelBufferRef = ptr::null_mut();

        let ret = unsafe {
            CVPixelBufferCreate(
                ptr::null_mut(),
                config.width as usize,
                config.height as usize,
                KCV_PIXEL_FORMAT_TYPE_420YPCBCR8_PLANAR,
                ptr::null_mut(),
                &mut pixel_buffer,
            )
        };
        if ret != KCV_RETURN_SUCCESS {
            unsafe {
                VTCompressionSessionInvalidate(session);
                drop(Arc::from_raw(ctx_ptr as *const CallbackContext));
            }
            return Err(VideoCodecError::EncodeFailed(format!(
                "CVPixelBufferCreate failed: {}",
                ret
            )));
        }

        Ok(Self {
            session,
            pixel_buffer,
            callback_ctx,
            config,
            codec_type,
            force_keyframe: false,
            frame_count: 0,
            timescale: 90000,
        })
    }

    fn codec(&self) -> lm_core::CodecType {
        if self.codec_type == KCM_VIDEO_CODEC_TYPE_HEVC {
            lm_core::CodecType::H265
        } else {
            lm_core::CodecType::H264
        }
    }
}

impl Drop for VideoToolboxEncoder {
    fn drop(&mut self) {
        unsafe {
            if !self.session.is_null() {
                VTCompressionSessionCompleteFrames(self.session, CMTime::default());
                VTCompressionSessionInvalidate(self.session);
            }
            if !self.pixel_buffer.is_null() {
                CVPixelBufferRelease(self.pixel_buffer);
            }
        }
    }
}

impl VideoEncoder for VideoToolboxEncoder {
    fn encode(&mut self, frame: &YuvFrame) -> Result<EncodedFrame, VideoCodecError> {
        if frame.width != self.config.width || frame.height != self.config.height {
            return Err(VideoCodecError::InvalidInput(format!(
                "frame dimensions {}x{} != encoder {}x{}",
                frame.width, frame.height, self.config.width, self.config.height
            )));
        }

        // Clear previous outputs
        if let Ok(mut outputs) = self.callback_ctx.outputs.lock() {
            outputs.clear();
        }

        // Copy YUV data into the pixel buffer
        unsafe {
            CVPixelBufferLockBaseAddress(self.pixel_buffer, 0);

            let y_ptr = CVPixelBufferGetBaseAddressOfPlane(self.pixel_buffer, 0) as *mut u8;
            let u_ptr = CVPixelBufferGetBaseAddressOfPlane(self.pixel_buffer, 1) as *mut u8;
            let v_ptr = CVPixelBufferGetBaseAddressOfPlane(self.pixel_buffer, 2) as *mut u8;
            let y_stride = CVPixelBufferGetBytesPerRowOfPlane(self.pixel_buffer, 0);
            let u_stride = CVPixelBufferGetBytesPerRowOfPlane(self.pixel_buffer, 1);
            let v_stride = CVPixelBufferGetBytesPerRowOfPlane(self.pixel_buffer, 2);

            let w = self.config.width as usize;
            let h = self.config.height as usize;
            let uv_w = w / 2;
            let uv_h = h / 2;

            for row in 0..h {
                ptr::copy_nonoverlapping(
                    frame.y.as_ptr().add(row * frame.y_stride()),
                    y_ptr.add(row * y_stride),
                    w,
                );
            }
            for row in 0..uv_h {
                ptr::copy_nonoverlapping(
                    frame.u.as_ptr().add(row * frame.uv_stride()),
                    u_ptr.add(row * u_stride),
                    uv_w,
                );
                ptr::copy_nonoverlapping(
                    frame.v.as_ptr().add(row * frame.uv_stride()),
                    v_ptr.add(row * v_stride),
                    uv_w,
                );
            }

            CVPixelBufferUnlockBaseAddress(self.pixel_buffer, 0);
        }

        // Build frame properties for force keyframe
        let frame_props: CFDictionaryRef = if self.force_keyframe {
            self.force_keyframe = false;
            {
                use core_foundation::base::TCFType;
                use core_foundation::boolean::CFBoolean;
                use core_foundation::dictionary::CFDictionary;
                use core_foundation::string::CFString;

                let key = CFString::new(KVT_ENCODE_FRAME_OPTION_KEY_FORCE_KEY_FRAME);
                let val = CFBoolean::true_value();
                let dict = CFDictionary::from_CFType_pairs(&[(key, val)]);
                let raw = dict.as_CFType().as_concrete_TypeRef();
                std::mem::forget(dict);
                raw as CFDictionaryRef
            }
        } else {
            ptr::null_mut()
        };
        let force_kf = !frame_props.is_null();

        let pts = CMTime {
            value: frame.timestamp as i64,
            timescale: self.timescale,
            flags: 1, // kCMTimeFlags_Valid
            epoch: 0,
        };

        let duration = CMTime {
            value: 1,
            timescale: self.timescale,
            flags: 1,
            epoch: 0,
        };

        let ret = unsafe {
            VTCompressionSessionEncodeFrame(
                self.session,
                self.pixel_buffer as CVImageBufferRef,
                pts,
                duration,
                frame_props,
                ptr::null_mut(),
                ptr::null_mut(),
            )
        };

        if !frame_props.is_null() {
            unsafe { CFRelease(frame_props as CFTypeRef) };
        }

        let _ = force_kf;

        if ret != NO_ERR {
            return Err(VideoCodecError::EncodeFailed(format!(
                "VTCompressionSessionEncodeFrame failed: {}",
                ret
            )));
        }

        // Wait for the callback to fire (VideoToolbox may call synchronously for realtime)
        // Complete frames to flush output. Use kCMTimeInvalid (default) to complete ALL frames.
        let flush_time = CMTime::default();
        let _ = unsafe { VTCompressionSessionCompleteFrames(self.session, flush_time) };

        // If no output yet, retry a few times (forced keyframe may need extra flush)
        let mut retries = 0;
        while {
            let empty = self
                .callback_ctx
                .outputs
                .lock()
                .map(|o| o.is_empty())
                .unwrap_or(true);
            empty && retries < 50
        } {
            std::thread::sleep(std::time::Duration::from_millis(20));
            let _ = unsafe { VTCompressionSessionCompleteFrames(self.session, flush_time) };
            retries += 1;
        }

        // Extract output
        let output = if let Ok(mut outputs) = self.callback_ctx.outputs.lock() {
            if outputs.is_empty() {
                None
            } else {
                // Combine all outputs (should be just one frame)
                let mut data = Vec::new();
                let mut is_keyframe = false;
                for out in outputs.drain(..) {
                    if out.keyframe {
                        is_keyframe = true;
                    }
                    data.extend_from_slice(&out.data);
                }
                Some((data, is_keyframe))
            }
        } else {
            None
        };

        match output {
            Some((data, keyframe)) => {
                if data.is_empty() {
                    return Err(VideoCodecError::EncodeFailed("empty output".into()));
                }
                self.frame_count += 1;

                Ok(EncodedFrame {
                    data: bytes::Bytes::from(data),
                    width: self.config.width,
                    height: self.config.height,
                    keyframe,
                    timestamp: frame.timestamp,
                })
            }
            None => Err(VideoCodecError::EncodeFailed(
                "no output from VideoToolbox".into(),
            )),
        }
    }

    fn request_keyframe(&mut self) {
        self.force_keyframe = true;
    }

    fn codec(&self) -> lm_core::CodecType {
        self.codec()
    }

    fn set_bitrate(&mut self, bps: u32) {
        self.config.bitrate = bps;
        let bitrate = bps as i32;
        unsafe {
            let c_key = CString::new(KVT_COMPRESSION_PROPERTY_KEY_AVERAGE_BIT_RATE).unwrap();
            let cf_key = CFStringCreateWithCString(
                ptr::null_mut(),
                c_key.as_ptr(),
                K_CF_STRING_ENCODING_UTF8,
            );
            let cf_val = CFNumberCreate(
                ptr::null_mut(),
                K_CF_NUMBER_S_INT32_TYPE,
                &bitrate as *const i32 as *const c_void,
            );
            let _ = VTSessionSetProperty(self.session, cf_key, cf_val as CFTypeRef);
            CFRelease(cf_key as CFTypeRef);
            CFRelease(cf_val as CFTypeRef);
        }
    }

    fn set_framerate(&mut self, fps: u32) {
        self.config.framerate = fps;
        let fps_i = fps as i32;
        unsafe {
            let c_key = CString::new(KVT_COMPRESSION_PROPERTY_KEY_EXPECTED_FRAME_RATE).unwrap();
            let cf_key = CFStringCreateWithCString(
                ptr::null_mut(),
                c_key.as_ptr(),
                K_CF_STRING_ENCODING_UTF8,
            );
            let cf_val = CFNumberCreate(
                ptr::null_mut(),
                K_CF_NUMBER_S_INT32_TYPE,
                &fps_i as *const i32 as *const c_void,
            );
            let _ = VTSessionSetProperty(self.session, cf_key, cf_val as CFTypeRef);
            CFRelease(cf_key as CFTypeRef);
            CFRelease(cf_val as CFTypeRef);
        }
    }
}
