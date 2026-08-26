//! lm-recorder — 录制器
//!
//! 支持 WAV 格式录制（PCM i16），参考 RustPBX wav_writer.rs。
//! 后续可扩展 Opus/PCAP 格式。
//!
//! Phase 3 新增：
//! - RecordingState 录制状态机
//! - SegmentRecorder 分段录制器（按关键帧边界 + 时长切分）
//! - MP4 合并（调用 ffmpeg）

use anyhow::Result;
use lm_core::{AudioFrame, SessionId};
use std::path::{Path, PathBuf};
use tokio::fs::File;
use tokio::io::{AsyncSeekExt, AsyncWriteExt, SeekFrom};
use tracing::{info, warn};

/// 录制格式
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RecordingFormat {
    Wav,
    Opus,
    Pcap,
}

/// 录制结果
#[derive(Debug, Clone)]
pub struct RecordingResult {
    pub file_path: String,
    pub duration_ms: u64,
    pub file_size: u64,
}

/// WAV 录制器（参考 RustPBX CodecWavWriter）
///
/// 写入 PCM i16 样本到 WAV 文件。
/// 构造时写 WAV header（data_size=0），停止时回写正确的 data_size。
pub struct WavRecorder {
    file: File,
    sample_rate: u32,
    channels: u16,
    written_bytes: u32,
    path: String,
    start_time: std::time::Instant,
}

impl WavRecorder {
    /// 创建 WAV 文件并写入 header
    pub async fn create(path: &str, sample_rate: u32, channels: u16) -> Result<Self> {
        // 创建父目录
        if let Some(parent) = Path::new(path).parent() {
            if !parent.as_os_str().is_empty() {
                tokio::fs::create_dir_all(parent).await?;
            }
        }

        let file = File::create(path)
            .await
            .map_err(|e| anyhow::anyhow!("create wav file {path}: {e}"))?;

        let mut recorder = Self {
            file,
            sample_rate,
            channels,
            written_bytes: 0,
            path: path.to_string(),
            start_time: std::time::Instant::now(),
        };

        recorder.write_header().await?;
        recorder.file.flush().await?;
        Ok(recorder)
    }

    /// 写入 PCM 帧
    pub async fn write_frame(&mut self, frame: &AudioFrame) -> Result<()> {
        // i16 → little-endian bytes
        let bytes = samples_to_le_bytes(&frame.samples);
        self.file.write_all(&bytes).await?;
        self.written_bytes += bytes.len() as u32;
        Ok(())
    }

    /// 写入原始 PCM 字节（已是 little-endian i16）
    pub async fn write_pcm_bytes(&mut self, data: &[u8]) -> Result<()> {
        self.file.write_all(data).await?;
        self.written_bytes += data.len() as u32;
        Ok(())
    }

    /// 完成录制（回写 header 中的 data_size）
    pub async fn finalize(mut self) -> Result<RecordingResult> {
        // 回到文件开头重写 header
        self.file.seek(SeekFrom::Start(0)).await?;
        self.write_header().await?;
        self.file.flush().await?;

        let duration_ms = self.start_time.elapsed().as_millis() as u64;
        let file_size = 44 + self.written_bytes as u64; // WAV header = 44 bytes

        Ok(RecordingResult {
            file_path: self.path,
            duration_ms,
            file_size,
        })
    }

    async fn write_header(&mut self) -> Result<()> {
        let header = wav_header_pcm16(self.sample_rate, self.channels, self.written_bytes);
        self.file.write_all(&header).await?;
        Ok(())
    }
}

/// 生成 PCM 16-bit WAV header（44 bytes）
///
/// 参考 RustPBX rustpbx_record_common::wav_header，简化为 PCM only。
fn wav_header_pcm16(sample_rate: u32, channels: u16, data_size: u32) -> [u8; 44] {
    let byte_rate = sample_rate * channels as u32 * 2; // 16-bit = 2 bytes
    let block_align = channels * 2;
    let chunk_size = 36 + data_size; // RIFF chunk size

    let mut h = [0u8; 44];

    // RIFF header
    h[0..4].copy_from_slice(b"RIFF");
    h[4..8].copy_from_slice(&chunk_size.to_le_bytes());
    h[8..12].copy_from_slice(b"WAVE");

    // fmt chunk
    h[12..16].copy_from_slice(b"fmt ");
    h[16..20].copy_from_slice(&16u32.to_le_bytes()); // subchunk1 size = 16
    h[20..22].copy_from_slice(&1u16.to_le_bytes()); // audio format = 1 (PCM)
    h[22..24].copy_from_slice(&channels.to_le_bytes());
    h[24..28].copy_from_slice(&sample_rate.to_le_bytes());
    h[28..32].copy_from_slice(&byte_rate.to_le_bytes());
    h[32..34].copy_from_slice(&block_align.to_le_bytes());
    h[34..36].copy_from_slice(&16u16.to_le_bytes()); // bits per sample = 16

    // data chunk
    h[36..40].copy_from_slice(b"data");
    h[40..44].copy_from_slice(&data_size.to_le_bytes());

    h
}

/// i16 样本转 little-endian bytes
fn samples_to_le_bytes(samples: &[i16]) -> Vec<u8> {
    let mut bytes = Vec::with_capacity(samples.len() * 2);
    for &s in samples {
        bytes.extend_from_slice(&s.to_le_bytes());
    }
    bytes
}

// ============================================================================
// 通用录制器（支持多格式）
// ============================================================================

/// 录制器（支持 WAV 格式，后续扩展 Opus/PCAP）
pub enum Recorder {
    Wav(WavRecorder),
}

impl Recorder {
    /// 创建录制器
    pub async fn new(
        _session_id: SessionId,
        format: RecordingFormat,
        path: String,
        sample_rate: u32,
        channels: u16,
    ) -> Result<Self> {
        match format {
            RecordingFormat::Wav => {
                let w = WavRecorder::create(&path, sample_rate, channels).await?;
                Ok(Self::Wav(w))
            }
            RecordingFormat::Opus => {
                Err(anyhow::anyhow!("Opus recording not yet implemented"))
            }
            RecordingFormat::Pcap => {
                Err(anyhow::anyhow!("PCAP recording not yet implemented"))
            }
        }
    }

    /// 写入一帧音频
    pub async fn write_frame(&mut self, frame: &AudioFrame) -> Result<()> {
        match self {
            Self::Wav(w) => w.write_frame(frame).await,
        }
    }

    /// 停止录制并返回文件信息
    pub async fn finalize(self) -> Result<RecordingResult> {
        match self {
            Self::Wav(w) => w.finalize().await,
        }
    }
}

// ============================================================================
// Phase 3: 录制状态机
// ============================================================================

/// 录制状态
///
/// 状态转换：
/// ```text
/// Requested → Starting → Active → Stopping → Finalizing → Completed
///                    ↓                              ↓
///                Failed                        Failed
/// ```
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RecordingState {
    /// 收到录制请求
    Requested,
    /// 正在创建文件、初始化
    Starting,
    /// 正在录制
    Active,
    /// 收到停止请求
    Stopping,
    /// 正在合并分片、写 MP4
    Finalizing,
    /// 录制完成
    Completed,
    /// 录制失败
    Failed(String),
}

impl RecordingState {
    pub fn is_active(&self) -> bool {
        matches!(self, RecordingState::Active)
    }

    pub fn is_terminal(&self) -> bool {
        matches!(self, RecordingState::Completed | RecordingState::Failed(_))
    }
}

impl std::fmt::Display for RecordingState {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            RecordingState::Requested => write!(f, "requested"),
            RecordingState::Starting => write!(f, "starting"),
            RecordingState::Active => write!(f, "active"),
            RecordingState::Stopping => write!(f, "stopping"),
            RecordingState::Finalizing => write!(f, "finalizing"),
            RecordingState::Completed => write!(f, "completed"),
            RecordingState::Failed(msg) => write!(f, "failed: {msg}"),
        }
    }
}

// ============================================================================
// Phase 3: 分段录制器
// ============================================================================

/// 分段录制器
///
/// 参考 Xiu HLS 录制 + atm0s RecordChunkWriter。
///
/// 按关键帧边界 + 时长切分视频分段，停止时用 ffmpeg 合并为 MP4。
/// 类似腾讯会议的分片录制→合并 MP4 模式。
pub struct SegmentRecorder {
    /// 输出目录
    output_dir: PathBuf,
    /// 录制 ID
    recording_id: String,
    /// 分段时长（秒），0 = 不分段
    segment_duration: u64,
    /// 视频编解码
    video_codec: lm_core::CodecType,
    /// 当前分段索引
    current_segment_index: u32,
    /// 当前分段文件
    current_segment: Option<SegmentWriter>,
    /// 已完成的分段文件路径
    completed_segments: Vec<PathBuf>,
    /// 第一帧时间戳
    first_timestamp: Option<u32>,
    /// 当前分段开始时间戳
    segment_start_ts: Option<u32>,
    /// 是否已设置 IVF dimensions
    dimensions_set: bool,
    /// 视频宽度
    width: u16,
    /// 视频高度
    height: u16,
    /// 总帧数
    total_frames: u64,
}

/// 单个分段写入器
struct SegmentWriter {
    file: File,
    path: PathBuf,
    frame_count: u64,
}

impl SegmentRecorder {
    /// 创建分段录制器
    pub fn new(
        output_dir: impl Into<PathBuf>,
        recording_id: impl Into<String>,
        segment_duration: u64,
        video_codec: lm_core::CodecType,
    ) -> Self {
        Self {
            output_dir: output_dir.into(),
            recording_id: recording_id.into(),
            segment_duration,
            video_codec,
            current_segment_index: 0,
            current_segment: None,
            completed_segments: Vec::new(),
            first_timestamp: None,
            segment_start_ts: None,
            dimensions_set: false,
            width: 0,
            height: 0,
            total_frames: 0,
        }
    }

    /// 写入一帧视频
    pub async fn write_frame(&mut self, frame: &lm_core::MediaFrame) -> Result<()> {
        // 提取 dimensions
        if !self.dimensions_set && frame.keyframe {
            if let Some((w, h)) = lm_depacketizer::vp8_extract_dimensions(&frame.data) {
                self.width = w;
                self.height = h;
                self.dimensions_set = true;
                info!(
                    recording_id = %self.recording_id,
                    width = w, height = h,
                    "segment recorder: extracted dimensions"
                );
            }
        }

        // 记录第一帧时间戳
        if self.first_timestamp.is_none() {
            self.first_timestamp = Some(frame.timestamp);
        }

        // 检查是否需要切分分段
        let should_split = self.should_split_segment(frame);
        if should_split {
            self.close_current_segment().await?;
            self.current_segment_index += 1;
        }

        // 创建新分段（如果需要）
        if self.current_segment.is_none() {
            self.open_new_segment().await?;
        }

        // 写入帧
        if let Some(ref mut seg) = self.current_segment {
            match self.video_codec {
                lm_core::CodecType::Vp8 | lm_core::CodecType::Vp9 => {
                    // IVF 帧头 + 数据
                    let ts_base = self.first_timestamp.unwrap_or(0);
                    let rel_ts = frame.timestamp.wrapping_sub(ts_base) as u64;
                    let ivf_frame = ivf_frame_header(frame.data.len() as u32, rel_ts);
                    seg.file.write_all(&ivf_frame).await?;
                    seg.file.write_all(&frame.data).await?;
                }
                lm_core::CodecType::H264 => {
                    // Annex-B 直接写入
                    seg.file.write_all(&frame.data).await?;
                }
                _ => {}
            }
            seg.frame_count += 1;
            self.total_frames += 1;
        }

        Ok(())
    }

    /// 判断是否应该切分分段
    fn should_split_segment(&self, frame: &lm_core::MediaFrame) -> bool {
        if self.current_segment.is_none() {
            return false;
        }

        // 不分段模式
        if self.segment_duration == 0 {
            return false;
        }

        // 只在关键帧时切分
        if !frame.keyframe {
            return false;
        }

        // 检查时长
        if let (Some(start_ts), Some(first_ts)) = (self.segment_start_ts, self.first_timestamp) {
            let clock_rate = 90000u32; // 视频时钟率
            let elapsed = frame.timestamp.wrapping_sub(start_ts) as f64 / clock_rate as f64;
            if elapsed >= self.segment_duration as f64 {
                return true;
            }
        }

        false
    }

    /// 开启新分段
    async fn open_new_segment(&mut self) -> Result<()> {
        let ext = match self.video_codec {
            lm_core::CodecType::Vp8 | lm_core::CodecType::Vp9 => "ivf",
            lm_core::CodecType::H264 => "h264",
            _ => "raw",
        };
        let path = self.output_dir.join(format!(
            "{}_seg{:04}.{}",
            self.recording_id, self.current_segment_index, ext
        ));

        let mut file = File::create(&path).await?;

        // 写入 IVF header（VP8/VP9）
        if self.video_codec == lm_core::CodecType::Vp8 || self.video_codec == lm_core::CodecType::Vp9 {
            let fourcc: [u8; 4] = if self.video_codec == lm_core::CodecType::Vp8 {
                *b"VP80"
            } else {
                *b"VP90"
            };
            let header = ivf_header(fourcc, self.width, self.height);
            file.write_all(&header).await?;
        }

        info!(
            recording_id = %self.recording_id,
            segment = self.current_segment_index,
            path = ?path,
            "segment recorder: opened new segment"
        );

        self.current_segment = Some(SegmentWriter {
            file,
            path: path.clone(),
            frame_count: 0,
        });

        Ok(())
    }

    /// 关闭当前分段
    async fn close_current_segment(&mut self) -> Result<()> {
        if let Some(mut seg) = self.current_segment.take() {
            seg.file.flush().await?;
            seg.file.shutdown().await?;
            info!(
                recording_id = %self.recording_id,
                segment = self.current_segment_index,
                frames = seg.frame_count,
                "segment recorder: closed segment"
            );
            self.completed_segments.push(seg.path);
        }
        Ok(())
    }

    /// 完成录制：关闭当前分段，合并为 MP4
    ///
    /// 使用 ffmpeg 合并分段：
    /// ```bash
    /// ffmpeg -i seg0.ivf -i seg1.ivf ... -c copy output.mp4
    /// ```
    /// 或对于 H264：
    /// ```bash
    /// ffmpeg -i seg0.h264 -i seg1.h264 ... -c copy output.mp4
    /// ```
    pub async fn finalize(&mut self) -> Result<PathBuf> {
        // 关闭当前分段
        self.close_current_segment().await?;

        if self.completed_segments.is_empty() {
            return Err(anyhow::anyhow!("no segments to finalize"));
        }

        // 如果只有一个分段，直接重命名为 MP4（或用 ffmpeg 转封装）
        let output_path = self.output_dir.join(format!("{}.mp4", self.recording_id));

        if self.completed_segments.len() == 1 {
            // 单分段：用 ffmpeg 转封装为 MP4
            let seg = &self.completed_segments[0];
            self.remux_to_mp4(seg, &output_path).await?;
        } else {
            // 多分段：先合并再转封装
            let merged_path = self.output_dir.join(format!("{}_merged.{}", self.recording_id,
                match self.video_codec {
                    lm_core::CodecType::Vp8 | lm_core::CodecType::Vp9 => "ivf",
                    lm_core::CodecType::H264 => "h264",
                    _ => "raw",
                }));
            self.merge_segments(&merged_path).await?;
            self.remux_to_mp4(&merged_path, &output_path).await?;
            // 清理合并文件
            let _ = tokio::fs::remove_file(&merged_path).await;
        }

        // 清理分段文件
        for seg in &self.completed_segments {
            let _ = tokio::fs::remove_file(seg).await;
        }

        info!(
            recording_id = %self.recording_id,
            output = ?output_path,
            total_frames = self.total_frames,
            segments = self.completed_segments.len(),
            "segment recorder: finalized to MP4"
        );

        Ok(output_path)
    }

    /// 合并分段文件（二进制拼接）
    async fn merge_segments(&self, output: &Path) -> Result<()> {
        let mut out = File::create(output).await?;

        for (i, seg) in self.completed_segments.iter().enumerate() {
            if i == 0 {
                // 第一个分段：直接复制（包含 IVF header）
                let data = tokio::fs::read(seg).await?;
                out.write_all(&data).await?;
            } else {
                // 后续分段：跳过 IVF header（32 bytes），只复制帧数据
                let data = tokio::fs::read(seg).await?;
                if data.len() > 32 {
                    out.write_all(&data[32..]).await?;
                }
            }
        }

        out.flush().await?;
        out.shutdown().await?;
        Ok(())
    }

    /// 用 ffmpeg 转封装为 MP4
    async fn remux_to_mp4(&self, input: &Path, output: &Path) -> Result<()> {
        let input_str = input.to_str().ok_or_else(|| anyhow::anyhow!("invalid input path"))?;
        let output_str = output.to_str().ok_or_else(|| anyhow::anyhow!("invalid output path"))?;

        let result = tokio::process::Command::new("ffmpeg")
            .args(["-y", "-i", input_str, "-c", "copy", output_str])
            .output()
            .await;

        match result {
            Ok(cmd_out) => {
                if !cmd_out.status.success() {
                    let stderr = String::from_utf8_lossy(&cmd_out.stderr);
                    warn!(
                        recording_id = %self.recording_id,
                        stderr = %stderr,
                        "ffmpeg remux failed, keeping raw format"
                    );
                    // 失败时保留原始文件
                    tokio::fs::copy(input, output).await?;
                }
            }
            Err(e) => {
                warn!(
                    recording_id = %self.recording_id,
                    error = %e,
                    "ffmpeg not found, keeping raw format"
                );
                // ffmpeg 不存在，直接复制
                tokio::fs::copy(input, output).await?;
            }
        }

        Ok(())
    }

    /// 获取总帧数
    pub fn total_frames(&self) -> u64 {
        self.total_frames
    }

    /// 获取分段数
    pub fn segment_count(&self) -> usize {
        self.completed_segments.len() + if self.current_segment.is_some() { 1 } else { 0 }
    }
}

/// IVF 文件头 (32 bytes)
fn ivf_header(fourcc: [u8; 4], width: u16, height: u16) -> [u8; 32] {
    let mut h = [0u8; 32];
    h[0..4].copy_from_slice(b"DKIF");
    h[4..6].copy_from_slice(&0u16.to_le_bytes());
    h[6..8].copy_from_slice(&32u16.to_le_bytes());
    h[8..12].copy_from_slice(&fourcc);
    h[12..14].copy_from_slice(&width.to_le_bytes());
    h[14..16].copy_from_slice(&height.to_le_bytes());
    h[16..20].copy_from_slice(&90000u32.to_le_bytes());
    h[20..24].copy_from_slice(&1u32.to_le_bytes());
    h[24..28].copy_from_slice(&0u32.to_le_bytes());
    h[28..32].copy_from_slice(&0u32.to_le_bytes());
    h
}

/// IVF 帧头 (12 bytes)
fn ivf_frame_header(size: u32, timestamp: u64) -> [u8; 12] {
    let mut h = [0u8; 12];
    h[0..4].copy_from_slice(&size.to_le_bytes());
    h[4..12].copy_from_slice(&timestamp.to_le_bytes());
    h
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_wav_header_size() {
        let h = wav_header_pcm16(8000, 1, 1600);
        assert_eq!(h.len(), 44);
        assert_eq!(&h[0..4], b"RIFF");
        assert_eq!(&h[8..12], b"WAVE");
        assert_eq!(&h[12..16], b"fmt ");
        assert_eq!(&h[36..40], b"data");
    }

    #[test]
    fn test_samples_to_le_bytes() {
        let bytes = samples_to_le_bytes(&[0i16, 256, -1]);
        assert_eq!(bytes, vec![0, 0, 0, 1, 255, 255]);
    }

    #[tokio::test]
    async fn test_wav_recorder() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let mut rec = WavRecorder::create(path, 8000, 1).await.unwrap();
        rec.write_frame(&AudioFrame {
            samples: vec![100i16; 160],
            sample_rate: 8000,
            timestamp: 0,
        })
        .await
        .unwrap();
        let result = rec.finalize().await.unwrap();

        assert_eq!(result.file_path, path);
        assert!(result.file_size > 44);

        // 验证文件存在且有内容
        let metadata = tokio::fs::metadata(path).await.unwrap();
        assert!(metadata.len() > 44);
    }
}
