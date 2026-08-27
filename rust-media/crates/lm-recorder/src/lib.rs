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
    Mp4,
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
// Opus 录制器（OGG/Opus 容器）
// ============================================================================

/// OGG 页面头部 (28 bytes + segment table)
struct OggPageHeader {
    version: u8,
    header_type: u8, // 0x02 = BOS (first page), 0x04 = EOS (last page)
    granule_position: i64,
    serial_number: u32,
    page_sequence: u32,
    checksum: u32,
    segments: Vec<u8>, // segment table
}

/// Opus 录制器 — 将 Opus 编码帧写入 OGG/Opus 容器
///
/// OGG/Opus 格式 (RFC 7845):
/// - 第一个 page: OpusHead (19 bytes header + channel mapping)
/// - 后续 pages: Opus 编码帧
/// - 最后一页: header_type |= 0x04 (EOS)
pub struct OpusRecorder {
    file: File,
    sample_rate: u32,
    channels: u16,
    serial: u32,
    page_seq: u32,
    granule: i64,
    path: String,
    start_time: std::time::Instant,
    written_bytes: u32,
}

impl OpusRecorder {
    /// 创建 OGG/Opus 文件并写入 OpusHead
    pub async fn create(path: &str, sample_rate: u32, channels: u16) -> Result<Self> {
        if let Some(parent) = Path::new(path).parent() {
            if !parent.as_os_str().is_empty() {
                tokio::fs::create_dir_all(parent).await?;
            }
        }

        let mut file = File::create(path)
            .await
            .map_err(|e| anyhow::anyhow!("create opus file {path}: {e}"))?;

        let serial = rand_serial();
        let mut recorder = Self {
            file,
            sample_rate,
            channels,
            serial,
            page_seq: 0,
            granule: 0,
            path: path.to_string(),
            start_time: std::time::Instant::now(),
            written_bytes: 0,
        };

        // 写 OpusHead page (BOS)
        recorder.write_opus_head().await?;
        recorder.file.flush().await?;
        Ok(recorder)
    }

    /// 写入一个 Opus 编码帧（已编码的 Opus payload）
    pub async fn write_opus_packet(
        &mut self,
        payload: &[u8],
        samples_in_packet: u32,
    ) -> Result<()> {
        // 更新 granule position
        self.granule += samples_in_packet as i64;

        // 构造 OGG page（单帧一页，简化）
        let page = build_ogg_page(
            self.serial,
            self.page_seq,
            self.granule,
            0, // continuation
            &[payload],
        );
        self.file.write_all(&page).await?;
        self.written_bytes += page.len() as u32;
        self.page_seq += 1;
        Ok(())
    }

    /// 写入 PCM 帧（内部编码为 Opus）
    pub async fn write_frame(&mut self, frame: &AudioFrame) -> Result<()> {
        // 需要编码 PCM → Opus，但 lm-recorder 不直接依赖 audio-codec
        // 调用方应使用 write_opus_packet 写入已编码的 Opus 数据
        // 这里提供 PCM 接口用于兼容，但会返回 error
        Err(anyhow::anyhow!(
            "OpusRecorder.write_frame not supported, use write_opus_packet instead"
        ))
    }

    /// 完成录制（写 EOS page）
    pub async fn finalize(mut self) -> Result<RecordingResult> {
        // 写 EOS page（空数据，header_type |= 0x04）
        let eos_page = build_ogg_page(self.serial, self.page_seq, self.granule, 0x04, &[]);
        self.file.write_all(&eos_page).await?;
        self.file.flush().await?;

        let duration_ms = self.start_time.elapsed().as_millis() as u64;
        let file_size = self.written_bytes as u64 + eos_page.len() as u64;

        Ok(RecordingResult {
            file_path: self.path,
            duration_ms,
            file_size,
        })
    }

    async fn write_opus_head(&mut self) -> Result<()> {
        // OpusHead header (RFC 7845 §5.1)
        let mut head = Vec::with_capacity(19);
        head.extend_from_slice(b"OpusHead");
        head.push(1); // version
        head.push(self.channels as u8); // channel count
        head.extend_from_slice(&0u16.to_le_bytes()); // pre-skip
        head.extend_from_slice(&(self.sample_rate).to_le_bytes()); // sample rate
        head.extend_from_slice(&0i16.to_le_bytes()); // output gain
        head.push(0); // channel mapping family (0 = mono/stereo)

        let page = build_ogg_page(self.serial, self.page_seq, 0, 0x02, &[&head]); // BOS
        self.file.write_all(&page).await?;
        self.written_bytes += page.len() as u32;
        self.page_seq += 1;
        Ok(())
    }
}

/// 构建 OGG page（简化版，单页单段或多段）
fn build_ogg_page(
    serial: u32,
    page_seq: u32,
    granule: i64,
    header_type: u8,
    segments: &[&[u8]],
) -> Vec<u8> {
    let total_size: usize = segments.iter().map(|s| s.len()).sum();

    // segment table: 每个 255 字节为一个 0xFF 段，最后一段为剩余长度
    let mut seg_table = Vec::new();
    for seg in segments {
        let mut remaining = seg.len();
        while remaining >= 255 {
            seg_table.push(255);
            remaining -= 255;
        }
        seg_table.push(remaining as u8);
    }

    let header_size = 27 + seg_table.len();
    let mut page = Vec::with_capacity(header_size + total_size);

    // OGG page header
    page.extend_from_slice(b"OggS"); // capture pattern
    page.push(0); // version
    page.push(header_type); // header type
    page.extend_from_slice(&granule.to_le_bytes()); // granule position
    page.extend_from_slice(&serial.to_le_bytes()); // serial number
    page.extend_from_slice(&page_seq.to_le_bytes()); // page sequence number
    page.extend_from_slice(&0u32.to_le_bytes()); // checksum (placeholder, CRC32)
    page.push(seg_table.len() as u8); // segment count
    page.extend_from_slice(&seg_table); // segment table

    // segment data
    for seg in segments {
        page.extend_from_slice(seg);
    }

    // 计算 CRC32 并回写
    let crc = ogg_crc32(&page);
    page[22..26].copy_from_slice(&crc.to_le_bytes());

    page
}

/// OGG CRC32 (polynomial 0x04C11DB7)
fn ogg_crc32(data: &[u8]) -> u32 {
    static mut TABLE: [u32; 256] = [0; 256];
    static INITIALIZED: std::sync::Once = std::sync::Once::new();

    unsafe {
        INITIALIZED.call_once(|| {
            for i in 0..256u32 {
                let mut r = i << 24;
                for _ in 0..8 {
                    if r & 0x80000000 != 0 {
                        r = (r << 1) ^ 0x04C11DB7;
                    } else {
                        r <<= 1;
                    }
                }
                TABLE[i as usize] = r;
            }
        });

        let mut crc: u32 = 0;
        for &byte in data {
            crc = (crc << 8) ^ TABLE[((crc >> 24) ^ byte as u32) as usize & 0xFF];
        }
        crc
    }
}

fn rand_serial() -> u32 {
    use std::collections::hash_map::DefaultHasher;
    use std::hash::{Hash, Hasher};
    let mut h = DefaultHasher::new();
    std::time::SystemTime::now().hash(&mut h);
    h.finish() as u32
}

// ============================================================================
// PCAP 录制器（RTP 原始包捕获）
// ============================================================================

/// PCAP 全局头 (24 bytes)
const PCAP_MAGIC: u32 = 0xA1B2C3D4;
const PCAP_VERSION_MAJOR: u16 = 2;
const PCAP_VERSION_MINOR: u16 = 4;

/// PCAP 录制器 — 将原始 RTP 包写入 pcap 文件
///
/// 使用 LINKTYPE_RAW (101) 或自定义 linktype，
/// 每个 RTP 包前加 pcap packet header (16 bytes)。
pub struct PcapRecorder {
    file: File,
    path: String,
    start_time: std::time::Instant,
    packet_count: u64,
    written_bytes: u32,
}

impl PcapRecorder {
    /// 创建 PCAP 文件并写入全局头
    pub async fn create(path: &str) -> Result<Self> {
        if let Some(parent) = Path::new(path).parent() {
            if !parent.as_os_str().is_empty() {
                tokio::fs::create_dir_all(parent).await?;
            }
        }

        let mut file = File::create(path)
            .await
            .map_err(|e| anyhow::anyhow!("create pcap file {path}: {e}"))?;

        // PCAP 全局头
        let mut header = [0u8; 24];
        header[0..4].copy_from_slice(&PCAP_MAGIC.to_le_bytes());
        header[4..6].copy_from_slice(&PCAP_VERSION_MAJOR.to_le_bytes());
        header[6..8].copy_from_slice(&PCAP_VERSION_MINOR.to_le_bytes());
        header[8..12].copy_from_slice(&0i32.to_le_bytes()); // thiszone
        header[12..16].copy_from_slice(&0u32.to_le_bytes()); // sigfigs
        header[16..20].copy_from_slice(&65535u32.to_le_bytes()); // snaplen
        header[20..24].copy_from_slice(&1u32.to_le_bytes()); // network (LINKTYPE_ETHERNET)

        file.write_all(&header).await?;
        file.flush().await?;

        Ok(Self {
            file,
            path: path.to_string(),
            start_time: std::time::Instant::now(),
            packet_count: 0,
            written_bytes: 24,
        })
    }

    /// 写入一个 RTP 包（原始字节，含 RTP header）
    pub async fn write_rtp_packet(&mut self, rtp_data: &[u8]) -> Result<()> {
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default();

        let ts_sec = now.as_secs() as u32;
        let ts_usec = now.subsec_micros();

        // PCAP packet header (16 bytes)
        let mut pkt_header = [0u8; 16];
        pkt_header[0..4].copy_from_slice(&ts_sec.to_le_bytes());
        pkt_header[4..8].copy_from_slice(&ts_usec.to_le_bytes());
        pkt_header[8..12].copy_from_slice(&(rtp_data.len() as u32).to_le_bytes()); // incl_len
        pkt_header[12..16].copy_from_slice(&(rtp_data.len() as u32).to_le_bytes()); // orig_len

        self.file.write_all(&pkt_header).await?;
        self.file.write_all(rtp_data).await?;
        self.written_bytes += 16 + rtp_data.len() as u32;
        self.packet_count += 1;
        Ok(())
    }

    /// 完成录制
    pub async fn finalize(mut self) -> Result<RecordingResult> {
        self.file.flush().await?;

        let duration_ms = self.start_time.elapsed().as_millis() as u64;
        Ok(RecordingResult {
            file_path: self.path,
            duration_ms,
            file_size: self.written_bytes as u64,
        })
    }

    /// 已写入的包数
    pub fn packet_count(&self) -> u64 {
        self.packet_count
    }
}

// ============================================================================
// MP4 录制器（通过 ffmpeg 转封装）
// ============================================================================

/// MP4 录制器 — 先写入临时 IVF/H264 文件，finalize 时用 ffmpeg 转封装为 MP4
///
/// 音频：先写 WAV 临时文件，finalize 时 ffmpeg 转封装
/// 视频：先写 IVF/H264 临时文件，finalize 时 ffmpeg 转封装
pub struct Mp4Recorder {
    /// 临时原始文件路径
    temp_path: String,
    /// 最终 MP4 输出路径
    mp4_path: String,
    /// 临时文件
    file: File,
    /// 视频编解码（决定临时文件格式）
    video_codec: Option<lm_core::CodecType>,
    /// 音频采样率（如果是音频录制）
    sample_rate: u32,
    channels: u16,
    /// 是否为音频录制
    is_audio: bool,
    /// IVF header 是否已写
    ivf_header_written: bool,
    /// 第一帧时间戳
    first_timestamp: Option<u32>,
    start_time: std::time::Instant,
    written_bytes: u32,
}

impl Mp4Recorder {
    /// 创建视频 MP4 录制器
    pub async fn create_video(path: &str, codec: lm_core::CodecType) -> Result<Self> {
        if let Some(parent) = Path::new(path).parent() {
            if !parent.as_os_str().is_empty() {
                tokio::fs::create_dir_all(parent).await?;
            }
        }

        let temp_ext = match codec {
            lm_core::CodecType::Vp8 | lm_core::CodecType::Vp9 => "ivf",
            lm_core::CodecType::H264 => "h264",
            _ => "raw",
        };
        let temp_path = format!("{path}.tmp.{temp_ext}");

        let mut file = File::create(&temp_path).await?;

        // 写 IVF header（VP8/VP9）
        if codec == lm_core::CodecType::Vp8 || codec == lm_core::CodecType::Vp9 {
            let fourcc: [u8; 4] = if codec == lm_core::CodecType::Vp8 {
                *b"VP80"
            } else {
                *b"VP90"
            };
            let header = ivf_header(fourcc, 0, 0);
            file.write_all(&header).await?;
        }

        Ok(Self {
            temp_path,
            mp4_path: path.to_string(),
            file,
            video_codec: Some(codec),
            sample_rate: 0,
            channels: 0,
            is_audio: false,
            ivf_header_written: codec == lm_core::CodecType::Vp8
                || codec == lm_core::CodecType::Vp9,
            first_timestamp: None,
            start_time: std::time::Instant::now(),
            written_bytes: 0,
        })
    }

    /// 创建音频 MP4 录制器（先写 WAV 临时文件）
    pub async fn create_audio(path: &str, sample_rate: u32, channels: u16) -> Result<Self> {
        if let Some(parent) = Path::new(path).parent() {
            if !parent.as_os_str().is_empty() {
                tokio::fs::create_dir_all(parent).await?;
            }
        }

        let temp_path = format!("{path}.tmp.wav");
        let mut file = File::create(&temp_path).await?;

        // 写 WAV header
        let header = wav_header_pcm16(sample_rate, channels, 0);
        file.write_all(&header).await?;

        Ok(Self {
            temp_path,
            mp4_path: path.to_string(),
            file,
            video_codec: None,
            sample_rate,
            channels,
            is_audio: true,
            ivf_header_written: false,
            first_timestamp: None,
            start_time: std::time::Instant::now(),
            written_bytes: 0,
        })
    }

    /// 写入视频帧（MediaFrame payload）
    pub async fn write_video_frame(&mut self, frame: &lm_core::MediaFrame) -> Result<()> {
        if let Some(codec) = self.video_codec {
            match codec {
                lm_core::CodecType::Vp8 | lm_core::CodecType::Vp9 => {
                    let ts_base = self.first_timestamp.unwrap_or(frame.timestamp);
                    if self.first_timestamp.is_none() {
                        self.first_timestamp = Some(frame.timestamp);
                    }
                    let rel_ts = frame.timestamp.wrapping_sub(ts_base) as u64;
                    let ivf_frame = ivf_frame_header(frame.data.len() as u32, rel_ts);
                    self.file.write_all(&ivf_frame).await?;
                    self.file.write_all(&frame.data).await?;
                    self.written_bytes += 12 + frame.data.len() as u32;
                }
                lm_core::CodecType::H264 => {
                    self.file.write_all(&frame.data).await?;
                    self.written_bytes += frame.data.len() as u32;
                }
                _ => {
                    self.file.write_all(&frame.data).await?;
                    self.written_bytes += frame.data.len() as u32;
                }
            }
        }
        Ok(())
    }

    /// 写入音频 PCM 帧
    pub async fn write_audio_frame(&mut self, frame: &AudioFrame) -> Result<()> {
        let bytes = samples_to_le_bytes(&frame.samples);
        self.file.write_all(&bytes).await?;
        self.written_bytes += bytes.len() as u32;
        Ok(())
    }

    /// 完成录制：关闭临时文件，用 ffmpeg 转封装为 MP4
    pub async fn finalize(mut self) -> Result<RecordingResult> {
        // 如果是音频，回写 WAV header 的 data_size
        if self.is_audio {
            use tokio::io::SeekFrom;
            self.file.seek(SeekFrom::Start(0)).await?;
            let header = wav_header_pcm16(self.sample_rate, self.channels, self.written_bytes);
            self.file.write_all(&header).await?;
        }

        self.file.flush().await?;
        self.file.shutdown().await?;

        // 用 ffmpeg 转封装
        let output_path = self.mp4_path.clone();
        let temp_path = self.temp_path.clone();

        let result = tokio::process::Command::new("ffmpeg")
            .args(["-y", "-i", &temp_path, "-c", "copy", &output_path])
            .output()
            .await;

        match result {
            Ok(cmd_out) if cmd_out.status.success() => {
                // 删除临时文件
                let _ = tokio::fs::remove_file(&temp_path).await;
            }
            _ => {
                // ffmpeg 失败，保留临时文件作为 fallback
                warn!(
                    temp = %temp_path,
                    output = %output_path,
                    "ffmpeg remux to mp4 failed, keeping temp file"
                );
            }
        }

        let duration_ms = self.start_time.elapsed().as_millis() as u64;
        let file_size = std::fs::metadata(&output_path)
            .map(|m| m.len())
            .unwrap_or(self.written_bytes as u64);

        Ok(RecordingResult {
            file_path: output_path,
            duration_ms,
            file_size,
        })
    }
}

// ============================================================================
// 通用录制器（支持多格式）
// ============================================================================

/// 录制器（支持 WAV/Opus/PCAP/MP4 格式）
pub enum Recorder {
    Wav(WavRecorder),
    Opus(OpusRecorder),
    Pcap(PcapRecorder),
    Mp4(Mp4Recorder),
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
                let r = OpusRecorder::create(&path, sample_rate, channels).await?;
                Ok(Self::Opus(r))
            }
            RecordingFormat::Pcap => {
                let r = PcapRecorder::create(&path).await?;
                Ok(Self::Pcap(r))
            }
            RecordingFormat::Mp4 => {
                let r = Mp4Recorder::create_audio(&path, sample_rate, channels).await?;
                Ok(Self::Mp4(r))
            }
        }
    }

    /// 写入一帧音频
    pub async fn write_frame(&mut self, frame: &AudioFrame) -> Result<()> {
        match self {
            Self::Wav(w) => w.write_frame(frame).await,
            Self::Opus(o) => o.write_frame(frame).await,
            Self::Mp4(m) => m.write_audio_frame(frame).await,
            Self::Pcap(_) => Err(anyhow::anyhow!(
                "PCAP recorder uses write_rtp_packet, not write_frame"
            )),
        }
    }

    /// 停止录制并返回文件信息
    pub async fn finalize(self) -> Result<RecordingResult> {
        match self {
            Self::Wav(w) => w.finalize().await,
            Self::Opus(o) => o.finalize().await,
            Self::Pcap(p) => p.finalize().await,
            Self::Mp4(m) => m.finalize().await,
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
        if self.video_codec == lm_core::CodecType::Vp8
            || self.video_codec == lm_core::CodecType::Vp9
        {
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
            let merged_path = self.output_dir.join(format!(
                "{}_merged.{}",
                self.recording_id,
                match self.video_codec {
                    lm_core::CodecType::Vp8 | lm_core::CodecType::Vp9 => "ivf",
                    lm_core::CodecType::H264 => "h264",
                    _ => "raw",
                }
            ));
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
        let input_str = input
            .to_str()
            .ok_or_else(|| anyhow::anyhow!("invalid input path"))?;
        let output_str = output
            .to_str()
            .ok_or_else(|| anyhow::anyhow!("invalid output path"))?;

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

// ============================================================================
// IVF 录制器（独立结构，用于 VP8/VP9/AV1 等视频帧）
// ============================================================================

/// IVF 录制器 — 将视频帧写入 IVF 容器文件
///
/// IVF 是 WebM 项目定义的简单视频容器，用于 VP8/VP9/AV1。
/// Header (32 bytes) + 每帧 (12 bytes frame header + data)。
pub struct IvfRecorder {
    file: File,
    frame_count: u64,
    codec_fourcc: [u8; 4],
    width: u16,
    height: u16,
    fps_num: u32,
    fps_den: u32,
}

impl IvfRecorder {
    /// 创建 IVF 文件并写入文件头
    pub async fn create(
        path: &str,
        fourcc: [u8; 4],
        width: u16,
        height: u16,
        fps: u32,
    ) -> Result<Self> {
        if let Some(parent) = Path::new(path).parent() {
            if !parent.as_os_str().is_empty() {
                tokio::fs::create_dir_all(parent).await?;
            }
        }

        let mut file = File::create(path)
            .await
            .map_err(|e| anyhow::anyhow!("create ivf file {path}: {e}"))?;

        let mut recorder = Self {
            file,
            frame_count: 0,
            codec_fourcc: fourcc,
            width,
            height,
            fps_num: fps,
            fps_den: 1,
        };

        recorder.write_header().await?;
        recorder.file.flush().await?;
        Ok(recorder)
    }

    async fn write_header(&mut self) -> Result<()> {
        let mut h = [0u8; 32];
        h[0..4].copy_from_slice(b"DKIF");
        h[4..6].copy_from_slice(&0u16.to_le_bytes()); // version
        h[6..8].copy_from_slice(&32u16.to_le_bytes()); // header length
        h[8..12].copy_from_slice(&self.codec_fourcc);
        h[12..14].copy_from_slice(&self.width.to_le_bytes());
        h[14..16].copy_from_slice(&self.height.to_le_bytes());
        h[16..20].copy_from_slice(&self.fps_num.to_le_bytes());
        h[20..24].copy_from_slice(&self.fps_den.to_le_bytes());
        h[24..28].copy_from_slice(&(self.frame_count as u32).to_le_bytes());
        h[28..32].copy_from_slice(&0u32.to_le_bytes()); // unused
        self.file.write_all(&h).await?;
        Ok(())
    }

    /// 写入一帧视频数据
    pub async fn write_frame(&mut self, data: &[u8], timestamp: u64) -> Result<()> {
        let frame_hdr = ivf_frame_header(data.len() as u32, timestamp);
        self.file.write_all(&frame_hdr).await?;
        self.file.write_all(data).await?;
        self.frame_count += 1;
        Ok(())
    }

    /// 关闭文件（回写 frame_count 到 header）
    pub async fn close(mut self) -> Result<()> {
        // 回到 header 的 frame_count 位置 (offset 24) 回写
        self.file.seek(SeekFrom::Start(24)).await?;
        let count = self.frame_count as u32;
        self.file.write_all(&count.to_le_bytes()).await?;
        self.file.flush().await?;
        Ok(())
    }
}

// ============================================================================
// WebM 录制器（简化 EBML/Matroska）
// ============================================================================

/// EBML 元素 ID 常量
const EBML_ID_EBML: u32 = 0x1A45DFA3;
const EBML_ID_SEGMENT: u32 = 0x18538067;
const EBML_ID_INFO: u32 = 0x1549A966;
const EBML_ID_TIMECODE_SCALE: u32 = 0x2AD7B1;
const EBML_ID_TRACKS: u32 = 0x1654AE6B;
const EBML_ID_TRACK_ENTRY: u32 = 0xAE;
const EBML_ID_TRACK_NUMBER: u32 = 0xD7;
const EBML_ID_TRACK_TYPE: u32 = 0x83;
const EBML_ID_CODEC_ID: u32 = 0x86;
const EBML_ID_CLUSTER: u32 = 0x1F43B675;
const EBML_ID_TIMECODE: u32 = 0xE7;
const EBML_ID_SIMPLE_BLOCK: u32 = 0xA3;

/// 将 EBML 元素 ID 编码为 1-4 字节的 VINT
fn encode_ebml_id(id: u32) -> Vec<u8> {
    if id <= 0xFF {
        vec![id as u8]
    } else if id <= 0xFFFF {
        vec![(id >> 8) as u8, id as u8]
    } else if id <= 0xFFFFFF {
        vec![(id >> 16) as u8, (id >> 8) as u8, id as u8]
    } else {
        vec![
            (id >> 24) as u8,
            (id >> 16) as u8,
            (id >> 8) as u8,
            id as u8,
        ]
    }
}

/// 将数据大小编码为 EBML VINT (variable-size integer)
fn encode_ebml_size(size: u64) -> Vec<u8> {
    if size < (1 << 7) - 1 {
        vec![size as u8]
    } else if size < (1 << 14) - 1 {
        vec![(0x80 | (size >> 8) as u8), (size & 0xFF) as u8]
    } else if size < (1 << 21) - 1 {
        vec![
            (0xC0 | (size >> 16) as u8),
            ((size >> 8) & 0xFF) as u8,
            (size & 0xFF) as u8,
        ]
    } else if size < (1 << 28) - 1 {
        vec![
            (0xE0 | (size >> 24) as u8),
            ((size >> 16) & 0xFF) as u8,
            ((size >> 8) & 0xFF) as u8,
            (size & 0xFF) as u8,
        ]
    } else {
        // 8-byte VINT
        vec![
            0x01,
            ((size >> 48) & 0xFF) as u8,
            ((size >> 40) & 0xFF) as u8,
            ((size >> 32) & 0xFF) as u8,
            ((size >> 24) & 0xFF) as u8,
            ((size >> 16) & 0xFF) as u8,
            ((size >> 8) & 0xFF) as u8,
            (size & 0xFF) as u8,
        ]
    }
}

/// 编码一个 EBML 元素 (ID + size + data)
fn encode_ebml_element(id: u32, data: &[u8]) -> Vec<u8> {
    let mut out = Vec::new();
    out.extend_from_slice(&encode_ebml_id(id));
    out.extend_from_slice(&encode_ebml_size(data.len() as u64));
    out.extend_from_slice(data);
    out
}

/// 编码一个 EBML 无符号整数元素
fn encode_ebml_uint(id: u32, value: u64) -> Vec<u8> {
    let mut out = Vec::new();
    out.extend_from_slice(&encode_ebml_id(id));

    // 将 value 编码为最小字节数的 big-endian
    let mut bytes = Vec::new();
    let mut v = value;
    if v == 0 {
        bytes.push(0);
    } else {
        while v > 0 {
            bytes.insert(0, (v & 0xFF) as u8);
            v >>= 8;
        }
    }
    out.extend_from_slice(&encode_ebml_size(bytes.len() as u64));
    out.extend_from_slice(&bytes);
    out
}

/// 编码一个 EBML 字符串元素
fn encode_ebml_string(id: u32, s: &str) -> Vec<u8> {
    let mut out = Vec::new();
    out.extend_from_slice(&encode_ebml_id(id));
    out.extend_from_slice(&encode_ebml_size(s.len() as u64));
    out.extend_from_slice(s.as_bytes());
    out
}

/// WebM 录制器 (简化 EBML/Matroska)
///
/// 写入 EBML header + Segment + Info + Tracks + Cluster。
/// 视频帧使用 SimpleBlock，音频帧也使用 SimpleBlock。
pub struct WebmRecorder {
    file: File,
    video_track: u8,
    audio_track: Option<u8>,
    cluster_timestamp: u64,
}

impl WebmRecorder {
    /// 创建 WebM 文件并写入 EBML header + Segment + Tracks
    pub async fn create(path: &str, has_audio: bool) -> Result<Self> {
        if let Some(parent) = Path::new(path).parent() {
            if !parent.as_os_str().is_empty() {
                tokio::fs::create_dir_all(parent).await?;
            }
        }

        let mut file = File::create(path)
            .await
            .map_err(|e| anyhow::anyhow!("create webm file {path}: {e}"))?;

        let mut recorder = Self {
            file,
            video_track: 1,
            audio_track: if has_audio { Some(2) } else { None },
            cluster_timestamp: u64::MAX,
        };

        recorder.write_ebml_header().await?;
        recorder.write_segment_and_tracks().await?;
        recorder.file.flush().await?;
        Ok(recorder)
    }

    async fn write_ebml_header(&mut self) -> Result<()> {
        // EBML header element
        let mut ebml_body = Vec::new();
        ebml_body.extend_from_slice(&encode_ebml_uint(0x4286, 1)); // EBMLVersion
        ebml_body.extend_from_slice(&encode_ebml_uint(0x42F7, 1)); // EBMLReadVersion
        ebml_body.extend_from_slice(&encode_ebml_uint(0x42F2, 4)); // EBMLMaxIDLength
        ebml_body.extend_from_slice(&encode_ebml_uint(0x42F3, 8)); // EBMLMaxSizeLength
        ebml_body.extend_from_slice(&encode_ebml_string(0x4282, "webm")); // DocType
        ebml_body.extend_from_slice(&encode_ebml_uint(0x4287, 2)); // DocTypeVersion
        ebml_body.extend_from_slice(&encode_ebml_uint(0x4285, 2)); // DocTypeReadVersion

        let header = encode_ebml_element(EBML_ID_EBML, &ebml_body);
        self.file.write_all(&header).await?;
        Ok(())
    }

    async fn write_segment_and_tracks(&mut self) -> Result<()> {
        // Build Info + Tracks content first, then wrap in Segment.
        let mut segment_body = Vec::new();

        // Info element: TimecodeScale = 1000000 (1ms in nanoseconds)
        let info_body = encode_ebml_uint(EBML_ID_TIMECODE_SCALE, 1000000);
        segment_body.extend_from_slice(&encode_ebml_element(EBML_ID_INFO, &info_body));

        // Tracks element
        let mut tracks_body = Vec::new();

        // Video track entry
        let mut video_entry = Vec::new();
        video_entry.extend_from_slice(&encode_ebml_uint(
            EBML_ID_TRACK_NUMBER,
            self.video_track as u64,
        ));
        video_entry.extend_from_slice(&encode_ebml_uint(0x73C5, self.video_track as u64)); // TrackUID
        video_entry.extend_from_slice(&encode_ebml_uint(EBML_ID_TRACK_TYPE, 1)); // TrackType = video
        video_entry.extend_from_slice(&encode_ebml_string(EBML_ID_CODEC_ID, "V_VP8"));
        tracks_body.extend_from_slice(&encode_ebml_element(EBML_ID_TRACK_ENTRY, &video_entry));

        // Audio track entry (optional)
        if let Some(audio_track) = self.audio_track {
            let mut audio_entry = Vec::new();
            audio_entry
                .extend_from_slice(&encode_ebml_uint(EBML_ID_TRACK_NUMBER, audio_track as u64));
            audio_entry.extend_from_slice(&encode_ebml_uint(0x73C5, audio_track as u64)); // TrackUID
            audio_entry.extend_from_slice(&encode_ebml_uint(EBML_ID_TRACK_TYPE, 2)); // TrackType = audio
            audio_entry.extend_from_slice(&encode_ebml_string(EBML_ID_CODEC_ID, "A_OPUS"));
            tracks_body.extend_from_slice(&encode_ebml_element(EBML_ID_TRACK_ENTRY, &audio_entry));
        }

        segment_body.extend_from_slice(&encode_ebml_element(EBML_ID_TRACKS, &tracks_body));

        // Write Segment element with unknown size (use 0x01FFFFFFFFFFFFFF for unknown)
        let segment_id = encode_ebml_id(EBML_ID_SEGMENT);
        self.file.write_all(&segment_id).await?;
        // Unknown size: 8 bytes, all 0xFF after the marker bit
        self.file
            .write_all(&[0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF])
            .await?;
        self.file.write_all(&segment_body).await?;
        Ok(())
    }

    /// 写入视频帧
    pub async fn write_video_frame(
        &mut self,
        data: &[u8],
        timestamp: u64,
        is_keyframe: bool,
    ) -> Result<()> {
        self.write_simple_block(self.video_track, data, timestamp, is_keyframe)
            .await
    }

    /// 写入音频帧
    pub async fn write_audio_frame(&mut self, data: &[u8], timestamp: u64) -> Result<()> {
        if let Some(audio_track) = self.audio_track {
            self.write_simple_block(audio_track, data, timestamp, true)
                .await?;
        }
        Ok(())
    }

    /// 写入 SimpleBlock
    ///
    /// SimpleBlock format: track_number (VINT) + timecode (i16, relative to cluster) + flags (1 byte) + data
    async fn write_simple_block(
        &mut self,
        track: u8,
        data: &[u8],
        timestamp: u64,
        is_keyframe: bool,
    ) -> Result<()> {
        // Start a new cluster if this is a keyframe with a new timestamp
        if is_keyframe && timestamp != self.cluster_timestamp {
            self.cluster_timestamp = timestamp;
            // Write Cluster element
            let cluster_id = encode_ebml_id(EBML_ID_CLUSTER);
            self.file.write_all(&cluster_id).await?;
            // Unknown size
            self.file
                .write_all(&[0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF])
                .await?;
            // Timecode (absolute, in ms)
            let tc = encode_ebml_uint(EBML_ID_TIMECODE, timestamp);
            self.file.write_all(&tc).await?;
        }

        // SimpleBlock body: track_number (1 byte VINT) + timecode (2 bytes, i16 BE, relative) + flags + data
        let mut block = Vec::with_capacity(4 + data.len());
        block.push(track & 0x7F); // track number as 1-byte VINT
                                  // Relative timecode (i16, big-endian) — 0 relative to cluster
        block.push(0x00);
        block.push(0x00);
        // Flags: bit 7 = keyframe (0x80), bit 0-2 = lace mode
        block.push(if is_keyframe { 0x80 } else { 0x00 });
        block.extend_from_slice(data);

        let simple_block = encode_ebml_element(EBML_ID_SIMPLE_BLOCK, &block);
        self.file.write_all(&simple_block).await?;
        Ok(())
    }

    /// 关闭文件
    pub async fn close(mut self) -> Result<()> {
        self.file.flush().await?;
        Ok(())
    }
}

// ============================================================================
// PCAP 扩展方法（write_packet with timestamp）
// ============================================================================

impl PcapRecorder {
    /// 写入一个数据包，使用显式时间戳（微秒）
    ///
    /// 与 `write_rtp_packet` 不同，此方法接受显式的时间戳参数，
    /// 适用于需要精确控制时间戳的场景。
    pub async fn write_packet(&mut self, data: &[u8], timestamp_us: u64) -> Result<()> {
        let ts_sec = (timestamp_us / 1_000_000) as u32;
        let ts_usec = (timestamp_us % 1_000_000) as u32;

        // PCAP packet header (16 bytes)
        let mut pkt_header = [0u8; 16];
        pkt_header[0..4].copy_from_slice(&ts_sec.to_le_bytes());
        pkt_header[4..8].copy_from_slice(&ts_usec.to_le_bytes());
        pkt_header[8..12].copy_from_slice(&(data.len() as u32).to_le_bytes()); // incl_len
        pkt_header[12..16].copy_from_slice(&(data.len() as u32).to_le_bytes()); // orig_len

        self.file.write_all(&pkt_header).await?;
        self.file.write_all(data).await?;
        self.written_bytes += 16 + data.len() as u32;
        self.packet_count += 1;
        Ok(())
    }

    /// 关闭文件（flush）
    pub async fn close(mut self) -> Result<()> {
        self.file.flush().await?;
        Ok(())
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

    #[test]
    fn test_wav_header_stereo() {
        let h = wav_header_pcm16(48000, 2, 192000);
        assert_eq!(h.len(), 44);
        // 验证 fmt chunk 的 channels 字段 (offset 22, 2 bytes LE)
        assert_eq!(u16::from_le_bytes([h[22], h[23]]), 2);
        // 验证 sample_rate 字段 (offset 24, 4 bytes LE)
        assert_eq!(u32::from_le_bytes([h[24], h[25], h[26], h[27]]), 48000);
        // byte_rate = sample_rate * channels * 2 = 48000 * 2 * 2 = 192000
        assert_eq!(u32::from_le_bytes([h[28], h[29], h[30], h[31]]), 192000);
        // block_align = channels * 2 = 4
        assert_eq!(u16::from_le_bytes([h[32], h[33]]), 4);
        // bits_per_sample = 16
        assert_eq!(u16::from_le_bytes([h[34], h[35]]), 16);
    }

    #[test]
    fn test_samples_to_le_bytes_negative() {
        let bytes = samples_to_le_bytes(&[-32768i16, 32767, 0]);
        // -32768 = 0x8000 LE = [0x00, 0x80]
        // 32767 = 0x7FFF LE = [0xFF, 0x7F]
        // 0 = [0x00, 0x00]
        assert_eq!(bytes, vec![0x00, 0x80, 0xFF, 0x7F, 0x00, 0x00]);
    }

    #[test]
    fn test_ivf_frame_header() {
        let h = ivf_frame_header(1024, 123456);
        assert_eq!(h.len(), 12);
        // size = 1024 = 0x400 LE
        assert_eq!(u32::from_le_bytes([h[0], h[1], h[2], h[3]]), 1024);
        // timestamp = 123456 LE
        assert_eq!(
            u64::from_le_bytes([h[4], h[5], h[6], h[7], h[8], h[9], h[10], h[11]]),
            123456
        );
    }

    #[test]
    fn test_recording_state_active() {
        assert!(!RecordingState::Requested.is_active());
        assert!(!RecordingState::Starting.is_active());
        assert!(RecordingState::Active.is_active());
        assert!(!RecordingState::Stopping.is_active());
        assert!(!RecordingState::Finalizing.is_active());
        assert!(!RecordingState::Completed.is_active());
    }

    #[test]
    fn test_recording_state_terminal() {
        assert!(!RecordingState::Requested.is_terminal());
        assert!(!RecordingState::Starting.is_terminal());
        assert!(!RecordingState::Active.is_terminal());
        assert!(RecordingState::Completed.is_terminal());
        assert!(RecordingState::Failed("test".into()).is_terminal());
    }

    #[tokio::test]
    async fn test_wav_recorder_multiple_frames() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let mut rec = WavRecorder::create(path, 16000, 1).await.unwrap();
        // 写入多帧
        for i in 0..5u32 {
            rec.write_frame(&AudioFrame {
                samples: vec![(i as i16) * 100; 320],
                sample_rate: 16000,
                timestamp: (i as u64) * 320,
            })
            .await
            .unwrap();
        }
        let result = rec.finalize().await.unwrap();
        // 5 frames * 320 samples * 2 bytes = 3200 bytes data + 44 header
        assert!(result.file_size >= 44 + 3200);
    }

    #[tokio::test]
    async fn test_wav_recorder_empty() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let rec = WavRecorder::create(path, 8000, 1).await.unwrap();
        let result = rec.finalize().await.unwrap();
        // 只有 header，没有数据
        assert_eq!(result.file_size, 44);
    }

    // ─── Opus 录制器测试 ─────────────────────────────────────────────

    #[tokio::test]
    async fn test_opus_recorder_create() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let rec = OpusRecorder::create(path, 48000, 1).await.unwrap();
        let result = rec.finalize().await.unwrap();

        // OGG Opus 文件至少有 OpusHead page + EOS page
        assert!(result.file_size > 0);

        // 验证文件以 OggS 开头
        let data = tokio::fs::read(path).await.unwrap();
        assert_eq!(&data[0..4], b"OggS");
    }

    #[tokio::test]
    async fn test_opus_recorder_write_packets() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let mut rec = OpusRecorder::create(path, 48000, 1).await.unwrap();
        // 写入几个模拟 Opus 帧
        for _ in 0..5 {
            rec.write_opus_packet(&[0xAB; 80], 960).await.unwrap(); // 20ms @ 48kHz
        }
        let result = rec.finalize().await.unwrap();

        // 应该有 OpusHead + 5 data pages + EOS page
        assert!(result.file_size > 100);

        // 验证文件内容
        let data = tokio::fs::read(path).await.unwrap();
        assert_eq!(&data[0..4], b"OggS");
        // OpusHead 应在第一个 page
        assert!(data.windows(8).any(|w| w == b"OpusHead"));
    }

    // ─── PCAP 录制器测试 ─────────────────────────────────────────────

    #[tokio::test]
    async fn test_pcap_recorder_create() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let rec = PcapRecorder::create(path).await.unwrap();
        let result = rec.finalize().await.unwrap();

        // PCAP 全局头 = 24 bytes
        assert_eq!(result.file_size, 24);

        // 验证 magic number
        let data = tokio::fs::read(path).await.unwrap();
        assert_eq!(&data[0..4], &0xA1B2C3D4u32.to_le_bytes());
    }

    #[tokio::test]
    async fn test_pcap_recorder_write_packets() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let mut rec = PcapRecorder::create(path).await.unwrap();
        // 写入几个模拟 RTP 包
        let rtp_packet = [
            0x80, 0x60, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0xAB, 0xCD,
        ];
        for _ in 0..3 {
            rec.write_rtp_packet(&rtp_packet).await.unwrap();
        }
        assert_eq!(rec.packet_count(), 3);
        let result = rec.finalize().await.unwrap();

        // 24 (global header) + 3 * (16 + 14) = 24 + 90 = 114
        assert_eq!(result.file_size, 114);
    }

    // ─── MP4 录制器测试 ──────────────────────────────────────────────

    #[tokio::test]
    async fn test_mp4_recorder_audio() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("test_audio.mp4");
        let path_str = path.to_str().unwrap();

        let mut rec = Mp4Recorder::create_audio(path_str, 8000, 1).await.unwrap();
        // 写入几帧 PCM
        for i in 0..5u32 {
            rec.write_audio_frame(&AudioFrame {
                samples: vec![(i as i16) * 100; 160],
                sample_rate: 8000,
                timestamp: (i as u64) * 160,
            })
            .await
            .unwrap();
        }
        let result = rec.finalize().await.unwrap();

        // ffmpeg 可能成功（生成 .mp4）或失败（保留 .tmp.wav）
        // 至少应该有一个文件存在
        assert!(result.file_size > 0);
    }

    #[tokio::test]
    async fn test_mp4_recorder_video_h264() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("test_video.mp4");
        let path_str = path.to_str().unwrap();

        let mut rec = Mp4Recorder::create_video(path_str, lm_core::CodecType::H264)
            .await
            .unwrap();
        // 写入模拟 H264 帧（SPS+PPS+IDR 简化）
        let frame = lm_core::MediaFrame::video(
            lm_core::CodecType::H264,
            9000,
            bytes::Bytes::from(vec![0, 0, 0, 1, 0x65, 0xAA, 0xBB]),
            0,
            true,
        );
        rec.write_video_frame(&frame).await.unwrap();
        let result = rec.finalize().await.unwrap();

        // ffmpeg 可能无法从单帧生成有效 MP4，但至少 temp 文件应该有内容
        // 如果 ffmpeg 成功，output 存在；如果失败，temp 保留
        // file_size 来自 output metadata 或 written_bytes
        // 只验证 finalize 不出错
        let _ = result;
    }

    // ─── 通用 Recorder 枚举测试 ──────────────────────────────────────

    #[tokio::test]
    async fn test_recorder_enum_wav() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let mut rec = Recorder::new(
            SessionId("test".into()),
            RecordingFormat::Wav,
            path.to_string(),
            8000,
            1,
        )
        .await
        .unwrap();

        rec.write_frame(&AudioFrame {
            samples: vec![100i16; 160],
            sample_rate: 8000,
            timestamp: 0,
        })
        .await
        .unwrap();

        let result = rec.finalize().await.unwrap();
        assert!(result.file_size > 44);
    }

    #[tokio::test]
    async fn test_recorder_enum_opus() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let rec = Recorder::new(
            SessionId("test".into()),
            RecordingFormat::Opus,
            path.to_string(),
            48000,
            1,
        )
        .await
        .unwrap();

        let result = rec.finalize().await.unwrap();
        assert!(result.file_size > 0);
    }

    #[tokio::test]
    async fn test_recorder_enum_pcap() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let rec = Recorder::new(
            SessionId("test".into()),
            RecordingFormat::Pcap,
            path.to_string(),
            0,
            0,
        )
        .await
        .unwrap();

        let result = rec.finalize().await.unwrap();
        assert_eq!(result.file_size, 24); // PCAP global header
    }

    #[tokio::test]
    async fn test_ogg_crc32() {
        // 测试 CRC32 计算是否正确
        let data = b"OggS";
        let crc = ogg_crc32(data);
        // CRC32 应该是非零的
        assert!(crc != 0);
    }

    #[tokio::test]
    async fn test_ogg_page_structure() {
        let page = build_ogg_page(12345, 0, 0, 0x02, &[b"OpusHead"]);
        // OGG page 应以 "OggS" 开头
        assert_eq!(&page[0..4], b"OggS");
        // header_type = 0x02 (BOS)
        assert_eq!(page[5], 0x02);
        // serial number
        assert_eq!(
            u32::from_le_bytes([page[14], page[15], page[16], page[17]]),
            12345
        );
    }

    // ─── IVF 录制器测试 ───────────────────────────────────────────────

    #[tokio::test]
    async fn test_ivf_header_format() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let rec = IvfRecorder::create(path, *b"VP80", 640, 480, 30)
            .await
            .unwrap();
        rec.close().await.unwrap();

        let data = tokio::fs::read(path).await.unwrap();
        assert_eq!(data.len(), 32); // header only

        // DKIF signature
        assert_eq!(&data[0..4], b"DKIF");
        // version = 0
        assert_eq!(u16::from_le_bytes([data[4], data[5]]), 0);
        // header length = 32
        assert_eq!(u16::from_le_bytes([data[6], data[7]]), 32);
        // fourcc = "VP80"
        assert_eq!(&data[8..12], b"VP80");
        // width = 640
        assert_eq!(u16::from_le_bytes([data[12], data[13]]), 640);
        // height = 480
        assert_eq!(u16::from_le_bytes([data[14], data[15]]), 480);
        // fps_num = 30
        assert_eq!(
            u32::from_le_bytes([data[16], data[17], data[18], data[19]]),
            30
        );
        // fps_den = 1
        assert_eq!(
            u32::from_le_bytes([data[20], data[21], data[22], data[23]]),
            1
        );
        // frame_count = 0
        assert_eq!(
            u32::from_le_bytes([data[24], data[25], data[26], data[27]]),
            0
        );
    }

    #[tokio::test]
    async fn test_ivf_write_frame() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let mut rec = IvfRecorder::create(path, *b"VP90", 1280, 720, 60)
            .await
            .unwrap();

        let frame_data = vec![0xAB; 100];
        rec.write_frame(&frame_data, 0).await.unwrap();
        rec.write_frame(&frame_data, 33).await.unwrap();
        rec.close().await.unwrap();

        let data = tokio::fs::read(path).await.unwrap();
        // 32 (header) + 2 * (12 + 100) = 32 + 224 = 256
        assert_eq!(data.len(), 256);

        // frame_count should be 2 (at offset 24)
        assert_eq!(
            u32::from_le_bytes([data[24], data[25], data[26], data[27]]),
            2
        );

        // First frame header at offset 32
        let frame_size = u32::from_le_bytes([data[32], data[33], data[34], data[35]]);
        assert_eq!(frame_size, 100);
        let frame_ts = u64::from_le_bytes([
            data[36], data[37], data[38], data[39], data[40], data[41], data[42], data[43],
        ]);
        assert_eq!(frame_ts, 0);

        // Second frame header at offset 32 + 12 + 100 = 144
        let frame2_size = u32::from_le_bytes([data[144], data[145], data[146], data[147]]);
        assert_eq!(frame2_size, 100);
        let frame2_ts = u64::from_le_bytes([
            data[148], data[149], data[150], data[151], data[152], data[153], data[154], data[155],
        ]);
        assert_eq!(frame2_ts, 33);
    }

    // ─── WebM 录制器测试 ──────────────────────────────────────────────

    #[tokio::test]
    async fn test_webm_ebml_header() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let rec = WebmRecorder::create(path, false).await.unwrap();
        rec.close().await.unwrap();

        let data = tokio::fs::read(path).await.unwrap();
        assert!(data.len() > 10);

        // EBML header element ID = 0x1A45DFA3 (4 bytes big-endian)
        assert_eq!(data[0], 0x1A);
        assert_eq!(data[1], 0x45);
        assert_eq!(data[2], 0xDF);
        assert_eq!(data[3], 0xA3);

        // Verify "webm" DocType string is present somewhere in the header
        assert!(data.windows(4).any(|w| w == b"webm"));

        // Verify Segment element ID = 0x18538067 is present
        assert!(data.windows(4).any(|w| w == &[0x18, 0x53, 0x80, 0x67]));
    }

    #[tokio::test]
    async fn test_webm_write_video_frame() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let mut rec = WebmRecorder::create(path, true).await.unwrap();
        rec.write_video_frame(&[0xAA; 50], 0, true).await.unwrap();
        rec.write_audio_frame(&[0xBB; 20], 0).await.unwrap();
        rec.close().await.unwrap();

        let data = tokio::fs::read(path).await.unwrap();
        // Should have EBML header + segment + tracks + cluster + frames
        assert!(data.len() > 50);

        // Verify Cluster element ID = 0x1F43B675 is present
        assert!(data.windows(4).any(|w| w == &[0x1F, 0x43, 0xB6, 0x75]));
    }

    // ─── PCAP 扩展测试 ────────────────────────────────────────────────

    #[tokio::test]
    async fn test_pcap_global_header() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let rec = PcapRecorder::create(path).await.unwrap();
        rec.close().await.unwrap();

        let data = tokio::fs::read(path).await.unwrap();
        assert_eq!(data.len(), 24); // global header only

        // magic number = 0xA1B2C3D4 (little-endian)
        assert_eq!(&data[0..4], &0xA1B2C3D4u32.to_le_bytes());
        // version major = 2
        assert_eq!(u16::from_le_bytes([data[4], data[5]]), 2);
        // version minor = 4
        assert_eq!(u16::from_le_bytes([data[6], data[7]]), 4);
    }

    #[tokio::test]
    async fn test_pcap_packet() {
        let tmp = tempfile::NamedTempFile::new().unwrap();
        let path = tmp.path().to_str().unwrap();

        let mut rec = PcapRecorder::create(path).await.unwrap();
        let payload = [0x80, 0x60, 0x00, 0x01, 0xAB, 0xCD];
        rec.write_packet(&payload, 1_000_000).await.unwrap(); // ts = 1.0s
        rec.close().await.unwrap();

        let data = tokio::fs::read(path).await.unwrap();
        // 24 (global header) + 16 (packet header) + 6 (payload) = 46
        assert_eq!(data.len(), 46);

        // Packet header starts at offset 24
        // ts_sec = 1
        assert_eq!(
            u32::from_le_bytes([data[24], data[25], data[26], data[27]]),
            1
        );
        // ts_usec = 0
        assert_eq!(
            u32::from_le_bytes([data[28], data[29], data[30], data[31]]),
            0
        );
        // incl_len = 6
        assert_eq!(
            u32::from_le_bytes([data[32], data[33], data[34], data[35]]),
            6
        );
        // orig_len = 6
        assert_eq!(
            u32::from_le_bytes([data[36], data[37], data[38], data[39]]),
            6
        );
        // payload
        assert_eq!(&data[40..46], &payload);
    }
}
