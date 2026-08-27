//! 视频转码管道 — 解码 → YUV → 编码
//!
//! 提供端到端的视频转码能力:
//!   VP8 → decode → YUV420p → encode → H.264
//!   H.264 → decode → YUV420p → encode → VP8
//!   任意组合的编解码器互转
//!
//! 使用 YuvFramePool 减少帧分配, 使用硬件加速编码器 (VideoToolbox/NVENC) 时自动回退到软件。

use crate::{
    create_decoder, create_encoder_auto, hardware_encoder_available, EncodedFrame, EncoderBackend,
    EncoderConfig, VideoCodecError, VideoDecoder, VideoEncoder, YuvFrame, YuvFramePool,
};

/// 转码方向
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct TranscodePath {
    pub from: lm_core::CodecType,
    pub to: lm_core::CodecType,
}

/// 转码器 — 将一种编码格式转换为另一种
///
/// 内部维护解码器和编码器实例, 使用 YuvFramePool 减少帧分配。
/// 支持运行时切换目标编码器 (如硬件 → 软件回退)。
pub struct Transcoder {
    decoder: Box<dyn VideoDecoder>,
    encoder: Box<dyn VideoEncoder>,
    pool: YuvFramePool,
    path: TranscodePath,
    frame_count: u64,
    prefer_hardware: bool,
    width: u32,
    height: u32,
}

impl Transcoder {
    /// 创建转码器
    ///
    /// # 参数
    /// - `from`: 源编码格式
    /// - `to`: 目标编码格式
    /// - `width`: 视频宽度
    /// - `height`: 视频高度
    /// - `prefer_hardware`: 优先使用硬件编码器
    pub fn new(
        from: lm_core::CodecType,
        to: lm_core::CodecType,
        width: u32,
        height: u32,
        prefer_hardware: bool,
    ) -> Result<Self, VideoCodecError> {
        let decoder = create_decoder(from)?;
        let encoder = create_encoder_auto(to, width, height, prefer_hardware)?;

        Ok(Self {
            decoder,
            encoder,
            pool: YuvFramePool::new(8),
            path: TranscodePath { from, to },
            frame_count: 0,
            prefer_hardware,
            width,
            height,
        })
    }

    /// 转码一帧
    ///
    /// 输入编码帧 (如 VP8), 输出目标编码帧 (如 H.264)。
    /// 内部使用 pool 减少分配。
    pub fn transcode(
        &mut self,
        encoded_input: &[u8],
        timestamp: u64,
    ) -> Result<EncodedFrame, VideoCodecError> {
        // 1. 解码 (使用 pool 直接写入)
        let yuv = self
            .decoder
            .decode_with_pool(encoded_input, timestamp, &self.pool)?;

        // 2. 编码
        let encoded = self.encoder.encode(&yuv)?;

        // 3. 回收帧缓冲区到 pool
        let mut yuv = yuv;
        yuv.recycle_buffers(&self.pool);

        self.frame_count += 1;
        Ok(encoded)
    }

    /// 请求下一个帧为关键帧
    pub fn request_keyframe(&mut self) {
        self.encoder.request_keyframe();
    }

    /// 设置输出比特率
    pub fn set_bitrate(&mut self, bps: u32) {
        self.encoder.set_bitrate(bps);
    }

    /// 设置输出帧率
    pub fn set_framerate(&mut self, fps: u32) {
        self.encoder.set_framerate(fps);
    }

    /// 返回转码路径
    pub fn path(&self) -> TranscodePath {
        self.path
    }

    /// 返回已转码帧数
    pub fn frame_count(&self) -> u64 {
        self.frame_count
    }

    /// 返回当前使用的硬件后端 (如果有)
    pub fn hardware_backend(&self) -> Option<EncoderBackend> {
        if self.prefer_hardware {
            hardware_encoder_available(self.path.to)
        } else {
            None
        }
    }

    /// 尝试切换到硬件编码器 (如果可用)
    pub fn try_switch_to_hardware(&mut self) -> Result<bool, VideoCodecError> {
        if !self.prefer_hardware {
            return Ok(false);
        }
        if hardware_encoder_available(self.path.to).is_none() {
            return Ok(false);
        }

        match create_encoder_auto(self.path.to, self.width, self.height, true) {
            Ok(new_encoder) => {
                self.encoder = new_encoder;
                Ok(true)
            }
            Err(_) => Ok(false),
        }
    }

    /// 清空帧缓冲池
    pub fn clear_pool(&self) {
        self.pool.clear();
    }

    /// 返回池中缓存数量
    pub fn pooled_count(&self) -> usize {
        self.pool.pooled_count()
    }
}

/// 批量转码器 — 管理多个转码路径
///
/// 适用于一个输入流需要同时输出多种编码格式的场景:
///   输入 VP8 → 同时输出 H.264 + H.265 + AV1
pub struct BatchTranscoder {
    decoder: Box<dyn VideoDecoder>,
    pool: YuvFramePool,
    encoders: Vec<(lm_core::CodecType, Box<dyn VideoEncoder>)>,
    from: lm_core::CodecType,
    frame_count: u64,
}

impl BatchTranscoder {
    /// 创建批量转码器
    ///
    /// # 参数
    /// - `from`: 源编码格式
    /// - `to`: 目标编码格式列表
    /// - `width`: 视频宽度
    /// - `height`: 视频高度
    pub fn new(
        from: lm_core::CodecType,
        to: &[lm_core::CodecType],
        width: u32,
        height: u32,
    ) -> Result<Self, VideoCodecError> {
        let decoder = create_decoder(from)?;
        let mut encoders = Vec::with_capacity(to.len());
        for &codec in to {
            let encoder = create_encoder_auto(codec, width, height, true)?;
            encoders.push((codec, encoder));
        }

        Ok(Self {
            decoder,
            pool: YuvFramePool::new(8),
            encoders,
            from,
            frame_count: 0,
        })
    }

    /// 转码一帧到所有目标编码格式
    ///
    /// 返回每个目标编码器的输出帧
    pub fn transcode(
        &mut self,
        encoded_input: &[u8],
        timestamp: u64,
    ) -> Result<Vec<(lm_core::CodecType, EncodedFrame)>, VideoCodecError> {
        // 1. 解码一次 (使用 pool)
        let yuv = self
            .decoder
            .decode_with_pool(encoded_input, timestamp, &self.pool)?;

        // 2. 用每个编码器编码
        let mut results = Vec::with_capacity(self.encoders.len());
        for (codec, encoder) in &mut self.encoders {
            let encoded = encoder.encode(&yuv)?;
            results.push((*codec, encoded));
        }

        // 3. 回收
        let mut yuv = yuv;
        yuv.recycle_buffers(&self.pool);

        self.frame_count += 1;
        Ok(results)
    }

    /// 请求所有编码器的下一个帧为关键帧
    pub fn request_keyframe(&mut self) {
        for (_, encoder) in &mut self.encoders {
            encoder.request_keyframe();
        }
    }

    /// 返回源编码格式
    pub fn source_codec(&self) -> lm_core::CodecType {
        self.from
    }

    /// 返回目标编码格式列表
    pub fn target_codecs(&self) -> Vec<lm_core::CodecType> {
        self.encoders.iter().map(|(c, _)| *c).collect()
    }

    /// 返回已转码帧数
    pub fn frame_count(&self) -> u64 {
        self.frame_count
    }
}

/// 转码会话 — 带状态的转码会话管理
///
/// 管理转码器的生命周期, 包括:
/// - 动态分辨率变化 (重新创建编码器)
/// - 码率自适应
/// - 关键帧请求
pub struct TranscodeSession {
    transcoder: Transcoder,
    session_id: String,
    target_bitrate: u32,
    target_framerate: u32,
    adaptive_bitrate: bool,
    last_keyframe_request: u64,
}

impl TranscodeSession {
    pub fn new(
        session_id: String,
        from: lm_core::CodecType,
        to: lm_core::CodecType,
        width: u32,
        height: u32,
        prefer_hardware: bool,
    ) -> Result<Self, VideoCodecError> {
        let transcoder = Transcoder::new(from, to, width, height, prefer_hardware)?;
        Ok(Self {
            transcoder,
            session_id,
            target_bitrate: 500_000,
            target_framerate: 30,
            adaptive_bitrate: false,
            last_keyframe_request: 0,
        })
    }

    pub fn transcode(
        &mut self,
        input: &[u8],
        timestamp: u64,
    ) -> Result<EncodedFrame, VideoCodecError> {
        // 自适应码率: 每 100 帧检查一次
        if self.adaptive_bitrate && self.transcoder.frame_count() % 100 == 0 {
            self.transcoder.set_bitrate(self.target_bitrate);
        }

        self.transcoder.transcode(input, timestamp)
    }

    pub fn request_keyframe(&mut self, timestamp: u64) {
        if timestamp - self.last_keyframe_request > 30 {
            self.transcoder.request_keyframe();
            self.last_keyframe_request = timestamp;
        }
    }

    pub fn set_bitrate(&mut self, bps: u32) {
        self.target_bitrate = bps;
        self.transcoder.set_bitrate(bps);
    }

    pub fn set_framerate(&mut self, fps: u32) {
        self.target_framerate = fps;
        self.transcoder.set_framerate(fps);
    }

    pub fn enable_adaptive_bitrate(&mut self, enabled: bool) {
        self.adaptive_bitrate = enabled;
    }

    pub fn session_id(&self) -> &str {
        &self.session_id
    }

    pub fn frame_count(&self) -> u64 {
        self.transcoder.frame_count()
    }

    pub fn path(&self) -> TranscodePath {
        self.transcoder.path()
    }

    /// 返回当前是否使用硬件编码器
    pub fn hardware_used(&self) -> bool {
        self.transcoder.hardware_backend().is_some()
    }

    /// 返回内部转码器引用（用于直接访问）
    pub fn transcoder(&self) -> &Transcoder {
        &self.transcoder
    }

    /// 返回内部转码器可变引用
    pub fn transcoder_mut(&mut self) -> &mut Transcoder {
        &mut self.transcoder
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_transcode_path_display() {
        let path = TranscodePath {
            from: lm_core::CodecType::Vp8,
            to: lm_core::CodecType::H264,
        };
        assert_eq!(path.from, lm_core::CodecType::Vp8);
        assert_eq!(path.to, lm_core::CodecType::H264);
    }

    #[test]
    fn test_transcoder_creation_vp8_to_h264() {
        // 仅在 vpx + openh264/videotoolbox 可用时测试
        #[cfg(all(
            feature = "vpx",
            any(
                feature = "openh264",
                all(feature = "videotoolbox", target_os = "macos")
            )
        ))]
        {
            let result = Transcoder::new(
                lm_core::CodecType::Vp8,
                lm_core::CodecType::H264,
                320,
                240,
                false,
            );
            assert!(result.is_ok(), "transcoder creation should succeed");
            let tc = result.unwrap();
            assert_eq!(tc.path().from, lm_core::CodecType::Vp8);
            assert_eq!(tc.path().to, lm_core::CodecType::H264);
            assert_eq!(tc.frame_count(), 0);
        }
    }

    #[test]
    fn test_transcoder_creation_unsupported() {
        // 不支持的 codec 应返回错误
        let result = Transcoder::new(
            lm_core::CodecType::Opus,
            lm_core::CodecType::H264,
            320,
            240,
            false,
        );
        // Opus 是音频，video-codec 不支持
        assert!(result.is_err());
    }

    #[test]
    fn test_batch_transcoder_creation() {
        #[cfg(all(
            feature = "vpx",
            any(
                feature = "openh264",
                all(feature = "videotoolbox", target_os = "macos")
            )
        ))]
        {
            let result = BatchTranscoder::new(
                lm_core::CodecType::Vp8,
                &[lm_core::CodecType::H264, lm_core::CodecType::Vp9],
                320,
                240,
            );
            assert!(result.is_ok());
            let bt = result.unwrap();
            assert_eq!(bt.source_codec(), lm_core::CodecType::Vp8);
            assert_eq!(bt.target_codecs().len(), 2);
        }
    }

    #[test]
    fn test_transcode_session_creation() {
        #[cfg(all(
            feature = "vpx",
            any(
                feature = "openh264",
                all(feature = "videotoolbox", target_os = "macos")
            )
        ))]
        {
            let result = TranscodeSession::new(
                "test-session".to_string(),
                lm_core::CodecType::Vp8,
                lm_core::CodecType::H264,
                320,
                240,
                false,
            );
            assert!(result.is_ok());
            let mut session = result.unwrap();
            assert_eq!(session.session_id(), "test-session");
            assert_eq!(session.frame_count(), 0);
            assert!(!session.hardware_used());

            // 设置码率/帧率
            session.set_bitrate(1_000_000);
            session.set_framerate(30);
        }
    }
}
