//! lm-recorder — 录制器
//!
//! 支持 WAV 格式录制（PCM i16），参考 RustPBX wav_writer.rs。
//! 后续可扩展 Opus/PCAP 格式。

use anyhow::Result;
use lm_core::{AudioFrame, SessionId};
use std::path::Path;
use tokio::fs::File;
use tokio::io::{AsyncSeekExt, AsyncWriteExt, SeekFrom};

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
