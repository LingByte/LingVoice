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
    reserved1: [*mut c_void; 58],
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
    _api_table: NvEncodeApiTable,
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
            _api_table: api_table,
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

impl VideoEncoder for NvencEncoder {
    fn encode(&mut self, frame: &YuvFrame) -> Result<EncodedFrame, VideoCodecError> {
        let _ = frame;
        Err(VideoCodecError::EncodeFailed(
            "NVENC encoder not fully initialized (framework only, GPU device required)".into(),
        ))
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
