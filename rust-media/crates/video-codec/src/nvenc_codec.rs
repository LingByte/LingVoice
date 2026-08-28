//! Linux NVENC 硬件加速编码器
//!
//! 通过 NVIDIA NVENC SDK 进行 H.264/HEVC 硬件编码。
//! 仅在 Linux + NVIDIA GPU 环境下可用。
//!
//! NVENC 使用异步编码模型，通过 output buffer 回调收集编码数据。
//! 编码流程:
//!   1. NvEncInitializeEncoder — 创建编码器并设置参数
//!   2. NvEncCreateInputBuffer — 分配 GPU 输入缓冲区
//!   3. NvEncEncodeFrame — 提交帧到 GPU 编码
//!   4. NvEncLockBitstream — 获取编码后的码流
//!   5. NvEncUnlockBitstream — 释放码流缓冲区
//!
//! 注意: 此模块仅在 Linux 上编译。macOS 上不会包含此模块。
//! 需要系统安装 NVIDIA 驱动和 NVENC SDK。
//!
//! ## 动态加载
//!
//! NVENC API 通过 `libloading` 动态加载 `libnvidia-encode.so.1`，
//! 避免编译时链接依赖。`is_available()` 检测运行时环境是否满足要求:
//!   1. `/proc/driver/nvidia` 存在 (NVIDIA 驱动已加载)
//!   2. `libnvidia-encode.so.1` 可通过 dlopen 加载
//!   3. `NvEncodeAPIGetMaxSupportedVersion` 返回成功

#![allow(dead_code)]

use crate::{EncodedFrame, EncoderConfig, VideoCodecError, VideoEncoder, YuvFrame};
use libloading::Library;
use std::ffi::c_void;
use std::path::Path;
use std::ptr;

// NVENC SDK 常量
const NV_ENC_SUCCESS: u32 = 0;
const NV_ENC_ERR_INVALID_VERSION: u32 = 2;
const NV_ENC_ERR_NO_ENCODE_DEVICE: u32 = 7;
const NV_ENC_ERR_UNSUPPORTED_DEVICE: u32 = 12;

// 编码器初始化参数版本
const NV_ENC_OPEN_API_VER: u32 = 0x10;

// H.264 codec GUID
const NV_ENC_CODEC_H264_GUID: [u8; 16] = [
    0x34, 0x47, 0x41, 0x31, 0x59, 0x6f, 0x4a, 0x4a, 0xb2, 0x8c, 0x8e, 0x70, 0x3f, 0x52, 0x37, 0x42,
];

// HEVC codec GUID
const NV_ENC_CODEC_HEVC_GUID: [u8; 16] = [
    0x36, 0x47, 0x41, 0x31, 0x59, 0x6f, 0x4a, 0x4a, 0xb2, 0x8c, 0x8e, 0x70, 0x3f, 0x52, 0x37, 0x42,
];

// 预设 GUID (low latency)
const NV_ENC_PRESET_P4_GUID: [u8; 16] = [
    0x6f, 0x27, 0x44, 0x4a, 0x6f, 0x6c, 0x4f, 0x61, 0xa3, 0x89, 0x86, 0x32, 0x0e, 0x04, 0x07, 0xc4,
];

// NV_ENC_BUFFER_FORMAT_NV12
const NV_ENC_BUFFER_FORMAT_NV12: u32 = 0x10;

// NV_ENC_PIC_PARAMS version
const NV_ENC_PIC_PARAMS_VER: u32 = 0x20;

#[repr(C)]
struct NvEncOpenEncodeSessionExParams {
    version: u32,
    deviceType: u32,
    device: *mut c_void,
    reserved1: *mut c_void,
    apiVersion: u32,
    reserved: [*mut c_void; 52],
}

#[repr(C)]
struct NvEncInitializeParams {
    version: u32,
    encodeGUID: [u8; 16],
    presetGUID: [u8; 16],
    encodeWidth: u32,
    encodeHeight: u32,
    darWidth: u32,
    darHeight: u32,
    frameRateNum: u32,
    frameRateDen: u32,
    enableEncodeAsync: u32,
    enablePTD: u32,
    reportSliceOffsets: u32,
    enableSubFrameWrite: u32,
    enableExternalMEHints: u32,
    enableMEOnlyMode: u32,
    enableWeightedPrediction: u32,
    enableOutputInVidmem: u32,
    reserved1: u32,
    reserved2: *mut c_void,
    reserved3: [u32; 219],
    encodeConfig: *mut c_void,
    maxEncodeWidth: u32,
    maxEncodeHeight: u32,
    maxMEHintCountsPerBlock: u32,
}

#[repr(C)]
struct NvEncCreateInputBuffer {
    version: u32,
    width: u32,
    height: u32,
    bufferFmt: u32,
    reserved: u32,
    inputBuffer: *mut c_void,
    sysMemBuffer: *mut c_void,
    reserved1: [*mut c_void; 57],
}

#[repr(C)]
struct NvEncLockInputBuffer {
    version: u32,
    inputBuffer: *mut c_void,
    bufferData: *mut c_void,
    pitch: u32,
    reserved1: u32,
    reserved: [*mut c_void; 241],
}

#[repr(C)]
struct NvEncLockBitstream {
    version: u32,
    outputBitstream: *mut c_void,
    sliceOffsets: *mut c_void,
    frameIdx: u32,
    hwEncodeStatus: u32,
    numSlices: u32,
    bitstreamSizeInBytes: u32,
    outputTimeStamp: u64,
    outputDuration: u64,
    bitstreamBufferPtr: *mut c_void,
    /// NV_ENC_PIC_TYPE: 1=IDR, 2=I, 3=P, 4=B, 5=BI
    pictureType: u32,
    reserved1: [*mut c_void; 57],
}

#[repr(C)]
struct NvEncPicParams {
    version: u32,
    inputWidth: u32,
    inputHeight: u32,
    inputPitch: u32,
    encodeParams: NvEncPicEncodeParams,
    inputBuffer: *mut c_void,
    outputBitstream: *mut c_void,
    completionEvent: *mut c_void,
    bufferFmt: u32,
    reserved1: u32,
    inputTimeStamp: u64,
    inputDuration: u64,
    reserved: [*mut c_void; 246],
}

#[repr(C)]
struct NvEncPicEncodeParams {
    fieldEncodingMode: u32,
    frameType: u32,
    reserved1: [u32; 14],
}

// NVENC API function pointer table
#[repr(C)]
struct NvEncodeApiTable {
    nvEncOpenEncodeSession: *mut c_void,
    nvEncInitializeEncoder: *mut c_void,
    nvEncTerminateEncoder: *mut c_void,
    nvEncCreateInputBuffer: *mut c_void,
    nvEncDestroyInputBuffer: *mut c_void,
    nvEncLockInputBuffer: *mut c_void,
    nvEncUnlockInputBuffer: *mut c_void,
    nvEncEncodeFrame: *mut c_void,
    nvEncLockBitstream: *mut c_void,
    nvEncUnlockBitstream: *mut c_void,
    // Additional function pointers omitted for brevity
    reserved: [*mut c_void; 100],
}

/// NVENC SDK 入口函数类型: `NvEncodeAPIGetMaxSupportedVersion(uint32_t* version)`
type NvEncodeAPIGetMaxSupportedVersionFn = unsafe extern "C" fn(version: *mut u32) -> u32;

/// NVENC SDK 入口函数类型: `NvEncodeAPICreateInstance(NV_ENCODE_API_FUNCTION_LIST*)`
type NvEncodeAPICreateInstanceFn =
    unsafe extern "C" fn(function_list: *mut NvEncodeApiTable) -> u32;

// NVENC API 函数指针类型 (通过 api_table 调用)
type NvEncCreateInputBufferFn =
    unsafe extern "C" fn(encoder: *mut c_void, params: *mut NvEncCreateInputBuffer) -> u32;
type NvEncDestroyInputBufferFn =
    unsafe extern "C" fn(encoder: *mut c_void, input_buffer: *mut c_void) -> u32;
type NvEncLockInputBufferFn =
    unsafe extern "C" fn(encoder: *mut c_void, params: *mut NvEncLockInputBuffer) -> u32;
type NvEncUnlockInputBufferFn =
    unsafe extern "C" fn(encoder: *mut c_void, input_buffer: *mut c_void) -> u32;
type NvEncEncodeFrameFn =
    unsafe extern "C" fn(encoder: *mut c_void, params: *mut NvEncPicParams) -> u32;
type NvEncLockBitstreamFn =
    unsafe extern "C" fn(encoder: *mut c_void, params: *mut NvEncLockBitstream) -> u32;
type NvEncUnlockBitstreamFn =
    unsafe extern "C" fn(encoder: *mut c_void, output_bitstream: *mut c_void) -> u32;

/// NVENC 硬件编码器
///
/// 通过 `libloading` 动态加载 `libnvidia-encode.so.1` 获取 NVENC API 函数指针。
/// 编码器在 `new()` 中完成 API 初始化流程，但实际编码操作需要完整的
/// GPU 输入缓冲区管理（此处仅搭建框架）。
pub struct NvencEncoder {
    config: EncoderConfig,
    is_h264: bool,
    /// dlopen 加载的 libnvidia-encode.so.1 库句柄 (保持存活以维持函数指针有效)
    _library: Library,
    /// NVENC API 函数指针表
    api_table: NvEncodeApiTable,
    encoder: *mut c_void,
    input_buffer: *mut c_void,
    output_buffer: *mut c_void,
    frame_count: u64,
    force_keyframe: bool,
}

unsafe impl Send for NvencEncoder {}
unsafe impl Sync for NvencEncoder {}

impl NvencEncoder {
    /// 创建 H.264 NVENC 编码器
    pub fn new_h264(config: EncoderConfig) -> Result<Self, VideoCodecError> {
        Self::new(config, true)
    }

    /// 创建 HEVC NVENC 编码器
    pub fn new_h265(config: EncoderConfig) -> Result<Self, VideoCodecError> {
        Self::new(config, false)
    }

    fn new(config: EncoderConfig, is_h264: bool) -> Result<Self, VideoCodecError> {
        // 检查 NVENC 运行时环境是否可用
        if !Self::is_available() {
            return Err(VideoCodecError::NotInitialized);
        }

        // 通过 libloading 动态加载 libnvidia-encode.so.1
        let library = unsafe { Library::new("libnvidia-encode.so.1") }
            .map_err(|_| VideoCodecError::NotInitialized)?;

        // 加载 NvEncodeAPICreateInstance 入口函数
        let create_instance: libloading::Symbol<NvEncodeAPICreateInstanceFn> =
            unsafe { library.get(b"NvEncodeAPICreateInstance\0") }
                .map_err(|_| VideoCodecError::NotInitialized)?;

        // 初始化 API 函数指针表
        let mut api_table = NvEncodeApiTable {
            nvEncOpenEncodeSession: ptr::null_mut(),
            nvEncInitializeEncoder: ptr::null_mut(),
            nvEncTerminateEncoder: ptr::null_mut(),
            nvEncCreateInputBuffer: ptr::null_mut(),
            nvEncDestroyInputBuffer: ptr::null_mut(),
            nvEncLockInputBuffer: ptr::null_mut(),
            nvEncUnlockInputBuffer: ptr::null_mut(),
            nvEncEncodeFrame: ptr::null_mut(),
            nvEncLockBitstream: ptr::null_mut(),
            nvEncUnlockBitstream: ptr::null_mut(),
            reserved: [ptr::null_mut(); 100],
        };

        // 调用 NvEncodeAPICreateInstance 填充函数指针表
        let ret = unsafe { create_instance(&mut api_table) };
        if ret != NV_ENC_SUCCESS {
            return Err(VideoCodecError::NotInitialized);
        }

        // 验证关键函数指针已填充
        if api_table.nvEncOpenEncodeSession.is_null() || api_table.nvEncInitializeEncoder.is_null()
        {
            return Err(VideoCodecError::NotInitialized);
        }

        // 选择 codec GUID (H.264 或 HEVC)
        let codec_guid = if is_h264 {
            NV_ENC_CODEC_H264_GUID
        } else {
            NV_ENC_CODEC_HEVC_GUID
        };

        // 构建 NVENC 编码器初始化参数
        // 注意: 完整的编码器创建需要 GPU device handle，此处仅搭建框架
        let _init_params = NvEncInitializeParams {
            version: NV_ENC_OPEN_API_VER,
            encodeGUID: codec_guid,
            presetGUID: NV_ENC_PRESET_P4_GUID,
            encodeWidth: config.width,
            encodeHeight: config.height,
            darWidth: config.width,
            darHeight: config.height,
            frameRateNum: config.framerate,
            frameRateDen: 1,
            enableEncodeAsync: 0,
            enablePTD: 1,
            reportSliceOffsets: 0,
            enableSubFrameWrite: 0,
            enableExternalMEHints: 0,
            enableMEOnlyMode: 0,
            enableWeightedPrediction: 0,
            enableOutputInVidmem: 0,
            reserved1: 0,
            reserved2: ptr::null_mut(),
            reserved3: [0; 219],
            encodeConfig: ptr::null_mut(),
            maxEncodeWidth: config.width,
            maxEncodeHeight: config.height,
            maxMEHintCountsPerBlock: 0,
        };

        // 框架就绪: API 函数指针已加载，编码器配置已构建。
        // 实际的 NvEncOpenEncodeSession + NvEncInitializeEncoder 调用
        // 需要 CUDA/OpenGL device context，此处保留为框架状态。
        Ok(Self {
            config,
            is_h264,
            _library: library,
            api_table,
            encoder: ptr::null_mut(),
            input_buffer: ptr::null_mut(),
            output_buffer: ptr::null_mut(),
            frame_count: 0,
            force_keyframe: false,
        })
    }

    /// 检测 NVENC 是否可用（NVIDIA GPU + 驱动）
    ///
    /// 检查步骤:
    ///   1. `/proc/driver/nvidia` 是否存在 (Linux NVIDIA 驱动已加载)
    ///   2. `libnvidia-encode.so.1` 是否可通过 dlopen 加载
    ///   3. `NvEncodeAPIGetMaxSupportedVersion` 是否返回成功
    ///
    /// 在 macOS 上始终返回 false (无 `/proc/driver/nvidia`)。
    pub fn is_available() -> bool {
        // 步骤 1: 检查 NVIDIA 驱动是否已加载
        if !Path::new("/proc/driver/nvidia").exists() {
            return false;
        }

        // 步骤 2: 尝试 dlopen libnvidia-encode.so.1
        let library = match unsafe { Library::new("libnvidia-encode.so.1") } {
            Ok(lib) => lib,
            Err(_) => return false,
        };

        // 步骤 3: 调用 NvEncodeAPIGetMaxSupportedVersion 验证 API 可用性
        let get_max_version: libloading::Symbol<NvEncodeAPIGetMaxSupportedVersionFn> =
            match unsafe { library.get(b"NvEncodeAPIGetMaxSupportedVersion\0") } {
                Ok(sym) => sym,
                Err(_) => return false,
            };

        let mut version: u32 = 0;
        let ret = unsafe { get_max_version(&mut version) };
        // NV_ENC_SUCCESS (0) 表示成功
        if ret != NV_ENC_SUCCESS {
            return false;
        }

        // version 编码: major = version >> 4, minor = version & 0xF
        // 只要返回成功即可，不限制具体版本
        true
    }
}

impl Drop for NvencEncoder {
    fn drop(&mut self) {
        if !self.encoder.is_null() {
            // nvEncTerminateEncoder + close
            // 实际实现中应调用 api_table.nvEncTerminateEncoder(encoder)
        }
    }
}

impl NvencEncoder {
    /// 将 YuvFrame (I420) 转换为 NV12 格式。
    ///
    /// NV12 格式: Y plane (height 行 × width 字节) + 交错的 UV plane (height/2 行 × width 字节)。
    /// UV plane 中 U 和 V 交替存储: U0, V0, U1, V1, ...
    fn yuv_to_nv12(frame: &YuvFrame) -> Vec<u8> {
        let w = frame.width as usize;
        let h = frame.height as usize;
        let uv_h = h / 2;
        // NV12: Y plane + interleaved UV plane
        let nv12_size = w * h + w * uv_h;
        let mut nv12 = vec![0u8; nv12_size];

        // Copy Y plane
        for row in 0..h {
            let src = &frame.y[row * frame.y_stride()..row * frame.y_stride() + w];
            nv12[row * w..row * w + w].copy_from_slice(src);
        }

        // Interleave U and V into UV plane
        let uv_offset = w * h;
        let uv_w = w / 2;
        for row in 0..uv_h {
            let u_row = &frame.u[row * frame.uv_stride()..row * frame.uv_stride() + uv_w];
            let v_row = &frame.v[row * frame.uv_stride()..row * frame.uv_stride() + uv_w];
            let dst_offset = uv_offset + row * w;
            for col in 0..uv_w {
                nv12[dst_offset + col * 2] = u_row[col];
                nv12[dst_offset + col * 2 + 1] = v_row[col];
            }
        }

        nv12
    }

    /// 从编码后的 bitstream 中检测 keyframe (fallback 方法)
    ///
    /// H.264: 查找 NALU type 5 (IDR) 或 type 7 (SPS)
    /// H.265: 查找 NALU type 19 (IDR_W_RADL) 或 type 20 (IDR_N_LP) 或 type 32 (VPS)
    fn detect_keyframe_from_bitstream(&self, data: &[u8]) -> bool {
        if self.is_h264 {
            // H.264: 搜索 NALU type 5 (IDR slice) 或 type 7 (SPS)
            let mut i = 0;
            while i + 5 < data.len() {
                // 查找起始码 (0x000001 或 0x00000001)
                if (data[i..i + 4] == [0, 0, 0, 1])
                    || (i + 3 <= data.len() && data[i..i + 3] == [0, 0, 1])
                {
                    let start = if data[i..i + 4] == [0, 0, 0, 1] {
                        i + 4
                    } else {
                        i + 3
                    };
                    if start < data.len() {
                        let nalu_type = data[start] & 0x1F;
                        // 5 = IDR slice, 7 = SPS (keyframe indicators)
                        if nalu_type == 5 || nalu_type == 7 {
                            return true;
                        }
                    }
                    i = start;
                } else {
                    i += 1;
                }
            }
            false
        } else {
            // H.265: 搜索 NALU type 19/20 (IDR) 或 32 (VPS)
            let mut i = 0;
            while i + 5 < data.len() {
                if (data[i..i + 4] == [0, 0, 0, 1])
                    || (i + 3 <= data.len() && data[i..i + 3] == [0, 0, 1])
                {
                    let start = if data[i..i + 4] == [0, 0, 0, 1] {
                        i + 4
                    } else {
                        i + 3
                    };
                    if start < data.len() {
                        let nalu_type = (data[start] >> 1) & 0x3F;
                        // 19 = IDR_W_RADL, 20 = IDR_N_LP, 32 = VPS
                        if nalu_type == 19 || nalu_type == 20 || nalu_type == 32 {
                            return true;
                        }
                    }
                    i = start;
                } else {
                    i += 1;
                }
            }
            false
        }
    }
}

impl VideoEncoder for NvencEncoder {
    fn encode(&mut self, frame: &YuvFrame) -> Result<EncodedFrame, VideoCodecError> {
        if frame.width != self.config.width || frame.height != self.config.height {
            return Err(VideoCodecError::InvalidInput(format!(
                "frame dimensions {}x{} != encoder {}x{}",
                frame.width, frame.height, self.config.width, self.config.height
            )));
        }

        // 检查 encoder 是否已初始化 (需要 GPU device)
        if self.encoder.is_null() {
            return Err(VideoCodecError::EncodeFailed(
                "NVENC encoder not initialized: GPU device required for encoding".into(),
            ));
        }

        // 步骤 1: 将 YuvFrame (I420) 转换为 NV12
        let nv12_data = Self::yuv_to_nv12(frame);
        let w = self.config.width;
        let h = self.config.height;

        // 步骤 2: 创建 NVENC input buffer (如果尚未创建)
        if self.input_buffer.is_null() {
            let mut create_input_params = NvEncCreateInputBuffer {
                version: NV_ENC_PIC_PARAMS_VER,
                width: w,
                height: h,
                bufferFmt: NV_ENC_BUFFER_FORMAT_NV12,
                reserved: 0,
                inputBuffer: ptr::null_mut(),
                sysMemBuffer: ptr::null_mut(),
                reserved1: [ptr::null_mut(); 57],
            };

            let create_fn: NvEncCreateInputBufferFn =
                unsafe { std::mem::transmute(self.api_table.nvEncCreateInputBuffer) };
            let ret = unsafe { create_fn(self.encoder, &mut create_input_params) };
            if ret != NV_ENC_SUCCESS {
                return Err(VideoCodecError::EncodeFailed(format!(
                    "NvEncCreateInputBuffer failed: error code {}",
                    ret
                )));
            }
            self.input_buffer = create_input_params.inputBuffer;
        }

        // 步骤 3: Lock input buffer 并复制 NV12 数据
        let mut lock_input_params = NvEncLockInputBuffer {
            version: NV_ENC_PIC_PARAMS_VER,
            inputBuffer: self.input_buffer,
            bufferData: ptr::null_mut(),
            pitch: 0,
            reserved1: 0,
            reserved: [ptr::null_mut(); 241],
        };

        let lock_fn: NvEncLockInputBufferFn =
            unsafe { std::mem::transmute(self.api_table.nvEncLockInputBuffer) };
        let ret = unsafe { lock_fn(self.encoder, &mut lock_input_params) };
        if ret != NV_ENC_SUCCESS {
            return Err(VideoCodecError::EncodeFailed(format!(
                "NvEncLockInputBuffer failed: error code {}",
                ret
            )));
        }

        // 将 NV12 数据复制到 locked buffer (按 pitch 对齐)
        let pitch = lock_input_params.pitch as usize;
        let buffer_data = lock_input_params.bufferData as *mut u8;
        let y_size = (w as usize) * (h as usize);
        let uv_h = (h / 2) as usize;

        // Copy Y plane (with pitch alignment)
        unsafe {
            for row in 0..h as usize {
                let dst = buffer_data.add(row * pitch);
                let src = &nv12_data[row * w as usize..row * w as usize + w as usize];
                ptr::copy_nonoverlapping(src.as_ptr(), dst, w as usize);
            }
            // Copy interleaved UV plane (with pitch alignment)
            let uv_offset = y_size;
            for row in 0..uv_h {
                let dst = buffer_data.add((h as usize) * pitch + row * pitch);
                let src = &nv12_data
                    [uv_offset + row * w as usize..uv_offset + row * w as usize + w as usize];
                ptr::copy_nonoverlapping(src.as_ptr(), dst, w as usize);
            }
        }

        // Unlock input buffer
        let unlock_fn: NvEncUnlockInputBufferFn =
            unsafe { std::mem::transmute(self.api_table.nvEncUnlockInputBuffer) };
        let _ = unsafe { unlock_fn(self.encoder, self.input_buffer) };

        // 步骤 4: 提交帧到 NVENC 编码
        let mut pic_params = NvEncPicParams {
            version: NV_ENC_PIC_PARAMS_VER,
            inputWidth: w,
            inputHeight: h,
            inputPitch: pitch as u32,
            encodeParams: NvEncPicEncodeParams {
                fieldEncodingMode: 0,
                frameType: if self.force_keyframe { 2 } else { 0 }, // 2=IDR, 0=auto
                reserved1: [0; 14],
            },
            inputBuffer: self.input_buffer,
            outputBitstream: self.output_buffer,
            completionEvent: ptr::null_mut(),
            bufferFmt: NV_ENC_BUFFER_FORMAT_NV12,
            reserved1: 0,
            inputTimeStamp: frame.timestamp,
            inputDuration: 1,
            reserved: [ptr::null_mut(); 246],
        };

        if self.force_keyframe {
            self.force_keyframe = false;
        }

        let encode_fn: NvEncEncodeFrameFn =
            unsafe { std::mem::transmute(self.api_table.nvEncEncodeFrame) };
        let ret = unsafe { encode_fn(self.encoder, &mut pic_params) };
        if ret != NV_ENC_SUCCESS {
            return Err(VideoCodecError::EncodeFailed(format!(
                "NvEncEncodeFrame failed: error code {}",
                ret
            )));
        }

        // 步骤 5: 获取编码后的 bitstream
        let mut lock_bitstream_params = NvEncLockBitstream {
            version: NV_ENC_PIC_PARAMS_VER,
            outputBitstream: self.output_buffer,
            sliceOffsets: ptr::null_mut(),
            frameIdx: 0,
            hwEncodeStatus: 0,
            numSlices: 0,
            bitstreamSizeInBytes: 0,
            outputTimeStamp: 0,
            outputDuration: 0,
            bitstreamBufferPtr: ptr::null_mut(),
            reserved1: [ptr::null_mut(); 58],
        };

        let lock_bs_fn: NvEncLockBitstreamFn =
            unsafe { std::mem::transmute(self.api_table.nvEncLockBitstream) };
        let ret = unsafe { lock_bs_fn(self.encoder, &mut lock_bitstream_params) };
        if ret != NV_ENC_SUCCESS {
            return Err(VideoCodecError::EncodeFailed(format!(
                "NvEncLockBitstream failed: error code {}",
                ret
            )));
        }

        // 读取编码数据
        let bs_size = lock_bitstream_params.bitstreamSizeInBytes as usize;
        let bs_ptr = lock_bitstream_params.bitstreamBufferPtr as *const u8;
        let data = if bs_size > 0 && !bs_ptr.is_null() {
            let slice = unsafe { std::slice::from_raw_parts(bs_ptr, bs_size) };
            slice.to_vec()
        } else {
            Vec::new()
        };

        // 解锁 bitstream
        let unlock_bs_fn: NvEncUnlockBitstreamFn =
            unsafe { std::mem::transmute(self.api_table.nvEncUnlockBitstream) };
        let _ = unsafe { unlock_bs_fn(self.encoder, self.output_buffer) };

        if data.is_empty() {
            return Err(VideoCodecError::EncodeFailed(
                "empty bitstream output".into(),
            ));
        }

        self.frame_count += 1;

        // 从 NVENC 输出获取实际 frame type
        // NV_ENC_PIC_TYPE: 1=IDR (keyframe), 2=I (keyframe), 3=P, 4=B, 5=BI
        let is_keyframe = matches!(lock_bitstream_params.pictureType, 1 | 2 | 5);

        // 对于 H.264/H.265, 也可以从 NALU header 检测 keyframe 作为 fallback
        let keyframe = is_keyframe || self.detect_keyframe_from_bitstream(&data);

        Ok(EncodedFrame {
            data: bytes::Bytes::from(data),
            width: self.config.width,
            height: self.config.height,
            keyframe,
            timestamp: frame.timestamp,
            bit_depth: 8,
        })
    }

    fn request_keyframe(&mut self) {
        self.force_keyframe = true;
    }

    fn codec(&self) -> lm_core::CodecType {
        if self.is_h264 {
            lm_core::CodecType::H264
        } else {
            lm_core::CodecType::H265
        }
    }

    fn set_bitrate(&mut self, bps: u32) {
        self.config.bitrate = bps;
    }

    fn set_framerate(&mut self, fps: u32) {
        self.config.framerate = fps;
    }
}
