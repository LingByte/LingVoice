//! macOS VideoToolbox 硬件加速编码器
//!
//! 直接调用 VideoToolbox C API，支持 H.264 和 H.265/HEVC 硬件编码。
//! 仅 macOS 可用，Linux 上此模块不会被编译。
//!
//! VideoToolbox 编码是异步的（callback-based），通过 Mutex<Vec> 收集输出。

#![allow(dead_code)]

use crate::{EncodedFrame, EncoderConfig, VideoCodecError, VideoDecoder, VideoEncoder, YuvFrame};
use std::collections::VecDeque;
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

// ============================================================================
// VideoToolbox 解码 FFI 声明
// ============================================================================

type VTDecompressionSessionRef = *mut c_void;
type CMVideoFormatDescriptionRef = *mut c_void;

/// VTDecompressionOutputHandler — newer block-based callback (macOS 10.8+)
type VTDecompressionOutputHandler = *mut c_void;

/// VTDecodeInfoFlags
type VTDecodeInfoFlags = u32;

/// 解码输出回调上下文
struct DecodeCallbackContext {
    outputs: Mutex<Vec<YuvFrame>>,
}

#[repr(C)]
#[derive(Clone, Copy, Default)]
struct CMVideoDimensions {
    width: i32,
    height: i32,
}

extern "C" {
    // CoreMedia — format description
    fn CMVideoFormatDescriptionCreate(
        allocator: CFAllocatorRef,
        codec_type: CMVideoCodecType,
        width: i32,
        height: i32,
        extensions: CFDictionaryRef,
        format_desc_out: *mut CMVideoFormatDescriptionRef,
    ) -> OSStatus;

    fn CMVideoFormatDescriptionGetDimensions(
        video_desc: CMVideoFormatDescriptionRef,
    ) -> CMVideoDimensions;

    fn CMVideoFormatDescriptionGetCodecType(
        video_desc: CMVideoFormatDescriptionRef,
    ) -> CMVideoCodecType;

    fn CMSampleBufferCreate(
        allocator: CFAllocatorRef,
        data_buffer: CMBlockBufferRef,
        data_ready: u32,
        make_data_ready_callback: *mut c_void,
        make_data_ready_refcon: *mut c_void,
        format_description: CMVideoFormatDescriptionRef,
        num_samples: CMItemCount,
        num_sample_timing_entries: CMItemCount,
        sample_timing_array: *const c_void,
        num_sample_size_entries: CMItemCount,
        sample_size_array: *const usize,
        sample_buffer_out: *mut CMSampleBufferRef,
    ) -> OSStatus;

    fn CMBlockBufferCreateWithMemoryBlock(
        allocator: CFAllocatorRef,
        memory_block_to_use: *mut c_void,
        block_length: usize,
        block_allocator: CFAllocatorRef,
        custom_block_source: *mut c_void,
        offset_to_data: usize,
        data_length: usize,
        flags: u32,
        new_block_buffer_out: *mut CMBlockBufferRef,
    ) -> OSStatus;

    // VideoToolbox — decompression session
    fn VTDecompressionSessionCreate(
        allocator: CFAllocatorRef,
        video_format_description: CMVideoFormatDescriptionRef,
        video_decoder_specification: CFDictionaryRef,
        destination_image_buffer_attributes: CFDictionaryRef,
        output_callback: VTDecompressionOutputHandler,
        decompression_session_out: *mut VTDecompressionSessionRef,
    ) -> OSStatus;

    fn VTDecompressionSessionDecodeFrame(
        session: VTDecompressionSessionRef,
        sample_buffer: CMSampleBufferRef,
        decode_flags: VTDecodeInfoFlags,
        output_refcon: *mut c_void,
        info_flags_out: *mut VTDecodeInfoFlags,
    ) -> OSStatus;

    fn VTDecompressionSessionInvalidate(session: VTDecompressionSessionRef);

    fn VTDecompressionSessionCanAcceptFormatDescription(
        session: VTDecompressionSessionRef,
        new_format_desc: CMVideoFormatDescriptionRef,
    ) -> u8;

    // CoreVideo — pixel buffer attributes for decode output
    fn CVPixelBufferGetPixelFormatType(pixel_buffer: CVPixelBufferRef) -> u32;
    fn CVPixelBufferGetPlaneCount(pixel_buffer: CVPixelBufferRef) -> usize;
    fn CVPixelBufferRetain(pixel_buffer: CVPixelBufferRef) -> CVPixelBufferRef;
}

// CVPixelBuffer pixel format types
const KCV_PIXEL_FORMAT_TYPE_420YPCBCR8BIPLANAR_VIDEO_RANGE: u32 = 0x74727670; // 'trvp' (NV12 video)
const KCV_PIXEL_FORMAT_TYPE_420YPCBCR8BIPLANAR_FULL_RANGE: u32 = 0x74727666; // 'trvf' (NV12 full)
const KCV_PIXEL_FORMAT_TYPE_420YPCCRBA8_PLANAR: u32 = 0x79343230; // 'y420' (I420)

// VTDecodeInfoFlags
const K_VT_DECODE_FRAME_DO_NOT_OUTPUT_FRAME: VTDecodeInfoFlags = 0x01;
const K_VT_DECODE_FRAME_SKIP_ENCRYPTION: VTDecodeInfoFlags = 0x02;

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
    cond: std::sync::Condvar,
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
    // Pre-allocate: each NAL has 4-byte start code + payload, total ~frame_data.len + NALs*4
    let total_nal_bytes: usize = nal_units.iter().map(|(d, _)| d.len()).sum();
    let mut data = Vec::with_capacity(total_nal_bytes + nal_units.len() * 4);
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
        ctx.cond.notify_one();
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
            cond: std::sync::Condvar::new(),
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

        // Wait for the callback to fire. VideoToolbox may call synchronously
        // for realtime mode, or asynchronously. Use Condvar to wait efficiently.
        let flush_time = CMTime::default();
        let _ = unsafe { VTCompressionSessionCompleteFrames(self.session, flush_time) };

        // If callback already fired synchronously, outputs is non-empty.
        // Otherwise wait on condvar with timeout (forced keyframe may delay).
        let timeout = std::time::Duration::from_secs(1);
        let mut guard = self.callback_ctx.outputs.lock().unwrap();
        if guard.is_empty() {
            let result = self.callback_ctx.cond.wait_timeout(guard, timeout).unwrap();
            guard = result.0;
        }
        // If still empty after timeout, try one more flush
        if guard.is_empty() {
            drop(guard);
            let _ = unsafe { VTCompressionSessionCompleteFrames(self.session, flush_time) };
            guard = self.callback_ctx.outputs.lock().unwrap();
            if guard.is_empty() {
                let result = self.callback_ctx.cond.wait_timeout(guard, timeout).unwrap();
                guard = result.0;
            }
        }

        // Extract output from the guard we already hold
        let output = if guard.is_empty() {
            None
        } else {
            let mut data = Vec::new();
            let mut is_keyframe = false;
            for out in guard.drain(..) {
                if out.keyframe {
                    is_keyframe = true;
                }
                data.extend_from_slice(&out.data);
            }
            Some((data, is_keyframe))
        };
        drop(guard);

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
                    bit_depth: 8,
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
        if self.codec_type == KCM_VIDEO_CODEC_TYPE_HEVC {
            lm_core::CodecType::H265
        } else {
            lm_core::CodecType::H264
        }
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

// ============================================================================
// VideoToolbox 硬件解码器
// ============================================================================

/// VideoToolbox 硬件解码器 (macOS)
///
/// 通过 VideoToolbox framework 的 VTDecompressionSession 进行硬件解码。
/// 支持 H.264 和 H.265/HEVC。
///
/// 使用流程:
/// 1. `new_h264()` 或 `new_h265()` 创建解码器
/// 2. `set_parameter_sets()` 设置 SPS/PPS (H.264) 或 VPS/SPS/PPS (H.265)
/// 3. `decode()` 提交编码帧进行解码
///
/// VideoToolbox 解码是异步的 (callback-based)，通过 Mutex<Vec> 收集输出。
pub struct VideoToolboxDecoder {
    session: VTDecompressionSessionRef,
    format_desc: CMVideoFormatDescriptionRef,
    callback_ctx: Arc<DecodeCallbackContext>,
    width: u32,
    height: u32,
    is_h264: bool,
    /// 保存的参数集 (SPS/PPS)，用于重建会话
    saved_params: Vec<Vec<u8>>,
    /// NAL unit header size (bytes), 从 format description 获取
    nal_header_size: usize,
    /// 输出回调队列 (备用，用于同步等待)
    output_queue: VecDeque<YuvFrame>,
    /// 是否已初始化会话
    session_initialized: bool,
}

unsafe impl Send for VideoToolboxDecoder {}
unsafe impl Sync for VideoToolboxDecoder {}

impl VideoToolboxDecoder {
    /// 检查 VideoToolbox 解码是否可用 (仅在 macOS 上返回 true)
    pub fn is_available() -> bool {
        cfg!(target_os = "macos")
    }

    /// 创建 H.264 解码器
    pub fn new_h264() -> Result<Self, VideoCodecError> {
        Self::new(true)
    }

    /// 创建 H.265/HEVC 解码器
    pub fn new_h265() -> Result<Self, VideoCodecError> {
        Self::new(false)
    }

    fn new(is_h264: bool) -> Result<Self, VideoCodecError> {
        if !Self::is_available() {
            return Err(VideoCodecError::Unsupported(
                "VideoToolbox decoder only available on macOS".into(),
            ));
        }

        Ok(Self {
            session: ptr::null_mut(),
            format_desc: ptr::null_mut(),
            callback_ctx: Arc::new(DecodeCallbackContext {
                outputs: Mutex::new(Vec::new()),
            }),
            width: 0,
            height: 0,
            is_h264,
            saved_params: Vec::new(),
            nal_header_size: 4,
            output_queue: VecDeque::new(),
            session_initialized: false,
        })
    }

    /// 设置 SPS/PPS (H.264) 或 VPS/SPS/PPS (H.265)
    ///
    /// 参数集应为不带 start code 的纯 NAL unit 数据。
    /// 调用后会创建 CMVideoFormatDescription 和 VTDecompressionSession。
    pub fn set_parameter_sets(&mut self, params: &[&[u8]]) -> Result<(), VideoCodecError> {
        if params.is_empty() {
            return Err(VideoCodecError::InvalidInput(
                "no parameter sets provided".into(),
            ));
        }

        // 保存参数集
        self.saved_params = params.iter().map(|p| p.to_vec()).collect();

        // 释放旧的 format description 和 session
        self.invalidate_session();

        // 创建 CMVideoFormatDescription
        let codec_type = if self.is_h264 {
            KCM_VIDEO_CODEC_TYPE_H264
        } else {
            KCM_VIDEO_CODEC_TYPE_HEVC
        };

        let mut format_desc: CMVideoFormatDescriptionRef = ptr::null_mut();

        let ret = unsafe {
            CMVideoFormatDescriptionCreate(
                ptr::null_mut(),
                codec_type,
                0, // width/height 从 SPS 自动推断
                0,
                ptr::null_mut(),
                &mut format_desc,
            )
        };

        if ret != NO_ERR {
            return Err(VideoCodecError::DecodeFailed(format!(
                "CMVideoFormatDescriptionCreate failed: {}",
                ret
            )));
        }

        // 获取 NAL header size 和参数集计数
        let mut param_count: usize = 0;
        let mut nal_header: i32 = 4;
        let get_ret = if self.is_h264 {
            unsafe {
                CMVideoFormatDescriptionGetH264ParameterSetAtIndex(
                    format_desc,
                    0,
                    ptr::null_mut(),
                    ptr::null_mut(),
                    &mut param_count,
                    &mut nal_header,
                )
            }
        } else {
            unsafe {
                CMVideoFormatDescriptionGetHEVCParameterSetAtIndex(
                    format_desc,
                    0,
                    ptr::null_mut(),
                    ptr::null_mut(),
                    &mut param_count,
                    &mut nal_header,
                )
            }
        };

        if get_ret == NO_ERR {
            self.nal_header_size = nal_header as usize;
        }

        // 获取视频尺寸
        let dims = unsafe { CMVideoFormatDescriptionGetDimensions(format_desc) };
        if dims.width > 0 && dims.height > 0 {
            self.width = dims.width as u32;
            self.height = dims.height as u32;
        }

        self.format_desc = format_desc;

        // 创建解码会话
        self.create_session()?;

        Ok(())
    }

    /// 创建 VTDecompressionSession
    fn create_session(&mut self) -> Result<(), VideoCodecError> {
        if self.format_desc.is_null() {
            return Err(VideoCodecError::NotInitialized);
        }

        // 释放旧会话
        if !self.session.is_null() {
            unsafe { VTDecompressionSessionInvalidate(self.session) };
            self.session = ptr::null_mut();
        }

        // 清空输出队列
        if let Ok(mut outputs) = self.callback_ctx.outputs.lock() {
            outputs.clear();
        }

        let mut session: VTDecompressionSessionRef = ptr::null_mut();

        // 创建解码会话
        // 注意: 完整实现需要设置 output callback (VTDecompressionOutputHandler)
        // 这里使用简化框架 — 实际 GPU 解码需要完整的回调设置
        let ret = unsafe {
            VTDecompressionSessionCreate(
                ptr::null_mut(),
                self.format_desc,
                ptr::null_mut(), // decoder specification
                ptr::null_mut(), // destination image buffer attributes
                ptr::null_mut(), // output callback (简化: 使用 null)
                &mut session,
            )
        };

        if ret != NO_ERR {
            return Err(VideoCodecError::DecodeFailed(format!(
                "VTDecompressionSessionCreate failed: {}",
                ret
            )));
        }

        self.session = session;
        self.session_initialized = true;

        Ok(())
    }

    /// 释放当前会话和 format description
    fn invalidate_session(&mut self) {
        unsafe {
            if !self.session.is_null() {
                VTDecompressionSessionInvalidate(self.session);
                self.session = ptr::null_mut();
            }
            if !self.format_desc.is_null() {
                CFRelease(self.format_desc as CFTypeRef);
                self.format_desc = ptr::null_mut();
            }
        }
        self.session_initialized = false;
    }

    /// 从 CVPixelBuffer 提取 YUV 数据并转换为 YuvFrame
    ///
    /// 支持 NV12 (biplanar) 和 I420 (planar) 格式。
    unsafe fn extract_yuv_from_pixel_buffer(
        &self,
        pixel_buffer: CVPixelBufferRef,
        timestamp: u64,
        keyframe: bool,
    ) -> Option<YuvFrame> {
        if pixel_buffer.is_null() {
            return None;
        }

        let pixel_format = CVPixelBufferGetPixelFormatType(pixel_buffer);
        let plane_count = CVPixelBufferGetPlaneCount(pixel_buffer);

        CVPixelBufferLockBaseAddress(pixel_buffer, 0);

        let result = if pixel_format == KCV_PIXEL_FORMAT_TYPE_420YPCBCR8BIPLANAR_VIDEO_RANGE
            || pixel_format == KCV_PIXEL_FORMAT_TYPE_420YPCBCR8BIPLANAR_FULL_RANGE
        {
            // NV12: 2 planes (Y + interleaved UV)
            if plane_count < 2 {
                None
            } else {
                let width = CVPixelBufferGetWidthOfPlane(pixel_buffer, 0) as u32;
                let height = CVPixelBufferGetHeightOfPlane(pixel_buffer, 0) as u32;
                let y_stride = CVPixelBufferGetBytesPerRowOfPlane(pixel_buffer, 0);
                let uv_stride = CVPixelBufferGetBytesPerRowOfPlane(pixel_buffer, 1);
                let y_ptr = CVPixelBufferGetBaseAddressOfPlane(pixel_buffer, 0) as *const u8;
                let uv_ptr = CVPixelBufferGetBaseAddressOfPlane(pixel_buffer, 1) as *const u8;

                if width == 0 || height == 0 || y_ptr.is_null() || uv_ptr.is_null() {
                    None
                } else {
                    let y_size = (width * height) as usize;
                    let uv_size = ((width / 2) * (height / 2)) as usize;
                    let mut y = vec![0u8; y_size];
                    let mut u = vec![128u8; uv_size];
                    let mut v = vec![128u8; uv_size];

                    // Copy Y plane
                    for row in 0..height as usize {
                        ptr::copy_nonoverlapping(
                            y_ptr.add(row * y_stride),
                            y.as_mut_ptr().add(row * width as usize),
                            width as usize,
                        );
                    }

                    // Deinterleave UV (NV12: [U0 V0 U1 V1...] → separate U, V planes)
                    let uv_w = (width / 2) as usize;
                    let uv_h = (height / 2) as usize;
                    for row in 0..uv_h {
                        let uv_row = uv_ptr.add(row * uv_stride);
                        for col in 0..uv_w {
                            u[row * uv_w + col] = *uv_row.add(col * 2);
                            v[row * uv_w + col] = *uv_row.add(col * 2 + 1);
                        }
                    }

                    Some(YuvFrame {
                        y,
                        u,
                        v,
                        width,
                        height,
                        timestamp,
                        keyframe,
                        bit_depth: 8,
                        y16: Vec::new(),
                        u16: Vec::new(),
                        v16: Vec::new(),
                    })
                }
            }
        } else if pixel_format == KCV_PIXEL_FORMAT_TYPE_420YPCCRBA8_PLANAR
            || pixel_format == KCV_PIXEL_FORMAT_TYPE_420YPCBCR8_PLANAR
        {
            // I420: 3 planes (Y, U, V)
            if plane_count < 3 {
                None
            } else {
                let width = CVPixelBufferGetWidthOfPlane(pixel_buffer, 0) as u32;
                let height = CVPixelBufferGetHeightOfPlane(pixel_buffer, 0) as u32;
                let y_stride = CVPixelBufferGetBytesPerRowOfPlane(pixel_buffer, 0);
                let u_stride = CVPixelBufferGetBytesPerRowOfPlane(pixel_buffer, 1);
                let v_stride = CVPixelBufferGetBytesPerRowOfPlane(pixel_buffer, 2);
                let y_ptr = CVPixelBufferGetBaseAddressOfPlane(pixel_buffer, 0) as *const u8;
                let u_ptr = CVPixelBufferGetBaseAddressOfPlane(pixel_buffer, 1) as *const u8;
                let v_ptr = CVPixelBufferGetBaseAddressOfPlane(pixel_buffer, 2) as *const u8;

                if width == 0 || height == 0 || y_ptr.is_null() {
                    None
                } else {
                    let y_size = (width * height) as usize;
                    let uv_size = ((width / 2) * (height / 2)) as usize;
                    let mut y = vec![0u8; y_size];
                    let mut u = vec![128u8; uv_size];
                    let mut v = vec![128u8; uv_size];

                    for row in 0..height as usize {
                        ptr::copy_nonoverlapping(
                            y_ptr.add(row * y_stride),
                            y.as_mut_ptr().add(row * width as usize),
                            width as usize,
                        );
                    }
                    let uv_w = (width / 2) as usize;
                    let uv_h = (height / 2) as usize;
                    for row in 0..uv_h {
                        ptr::copy_nonoverlapping(
                            u_ptr.add(row * u_stride),
                            u.as_mut_ptr().add(row * uv_w),
                            uv_w,
                        );
                        ptr::copy_nonoverlapping(
                            v_ptr.add(row * v_stride),
                            v.as_mut_ptr().add(row * uv_w),
                            uv_w,
                        );
                    }

                    Some(YuvFrame {
                        y,
                        u,
                        v,
                        width,
                        height,
                        timestamp,
                        keyframe,
                        bit_depth: 8,
                        y16: Vec::new(),
                        u16: Vec::new(),
                        v16: Vec::new(),
                    })
                }
            }
        } else {
            // Unknown pixel format
            None
        };

        CVPixelBufferUnlockBaseAddress(pixel_buffer, 0);
        result
    }

    /// 将 Annex B 格式的 NAL unit 数据转换为 AVCC 格式 (4-byte length prefix)
    /// VideoToolbox 解码需要 AVCC 格式
    fn annexb_to_avcc(data: &[u8], nal_header_size: usize) -> Vec<u8> {
        let mut output = Vec::with_capacity(data.len() + 16);
        let mut i = 0usize;

        while i < data.len() {
            // 查找 start code (00 00 01 或 00 00 00 01)
            let start_code_len =
                if i + 3 <= data.len() && data[i] == 0 && data[i + 1] == 0 && data[i + 2] == 1 {
                    3
                } else if i + 4 <= data.len()
                    && data[i] == 0
                    && data[i + 1] == 0
                    && data[i + 2] == 0
                    && data[i + 3] == 1
                {
                    4
                } else {
                    i += 1;
                    continue;
                };

            let nal_start = i + start_code_len;

            // 查找下一个 start code 或数据末尾
            let mut nal_end = data.len();
            let mut j = nal_start + 1;
            while j + 3 <= data.len() {
                if data[j] == 0 && data[j + 1] == 0 && data[j + 2] == 1 {
                    nal_end = j;
                    break;
                }
                if j + 4 <= data.len()
                    && data[j] == 0
                    && data[j + 1] == 0
                    && data[j + 2] == 0
                    && data[j + 3] == 1
                {
                    nal_end = j;
                    break;
                }
                j += 1;
            }

            let nal_data = &data[nal_start..nal_end];
            let nal_len = nal_data.len() as u32;

            // 写入 4-byte length prefix (big-endian)
            output.extend_from_slice(&nal_len.to_be_bytes());
            output.extend_from_slice(nal_data);

            i = nal_end;
        }

        let _ = nal_header_size; // AVCC always uses 4-byte length prefix
        output
    }

    /// 获取视频宽度
    pub fn width(&self) -> u32 {
        self.width
    }

    /// 获取视频高度
    pub fn height(&self) -> u32 {
        self.height
    }

    /// 是否已初始化 (已设置参数集并创建会话)
    pub fn is_initialized(&self) -> bool {
        self.session_initialized
    }
}

impl Drop for VideoToolboxDecoder {
    fn drop(&mut self) {
        self.invalidate_session();
    }
}

impl VideoDecoder for VideoToolboxDecoder {
    fn decode(&mut self, data: &[u8], timestamp: u64) -> Result<YuvFrame, VideoCodecError> {
        if data.is_empty() {
            return Err(VideoCodecError::InvalidInput("empty decode data".into()));
        }

        if !self.session_initialized || self.session.is_null() {
            return Err(VideoCodecError::NotInitialized);
        }

        // 将 Annex B 转换为 AVCC 格式
        let avcc_data = Self::annexb_to_avcc(data, self.nal_header_size);
        if avcc_data.is_empty() {
            return Err(VideoCodecError::InvalidInput(
                "no valid NAL units found in data".into(),
            ));
        }

        // 创建 CMBlockBuffer 包含 AVCC 数据
        let mut block_buffer: CMBlockBufferRef = ptr::null_mut();
        let data_len = avcc_data.len();

        let ret = unsafe {
            CMBlockBufferCreateWithMemoryBlock(
                ptr::null_mut(),
                ptr::null_mut(), // let CoreMedia allocate
                data_len,
                ptr::null_mut(),
                ptr::null_mut(),
                0,
                data_len,
                0,
                &mut block_buffer,
            )
        };

        if ret != NO_ERR || block_buffer.is_null() {
            return Err(VideoCodecError::DecodeFailed(format!(
                "CMBlockBufferCreateWithMemoryBlock failed: {}",
                ret
            )));
        }

        // Copy data into block buffer
        let copy_ret = unsafe {
            CMBlockBufferCopyDataBytes(block_buffer, 0, data_len, avcc_data.as_ptr() as *mut c_void)
        };

        if copy_ret != NO_ERR {
            unsafe { CFRelease(block_buffer as CFTypeRef) };
            return Err(VideoCodecError::DecodeFailed(format!(
                "CMBlockBufferCopyDataBytes failed: {}",
                copy_ret
            )));
        }

        // 创建 CMSampleBuffer
        let mut sample_buffer: CMSampleBufferRef = ptr::null_mut();
        let sample_size = data_len;

        let ret = unsafe {
            CMSampleBufferCreate(
                ptr::null_mut(),
                block_buffer,
                1, // data_ready
                ptr::null_mut(),
                ptr::null_mut(),
                self.format_desc,
                1, // num_samples
                0, // num_sample_timing_entries
                ptr::null(),
                1, // num_sample_size_entries
                &sample_size,
                &mut sample_buffer,
            )
        };

        if ret != NO_ERR {
            unsafe { CFRelease(block_buffer as CFTypeRef) };
            return Err(VideoCodecError::DecodeFailed(format!(
                "CMSampleBufferCreate failed: {}",
                ret
            )));
        }

        // 提交解码
        let mut info_flags: VTDecodeInfoFlags = 0;
        let ret = unsafe {
            VTDecompressionSessionDecodeFrame(
                self.session,
                sample_buffer,
                0,
                ptr::null_mut(),
                &mut info_flags,
            )
        };

        // 释放 sample buffer (block buffer 会被 sample buffer 释放)
        unsafe { CFRelease(sample_buffer as CFTypeRef) };

        if ret != NO_ERR {
            return Err(VideoCodecError::DecodeFailed(format!(
                "VTDecompressionSessionDecodeFrame failed: {}",
                ret
            )));
        }

        // 检查输出队列 (简化框架: 实际回调需要完整设置)
        // 尝试从回调上下文获取输出
        if let Ok(mut outputs) = self.callback_ctx.outputs.lock() {
            if let Some(frame) = outputs.pop() {
                return Ok(frame);
            }
        }

        // 回调未设置时, 返回错误 (框架限制)
        // 完整实现需要设置 VTDecompressionOutputHandler 回调
        Err(VideoCodecError::DecodeFailed(
            "VideoToolbox decode callback not configured (framework limitation)".into(),
        ))
    }

    fn codec(&self) -> lm_core::CodecType {
        if self.is_h264 {
            lm_core::CodecType::H264
        } else {
            lm_core::CodecType::H265
        }
    }
}
