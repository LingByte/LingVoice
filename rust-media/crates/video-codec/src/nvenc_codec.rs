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

#![allow(dead_code)]

use crate::{EncodedFrame, EncoderConfig, VideoCodecError, VideoEncoder, YuvFrame};
use std::ffi::c_void;
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

/// NVENC 硬件编码器
pub struct NvencEncoder {
    config: EncoderConfig,
    encoder: *mut c_void,
    api_table: *mut NvEncodeApiTable,
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
        let _ = is_h264;
        // NVENC SDK 需要通过 dlopen 加载 libnvidia-encode.so
        // 这里返回错误，因为完整的 NVENC 初始化需要动态加载
        // 在实际部署时需要链接 NVIDIA Encode SDK
        Err(VideoCodecError::NotInitialized)
    }

    /// 检测 NVENC 是否可用（NVIDIA GPU + 驱动）
    pub fn is_available() -> bool {
        // 尝试 dlopen libnvidia-encode.so
        // 在实际实现中应检查:
        // 1. /proc/driver/nvidia 存在
        // 2. libnvidia-encode.so 可加载
        // 3. NvEncodeAPIGetMaxSupportedVersion 成功
        false
    }
}

impl Drop for NvencEncoder {
    fn drop(&mut self) {
        if !self.encoder.is_null() {
            // nvEncTerminateEncoder + close
        }
    }
}

impl VideoEncoder for NvencEncoder {
    fn encode(&mut self, frame: &YuvFrame) -> Result<EncodedFrame, VideoCodecError> {
        let _ = frame;
        Err(VideoCodecError::EncodeFailed(
            "NVENC encoder not fully initialized".into(),
        ))
    }

    fn request_keyframe(&mut self) {
        self.force_keyframe = true;
    }

    fn codec(&self) -> lm_core::CodecType {
        lm_core::CodecType::H264
    }

    fn set_bitrate(&mut self, bps: u32) {
        self.config.bitrate = bps;
    }

    fn set_framerate(&mut self, fps: u32) {
        self.config.framerate = fps;
    }
}
